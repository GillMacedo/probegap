package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// binary is the program built with the net and net/http hooks.
type binary struct {
	path    string
	pkgPath string
	tmp     string
	wait    time.Duration
}

type listPkg struct {
	ImportPath string
	Name       string
	Dir        string
	GoFiles    []string
	Standard   bool
	DepOnly    bool
	Error      *struct{ Err string }
	DepsErrors []*struct{ Err string }
	Incomplete bool
}

// buildProgram builds the main package in dir with net and net/http
// patched, so the program records its listeners and servers instead of
// opening ports.
func buildProgram(dir, pattern string) (*binary, error) {
	cmd := exec.Command("go", "list", "-deps", "-json", "--", pattern)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, untrusted("go list %s in %s failed, so the program can't be built (%v):\n%s", pattern, dir, err, clip(errb.String()))
	}
	var pkgs []*listPkg
	dec := json.NewDecoder(&out)
	for {
		p := new(listPkg)
		if err := dec.Decode(p); err == io.EOF {
			break
		} else if err != nil {
			return nil, untrusted("reading go list output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
	var root *listPkg
	byPath := map[string]*listPkg{}
	for _, p := range pkgs {
		byPath[p.ImportPath] = p
		if p.Error != nil {
			return nil, untrusted("package %s: %s", p.ImportPath, strings.TrimSpace(p.Error.Err))
		}
		if len(p.DepsErrors) > 0 {
			return nil, untrusted("package %s: %s", p.ImportPath, strings.TrimSpace(p.DepsErrors[0].Err))
		}
		if p.Incomplete {
			return nil, untrusted("package %s is incomplete", p.ImportPath)
		}
		if !p.DepOnly {
			if root != nil {
				return nil, untrusted("%s matches more than one package; name one main package, such as ./cmd/server", pattern)
			}
			root = p
		}
	}
	if root == nil {
		return nil, untrusted("go list %s printed no packages", pattern)
	}
	if root.Name != "main" {
		return nil, untrusted("%s is package %s, not a main package; name the program's main package with --pkg", root.ImportPath, root.Name)
	}
	hp, np := byPath["net/http"], byPath["net"]
	if hp == nil || np == nil || !hp.Standard {
		return nil, untrusted("%s doesn't link net/http, so probegap can't see its servers; it only checks programs that serve HTTP with net/http or a router built on it", root.ImportPath)
	}
	b := &binary{pkgPath: root.ImportPath}
	tmp, err := os.MkdirTemp("", "probegap-")
	if err != nil {
		return nil, err
	}
	b.tmp = tmp
	overlay := map[string]string{}
	add := func(dir, file, hookName, hook string, patch func(string) string) error {
		f := filepath.Join(dir, file)
		src, err := os.ReadFile(f)
		if err != nil {
			return untrusted("reading %s: %v", f, err)
		}
		patched := patch(string(src))
		if patched == "" {
			return untrusted("%s doesn't have the function probegap hooks; this Go version isn't supported", f)
		}
		pf := filepath.Join(tmp, strings.ReplaceAll(filepath.Base(dir)+"_"+file, "/", "_"))
		hf := filepath.Join(tmp, filepath.Base(dir)+"_"+hookName)
		if err := os.WriteFile(pf, []byte(patched), 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(hf, []byte(hook), 0o600); err != nil {
			return err
		}
		overlay[f] = pf
		overlay[filepath.Join(dir, hookName)] = hf
		return nil
	}
	if src, err := os.ReadFile(filepath.Join(hp.Dir, "server.go")); err != nil || !hasServerHandler(string(src)) {
		b.cleanup()
		return nil, untrusted("net/http in %s has no serverHandler type; this Go version isn't supported", hp.Dir)
	}
	if err := add(np.Dir, "dial.go", "zz_probegap_net.go", netHook, patchDial); err != nil {
		b.cleanup()
		return nil, err
	}
	if err := add(hp.Dir, "server.go", "zz_probegap_http.go", httpHook, patchServer); err != nil {
		b.cleanup()
		return nil, err
	}
	ov, _ := json.Marshal(map[string]any{"Replace": overlay})
	ovFile := filepath.Join(tmp, "overlay.json")
	if err := os.WriteFile(ovFile, ov, 0o600); err != nil {
		b.cleanup()
		return nil, err
	}
	b.path = filepath.Join(tmp, "program")
	build := exec.Command("go", "build", "-overlay", ovFile, "-o", b.path, "--", pattern)
	build.Dir = dir
	var berr bytes.Buffer
	build.Stdout, build.Stderr = &berr, &berr
	if err := build.Run(); err != nil {
		b.cleanup()
		return nil, untrusted("go build %s failed (%v):\n%s", pattern, err, clip(berr.String()))
	}
	return b, nil
}

func (b *binary) cleanup() {
	if b.tmp != "" {
		os.RemoveAll(b.tmp)
	}
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 1500 {
		s = s[:1500] + "..."
	}
	return s
}

// planProbe is a probe as the hook reads it.
type planProbe struct {
	ID        int         `json:"id"`
	Kind      string      `json:"kind"` // http or tcp
	Port      int         `json:"port"`
	Scheme    string      `json:"scheme"`
	Path      string      `json:"path"`
	Headers   [][2]string `json:"headers"`
	TimeoutMS int         `json:"timeout_ms"`
	PodIP     string      `json:"pod_ip"`
	UserAgent string      `json:"user_agent"`
}

type listenOut struct {
	Network string
	Address string
	Host    string
	Port    int
	Err     string
}

type serverOut struct {
	Address string `json:"address"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	TLS     bool   `json:"tls"`
	Handler string `json:"handler"`
}

type hop struct {
	Path     string `json:"path"`
	Status   int    `json:"status"`
	Location string `json:"location,omitempty"`
}

type resultOut struct {
	ID       int    `json:"id"`
	Server   int    `json:"server"`
	Status   int    `json:"status"`
	Body     string `json:"body,omitempty"`
	Hops     []hop  `json:"hops,omitempty"`
	Panic    string `json:"panic,omitempty"`
	TimedOut bool   `json:"timed_out,omitempty"`
	OffHost  string `json:"off_host,omitempty"`
	Err      string `json:"err,omitempty"`
}

type runOut struct {
	Listens []listenOut `json:"listens"`
	Servers []serverOut `json:"servers"`
	Results []resultOut `json:"results"`
	Waited  bool        `json:"waited"`
}

// run starts the program with args and env in an empty directory and
// reads what the hook wrote.
func (b *binary) run(args []string, env []string, probes []planProbe) (*runOut, string, error) {
	work, err := os.MkdirTemp(b.tmp, "run-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(work)
	planFile := filepath.Join(work, ".probegap-plan.json")
	outFile := filepath.Join(work, ".probegap-out.json")
	plan, _ := json.Marshal(map[string]any{"wait_ms": b.wait.Milliseconds(), "probes": probes})
	if err := os.WriteFile(planFile, plan, 0o600); err != nil {
		return nil, "", err
	}
	limit := b.wait + 10*time.Second
	for _, p := range probes {
		limit += time.Duration(p.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, b.path, args...)
	cmd.Dir = work
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + work, "TMPDIR=" + work}, env...)
	cmd.Env = append(cmd.Env, "PROBEGAP_PLAN="+planFile, "PROBEGAP_OUT="+outFile)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.Stdin = nil
	runErr := cmd.Run()
	data, rerr := os.ReadFile(outFile)
	if rerr != nil || runErr != nil {
		why := fmt.Sprintf("exited (%v)", runErr)
		if runErr == nil {
			why = "exited with status 0"
		}
		if ctx.Err() != nil {
			why = fmt.Sprintf("was still running after %s and never reported", limit)
		}
		return nil, out.String(), fmt.Errorf("%s %s before probegap could check its servers", b.pkgPath, why)
	}
	var r runOut
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, out.String(), fmt.Errorf("unreadable hook output: %v", err)
	}
	return &r, out.String(), nil
}

// describeServers lists what the program served, for the summary line.
func describeServers(r *runOut) string {
	var parts []string
	seen := map[string]bool{}
	for _, s := range r.Servers {
		kind := "HTTP"
		if s.TLS {
			kind = "HTTPS"
		}
		p := fmt.Sprintf("%s (%s)", s.Address, kind)
		if !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}
	served := map[int]bool{}
	for _, s := range r.Servers {
		served[s.Port] = true
	}
	for _, l := range r.Listens {
		if l.Err != "" || served[l.Port] || l.Network == "unix" {
			continue
		}
		p := fmt.Sprintf("%s (not net/http)", l.Address)
		if !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}
