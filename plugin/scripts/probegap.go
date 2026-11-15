// Command probegap checks the httpGet and tcpSocket probes in Kubernetes
// manifests against the servers a Go program really starts. It builds the
// program with net.Listen and http.Server.Serve hooked, runs it with each
// container's arguments and environment, records the listeners and
// handlers it sets up without opening any port, and sends each probe's
// request to the recorded handler the way the kubelet does.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const version = "0.1.0"

// The kubelet's User-Agent is kube-probe/<major>.<minor> of its version.
const userAgent = "kube-probe/1.34"

const (
	sevNote = iota
	sevWarn
	sevError
)

var sevNames = map[int]string{sevNote: "note", sevWarn: "WARN", sevError: "ERROR"}

type finding struct {
	sev       int
	Severity  string `json:"severity"`
	Code      string `json:"code"`
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	Container string `json:"container,omitempty"`
	Probe     string `json:"probe,omitempty"`
	Message   string `json:"message"`
	Fix       string `json:"fix,omitempty"`
	File      string `json:"file,omitempty"`
}

// inputError means the input can't be trusted; nothing is reported.
type inputError struct{ msg string }

func (e *inputError) Error() string { return e.msg }

func untrusted(format string, a ...any) error {
	return &inputError{fmt.Sprintf(format, a...)}
}

// argTok is one command-line argument after Kubernetes $(VAR) expansion.
type argTok struct {
	text    string
	raw     string // as written in the manifest, before $(VAR) expansion
	unknown bool   // part of it comes from a variable whose value isn't in the input
	vars    string // the variables it couldn't resolve, for messages
}

// where says which container a finding belongs to.
type where struct {
	Kind, Namespace, Name, Container, File string
}

func (w where) String() string {
	ns := ""
	if w.Namespace != "" {
		ns = w.Namespace + "/"
	}
	return fmt.Sprintf("%s %s%s, container %s", w.Kind, ns, w.Name, w.Container)
}

type containerReport struct {
	Kind      string      `json:"kind"`
	Namespace string      `json:"namespace,omitempty"`
	Name      string      `json:"name"`
	Container string      `json:"container"`
	Args      []string    `json:"args"`
	Servers   []serverOut `json:"servers"`
	Listens   []listenOut `json:"listens"`
	listened  string
}

type report struct {
	Version    string             `json:"version"`
	Program    string             `json:"program"`
	Probes     int                `json:"probes"`
	Checked    int                `json:"checked_probes"`
	Containers []*containerReport `json:"containers"`
	Findings   []*finding         `json:"findings"`
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("probegap", flag.ContinueOnError)
	fl.SetOutput(stderr)
	pkg := fl.String("pkg", ".", "the Go main package: a directory, or a package pattern resolved in the current directory")
	ctrs := fl.String("container", "", "comma-separated names of the containers that run the program; needed when more than one container has probes")
	format := fl.String("format", "text", "output format: text or json")
	failOn := fl.String("fail-on", "error", "exit 1 when a finding is at or above this level: error, warn or never")
	wait := fl.Duration("wait", 10*time.Second, "how long the program may take to start the servers the probes need")
	entry := fl.String("entrypoint", "", "space-separated words the image's ENTRYPOINT passes before the container's args, such as a subcommand; used for containers that set no command")
	var extraEnv multiFlag
	fl.Var(&extraEnv, "env", "KEY=VALUE to set when the program runs, for values from Secrets or anything else the manifest doesn't hold; repeatable")
	showVersion := fl.Bool("version", false, "print the version")
	fl.Usage = func() {
		fmt.Fprintf(stderr, `usage: probegap [flags] MANIFEST|DIR|- ...

Checks the httpGet and tcpSocket probes in rendered Kubernetes manifests
against the servers the Go program at --pkg really starts.
Exit 0: nothing at or above --fail-on. Exit 1: findings. Exit 2: input can't be trusted or bad usage.

`)
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "probegap", version)
		return 0
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintln(stderr, "probegap: --format must be text or json")
		return 2
	}
	if *failOn != "error" && *failOn != "warn" && *failOn != "never" {
		fmt.Fprintln(stderr, "probegap: --fail-on must be error, warn or never")
		return 2
	}
	if *wait <= 0 {
		fmt.Fprintln(stderr, "probegap: --wait must be positive")
		return 2
	}
	for _, e := range extraEnv {
		if k, _, ok := strings.Cut(e, "="); !ok || k == "" {
			fmt.Fprintf(stderr, "probegap: --env %q must be KEY=VALUE\n", e)
			return 2
		}
	}
	if fl.NArg() == 0 {
		fl.Usage()
		return 2
	}

	// Read the manifests first: they are cheap, and a bad file shouldn't
	// wait for a build.
	objs, err := load(fl.Args(), stdin)
	if err != nil {
		return refuse(stderr, err)
	}
	all, _, err := containers(objs)
	if err != nil {
		return refuse(stderr, err)
	}
	type job struct {
		c      *container
		probes []*probe
		notes  []*finding
	}
	var jobs []*job
	withProbes := []*container{}
	parsed := map[*container][]*probe{}
	for _, c := range all {
		ps, err := c.probes()
		if err != nil {
			return refuse(stderr, err)
		}
		parsed[c] = ps
		if len(ps) > 0 {
			withProbes = append(withProbes, c)
		}
	}
	cs, err := pick(all, withProbes, *ctrs)
	if err != nil {
		return refuse(stderr, err)
	}
	rep := &report{Version: version, Containers: []*containerReport{}}
	var reasons []string
	for _, c := range cs {
		j := &job{c: c}
		for _, p := range parsed[c] {
			if p.skip != "" {
				j.notes = append(j.notes, c.find(sevNote, "not-checked", p, p.skip, ""))
				continue
			}
			if p.bad != nil {
				j.notes = append(j.notes, p.bad)
				continue
			}
			j.probes = append(j.probes, p)
		}
		rep.Probes += len(parsed[c])
		if len(parsed[c]) == 0 {
			reasons = append(reasons, c.at.String()+" has no probes")
			rep.Findings = append(rep.Findings, c.find(sevNote, "not-checked", nil, "this container has no probes", ""))
			continue
		}
		if len(j.probes) == 0 {
			var why []string
			for _, n := range j.notes {
				why = append(why, n.Probe+": "+n.Message)
			}
			reasons = append(reasons, c.at.String()+" has no httpGet or tcpSocket probe probegap can check ("+strings.Join(why, "; ")+")")
			rep.Findings = append(rep.Findings, j.notes...)
			continue
		}
		jobs = append(jobs, j)
	}
	if len(jobs) == 0 {
		for _, f := range rep.Findings {
			if f.sev == sevError {
				// Probes the API server would reject are worth reporting.
				return finish(stdout, rep, *format, *failOn)
			}
		}
		return refuse(stderr, untrusted("no probe could be checked: %s", strings.Join(reasons, "; ")))
	}

	dir, pattern := ".", *pkg
	if st, err := os.Stat(*pkg); err == nil && st.IsDir() {
		dir, pattern = *pkg, "."
	}
	b, err := buildProgram(dir, pattern)
	if err != nil {
		return refuse(stderr, err)
	}
	defer b.cleanup()
	b.wait = *wait
	rep.Program = b.pkgPath

	for _, j := range jobs {
		c := j.c
		argv, why := c.argv(strings.Fields(*entry))
		if why != "" {
			reasons = append(reasons, c.at.String()+": "+why)
			for _, p := range j.probes {
				rep.Findings = append(rep.Findings, c.find(sevNote, "not-checked", p, "the program's arguments can't be known: "+why, ""))
			}
			continue
		}
		var args, unknown []string
		for _, t := range argv {
			args = append(args, t.text)
			if t.unknown {
				unknown = append(unknown, t.vars)
			}
		}
		env, envUnknown := c.processEnv(extraEnv)
		unknown = append(unknown, envUnknown...)
		var plan []planProbe
		for i, p := range j.probes {
			plan = append(plan, planProbe{ID: i, Kind: p.kind, Port: p.port, Scheme: p.scheme, Path: p.path, Headers: p.headers, TimeoutMS: p.timeout * 1000, PodIP: podIP, UserAgent: userAgent})
		}
		out, output, err := b.run(args, env, plan)
		if err != nil {
			msg := fmt.Sprintf("%s: %v. Its output:\n%s", c.at, err, indent(clip(output)))
			if len(unknown) > 0 {
				msg += "\nValues probegap couldn't fill in, which may be why: " + strings.Join(dedupe(unknown), "; ") + ". Give them with --env KEY=VALUE."
			}
			return refuse(stderr, untrusted("%s", msg))
		}
		if len(out.Listens) == 0 && len(out.Servers) == 0 {
			msg := fmt.Sprintf("%s: %s started no listener within %s, so its probes can't be checked. If it waits for a database or other dependency first, give it what it needs with --env or raise --wait. Its output:\n%s", c.at, b.pkgPath, *wait, indent(clip(output)))
			if len(unknown) > 0 {
				msg += "\nValues probegap couldn't fill in: " + strings.Join(dedupe(unknown), "; ") + "."
			}
			return refuse(stderr, untrusted("%s", msg))
		}
		cr := &containerReport{Kind: c.at.Kind, Namespace: c.at.Namespace, Name: c.at.Name, Container: c.at.Container, Args: args, Servers: out.Servers, Listens: out.Listens, listened: describeServers(out)}
		if cr.Args == nil {
			cr.Args = []string{}
		}
		rep.Containers = append(rep.Containers, cr)
		rep.Findings = append(rep.Findings, j.notes...)
		for i, p := range j.probes {
			rep.Findings = append(rep.Findings, c.judge(p, i, out, *wait)...)
			rep.Checked++
		}
		for _, u := range dedupe(unknown) {
			rep.Findings = append(rep.Findings, c.find(sevNote, "unknown-value", nil, "ran without "+u+"; give it with --env KEY=VALUE if it changes where the program listens", ""))
		}
	}
	if rep.Checked == 0 {
		return refuse(stderr, untrusted("no probe could be checked: %s", strings.Join(reasons, "; ")))
	}
	return finish(stdout, rep, *format, *failOn)
}

func finish(stdout io.Writer, rep *report, format, failOn string) int {
	for _, f := range rep.Findings {
		f.Severity = sevNames[f.sev]
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool { return rep.Findings[i].sev > rep.Findings[j].sev })
	if rep.Findings == nil {
		rep.Findings = []*finding{}
	}
	if format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rep)
	} else {
		printText(stdout, rep)
	}
	limit := map[string]int{"error": sevError, "warn": sevWarn, "never": 99}[failOn]
	for _, f := range rep.Findings {
		if f.sev >= limit {
			return 1
		}
	}
	return 0
}

func refuse(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "probegap: can't trust the input, nothing was checked: %v\n", err)
	return 2
}

func indent(s string) string {
	if s == "" {
		return "  (no output)"
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

func dedupe(l []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range l {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// pick chooses the containers that run the program.
func pick(all, withProbes []*container, names string) ([]*container, error) {
	var listed []string
	for _, c := range all {
		listed = append(listed, c.at.Container)
	}
	if names == "" {
		switch {
		case len(all) == 1:
			return all, nil
		case len(withProbes) == 1:
			return withProbes, nil
		case len(withProbes) == 0:
			return nil, untrusted("none of the %d containers in the input has probes, so there is nothing to check", len(all))
		}
		uniq := map[string]bool{}
		var u []string
		for _, c := range withProbes {
			if !uniq[c.at.Container] {
				uniq[c.at.Container] = true
				u = append(u, c.at.Container)
			}
		}
		sort.Strings(u)
		if len(u) == 1 {
			return withProbes, nil
		}
		return nil, untrusted("%d containers have probes (%s); name the ones that run the program with --container", len(withProbes), strings.Join(u, ", "))
	}
	var out []*container
	for _, n := range strings.Split(names, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		found := false
		for _, c := range all {
			if c.at.Container == n {
				out = append(out, c)
				found = true
			}
		}
		if !found {
			return nil, untrusted("--container %s isn't in the input, which has: %s", n, strings.Join(listed, ", "))
		}
	}
	if len(out) == 0 {
		return nil, untrusted("--container names no containers")
	}
	return out, nil
}

// probe is one liveness, readiness or startup probe of a container.
type probe struct {
	which   string // liveness, readiness, startup
	kind    string // http, tcp
	port    int
	portRaw string
	scheme  string
	path    string
	headers [][2]string
	timeout int
	skip    string   // why it isn't checked
	bad     *finding // a probe the API server would reject
}

func (p *probe) String() string {
	if p == nil {
		return ""
	}
	switch p.kind {
	case "http":
		s := fmt.Sprintf("%s httpGet %s port %s", p.which, p.path, p.portRaw)
		if p.scheme == "HTTPS" {
			s += " HTTPS"
		}
		return s
	case "tcp":
		return fmt.Sprintf("%s tcpSocket port %s", p.which, p.portRaw)
	}
	return p.which + " " + p.kind
}

func (c *container) find(sev int, code string, p *probe, msg, fix string) *finding {
	return &finding{sev: sev, Code: code, Kind: c.at.Kind, Namespace: c.at.Namespace, Name: c.at.Name, Container: c.at.Container, Probe: p.String(), Message: msg, Fix: fix, File: c.at.File}
}

// probes reads the container's probes. A probe that is present but not
// an object, or has no handler, means the manifest can't be trusted.
func (c *container) probes() ([]*probe, error) {
	// Init containers only run probes when they are sidecars.
	if c.init {
		if rp, _ := c.raw["restartPolicy"].(string); rp != "Always" {
			return nil, nil
		}
	}
	var out []*probe
	for _, which := range []string{"startup", "liveness", "readiness"} {
		v, ok := c.raw[which+"Probe"]
		if !ok || v == nil {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, untrusted("%s: %s: %sProbe is not an object", c.at.File, c.at, which)
		}
		p := &probe{which: which, timeout: 1}
		if t, ok := m["timeoutSeconds"]; ok {
			n, ok := asInt(t)
			if !ok || n < 1 {
				return nil, untrusted("%s: %s: %sProbe.timeoutSeconds is %v, not a whole number of seconds", c.at.File, c.at, which, t)
			}
			p.timeout = n
		}
		var handler map[string]any
		switch {
		case m["httpGet"] != nil:
			p.kind = "http"
			handler, ok = m["httpGet"].(map[string]any)
		case m["tcpSocket"] != nil:
			p.kind = "tcp"
			handler, ok = m["tcpSocket"].(map[string]any)
		case m["grpc"] != nil:
			p.kind = "grpc"
			p.skip = "gRPC probes aren't checked yet"
			out = append(out, p)
			continue
		case m["exec"] != nil:
			p.kind = "exec"
			p.skip = "exec probes run a command in the image, which probegap doesn't read"
			out = append(out, p)
			continue
		default:
			return nil, untrusted("%s: %s: %sProbe has no httpGet, tcpSocket, grpc or exec handler, so it looks cut off", c.at.File, c.at, which)
		}
		if !ok {
			return nil, untrusted("%s: %s: %sProbe's handler is not an object", c.at.File, c.at, which)
		}
		portV, ok := handler["port"]
		if !ok || portV == nil {
			return nil, untrusted("%s: %s: %sProbe has no port, so it looks cut off", c.at.File, c.at, which)
		}
		if h, _ := handler["host"].(string); h != "" {
			p.portRaw = fmt.Sprint(portV)
			p.path, _ = handler["path"].(string)
			p.skip = fmt.Sprintf("it sets host %s, so the kubelet probes that host rather than this pod", h)
			out = append(out, p)
			continue
		}
		if n, isInt := asInt(portV); isInt {
			p.port, p.portRaw = n, strconv.Itoa(n)
			if n < 1 || n > 65535 {
				p.bad = c.find(sevError, "invalid-probe", p, fmt.Sprintf("port %d is out of range, and the API server rejects it", n), "")
			}
		} else if s, isStr := portV.(string); isStr {
			p.portRaw = s
			n, found := c.namedPort(s)
			switch {
			case found:
				p.port = n
			case allDigits(s):
				p.bad = c.find(sevError, "invalid-probe", p, fmt.Sprintf("port %q is a string of digits; Kubernetes reads a string as a port name, and a name needs a letter, so the API server rejects it", s), "write the number without quotes: port: "+s)
			default:
				p.bad = c.find(sevError, "named-port-missing", p, fmt.Sprintf("the container has no port named %q, so the kubelet can't resolve the probe's port and the probe fails", s), c.portNamesFix())
			}
		} else {
			return nil, untrusted("%s: %s: %sProbe's port is %v, not a number or a name", c.at.File, c.at, which, portV)
		}
		if p.kind == "http" {
			p.path, _ = handler["path"].(string)
			if p.path == "" {
				p.path = "/"
			} else if !strings.HasPrefix(p.path, "/") {
				p.path = "/" + p.path
			}
			p.scheme, _ = handler["scheme"].(string)
			if p.scheme == "" {
				p.scheme = "HTTP"
			}
			if p.scheme != "HTTP" && p.scheme != "HTTPS" && p.bad == nil {
				p.bad = c.find(sevError, "invalid-probe", p, fmt.Sprintf("scheme %q isn't HTTP or HTTPS, and the API server rejects it", p.scheme), "")
			}
			for _, it := range asList(handler["httpHeaders"]) {
				hm, _ := it.(map[string]any)
				name, _ := hm["name"].(string)
				val, _ := hm["value"].(string)
				if name == "" {
					return nil, untrusted("%s: %s: %sProbe has an httpHeaders entry with no name", c.at.File, c.at, which)
				}
				p.headers = append(p.headers, [2]string{name, val})
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case uint64:
		return int(t), true
	case float64:
		if t == float64(int(t)) {
			return int(t), true
		}
	}
	return 0, false
}

func (c *container) namedPort(name string) (int, bool) {
	for _, it := range asList(c.raw["ports"]) {
		m, _ := it.(map[string]any)
		if n, _ := m["name"].(string); n == name {
			if p, ok := asInt(m["containerPort"]); ok {
				return p, true
			}
		}
	}
	return 0, false
}

func (c *container) portNamesFix() string {
	var names []string
	for _, it := range asList(c.raw["ports"]) {
		m, _ := it.(map[string]any)
		if n, _ := m["name"].(string); n != "" {
			p, _ := asInt(m["containerPort"])
			names = append(names, fmt.Sprintf("%s (%d)", n, p))
		}
	}
	if len(names) == 0 {
		return "name the port in the container's ports list, or use the port number"
	}
	return "use one of the container's named ports: " + strings.Join(names, ", ")
}

// processEnv returns the variables the program runs with, and the ones
// it couldn't know.
func (c *container) processEnv(extra []string) ([]string, []string) {
	vars, unknownSources := c.env()
	set := map[string]bool{}
	for _, e := range extra {
		k, _, _ := strings.Cut(e, "=")
		set[k] = true
	}
	var env, unknown []string
	var names []string
	for k := range vars {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		v := vars[k]
		if set[k] {
			continue
		}
		if v.known {
			env = append(env, k+"="+v.value)
		} else {
			unknown = append(unknown, k+", which "+v.why)
		}
	}
	for _, s := range unknownSources {
		unknown = append(unknown, "the variables of envFrom "+s)
	}
	return append(env, extra...), unknown
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func unspecified(host string) bool {
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// judge turns what the program did into findings for one probe.
func (c *container) judge(p *probe, id int, out *runOut, wait time.Duration) []*finding {
	var hosts []string
	for _, l := range out.Listens {
		if l.Err == "" && l.Port == p.port && l.Network != "unix" {
			hosts = append(hosts, l.Host)
		}
	}
	httpOn := false
	for _, s := range out.Servers {
		if s.Port == p.port {
			hosts = append(hosts, s.Host)
			httpOn = true
		}
	}
	if len(hosts) == 0 {
		listened := describeServers(out)
		return []*finding{c.find(sevError, "no-listener", p, fmt.Sprintf("nothing listened on port %d within %s of starting; the program listened on %s. The kubelet's connection is refused and the probe fails", p.port, wait, listened), "point the probe at the port the server uses, or change the program's listen address. If the server starts later, after a dependency, raise --wait")}
	}
	reachable := false
	for _, h := range hosts {
		if !loopback(h) {
			reachable = true
		}
	}
	if !reachable {
		return []*finding{c.find(sevError, "loopback-only", p, fmt.Sprintf("port %d listens only on %s, inside the pod. The kubelet connects to the pod IP, so the connection is refused and the probe fails", p.port, net.JoinHostPort(hosts[0], strconv.Itoa(p.port))), fmt.Sprintf("listen on :%d (all addresses) instead of %s", p.port, hosts[0]))}
	}
	var fs []*finding
	for _, h := range hosts {
		if !loopback(h) && !unspecified(h) && h != podIP {
			fs = append(fs, c.find(sevWarn, "specific-address", p, fmt.Sprintf("port %d listens on %s only; the kubelet connects to the pod IP, so this works only if %s is the pod IP", p.port, h, h), "listen on all addresses, or on the pod IP from a fieldRef to status.podIP"))
			break
		}
	}
	if p.kind == "tcp" {
		return fs
	}
	if !httpOn {
		return append(fs, c.find(sevError, "not-http", p, fmt.Sprintf("port %d is a listener that no net/http server serves (gRPC or another protocol, or a server probegap can't see), so an HTTP GET gets no HTTP answer", p.port), "use a tcpSocket or grpc probe for this port, or probe the HTTP port"))
	}
	var r *resultOut
	for i := range out.Results {
		if out.Results[i].ID == id {
			r = &out.Results[i]
		}
	}
	if r == nil {
		return append(fs, c.find(sevError, "not-checked", p, "the hook didn't report a result for this probe; please report this as a bug", ""))
	}
	srv := out.Servers[r.Server]
	switch {
	case r.Err == "plain-http":
		return append(fs, c.find(sevError, "scheme-mismatch", p, fmt.Sprintf("the probe uses HTTPS, and the server on port %d serves plain HTTP, so the TLS handshake fails", p.port), "remove scheme: HTTPS"))
	case r.Err == "tls-server":
		return append(fs, c.find(sevError, "scheme-mismatch", p, fmt.Sprintf("the probe uses HTTP, and the server on port %d serves HTTPS; it answers a plain request with 400 Bad Request", p.port), "add scheme: HTTPS (the kubelet doesn't check the certificate)"))
	case r.Err != "":
		return append(fs, c.find(sevError, "probe-error", p, r.Err, ""))
	case r.Panic != "":
		return append(fs, c.find(sevWarn, "handler-panicked", p, fmt.Sprintf("the handler (%s) panicked with %q. The route exists; in this run the program had no database or other dependencies, which may be why. A panic closes the connection, so the probe fails", srv.Handler, clipLine(r.Panic)), "check that the handler can't panic in the cluster"))
	case r.TimedOut:
		return append(fs, c.find(sevWarn, "timeout", p, fmt.Sprintf("the handler didn't answer within timeoutSeconds (%ds). The route exists; in this run the program had no database or other dependencies, which may be why it waited", p.timeout), "check what the handler waits for, or raise timeoutSeconds"))
	}
	chain := ""
	if len(r.Hops) > 1 {
		var parts []string
		for _, h := range r.Hops {
			parts = append(parts, fmt.Sprintf("%s (%d)", h.Path, h.Status))
		}
		chain = " after redirects " + strings.Join(parts, " -> ")
	}
	body := ""
	if b := clipLine(r.Body); b != "" {
		body = fmt.Sprintf(", body %q", b)
	}
	got := fmt.Sprintf("GET %s on port %d returns %d%s%s", p.path, p.port, r.Status, chain, body)
	switch {
	case r.OffHost != "":
		return append(fs, c.find(sevNote, "redirect-off-host", p, fmt.Sprintf("GET %s redirects to %s, another host. The kubelet doesn't follow it and counts the redirect as a success, so this probe checks nothing", p.path, r.OffHost), "probe a path that answers directly"))
	case r.Status >= 200 && r.Status < 400:
		return fs
	case r.Status == 404:
		return append(fs, c.find(sevError, "not-found", p, got+". The kubelet counts any status of 400 or more as a failure"+c.restartNote(p), "probe a path the server routes, or register this one"))
	case r.Status == 405:
		return append(fs, c.find(sevError, "method-not-allowed", p, got+". The kubelet always sends GET"+c.restartNote(p), "accept GET on this route"))
	case r.Status == 401 || r.Status == 403:
		return append(fs, c.find(sevError, "needs-auth", p, got+". The kubelet sends no credentials, only the probe's httpHeaders"+c.restartNote(p), "serve this path before the auth middleware, or add the header the server needs with httpHeaders"))
	case r.Status < 500:
		return append(fs, c.find(sevError, "client-error", p, got+". The kubelet counts any status of 400 or more as a failure"+c.restartNote(p), ""))
	}
	return append(fs, c.find(sevWarn, "server-error", p, got+". The route exists; in this run the program had no database or other dependencies, which may be why it failed", "check that this handler returns 200 when the pod is healthy in the cluster"))
}

func (c *container) restartNote(p *probe) string {
	switch p.which {
	case "liveness":
		return ". As a liveness probe, it gets the container restarted over and over"
	case "startup":
		return ". As a startup probe, it keeps the container from ever finishing its start, and gets it restarted"
	case "readiness":
		return ". As a readiness probe, it keeps the pod out of its Services"
	}
	return ""
}

func clipLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + "..."
	}
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

func printText(w io.Writer, rep *report) {
	counts := map[int]int{}
	for _, f := range rep.Findings {
		counts[f.sev]++
		at := where{Kind: f.Kind, Namespace: f.Namespace, Name: f.Name, Container: f.Container}
		head := at.String()
		if f.Probe != "" {
			head += ", " + f.Probe
		}
		fmt.Fprintf(w, "%s %s: %s\n", f.Severity, f.Code, head)
		fmt.Fprintf(w, "  %s\n", f.Message)
		if f.Fix != "" {
			fmt.Fprintf(w, "  fix: %s\n", f.Fix)
		}
		if f.File != "" {
			fmt.Fprintf(w, "  in %s\n", f.File)
		}
		fmt.Fprintln(w)
	}
	scope := fmt.Sprintf("%d checked %s in %d %s", rep.Checked, plural(rep.Checked, "probe", "probes"), len(rep.Containers), plural(len(rep.Containers), "container", "containers"))
	if counts[sevError]+counts[sevWarn] == 0 {
		notes := ""
		if counts[sevNote] > 0 {
			notes = fmt.Sprintf(", and %d %s above", counts[sevNote], plural(counts[sevNote], "note", "notes"))
		}
		fmt.Fprintf(w, "No problems in %s%s.\n", scope, notes)
	} else {
		fmt.Fprintf(w, "%d %s, %d %s and %d %s in %s.\n",
			counts[sevError], plural(counts[sevError], "error", "errors"),
			counts[sevWarn], plural(counts[sevWarn], "warning", "warnings"),
			counts[sevNote], plural(counts[sevNote], "note", "notes"), scope)
	}
	for _, c := range rep.Containers {
		at := where{Kind: c.Kind, Namespace: c.Namespace, Name: c.Name, Container: c.Container}
		fmt.Fprintf(w, "%s listened on %s.\n", at, c.listened)
	}
}
