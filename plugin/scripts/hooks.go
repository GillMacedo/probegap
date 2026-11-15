package main

import (
	"regexp"
	"strings"
)

// The two hooks below are added to the standard library with go build
// -overlay. They are only active when PROBEGAP_PLAN is set, which only
// probegap does.
//
// netHook makes (*net.ListenConfig).Listen, and so net.Listen,
// http.ListenAndServe, tls.Listen and most servers, return a listener that
// records its address and never accepts a connection. No port is opened.
const netHook = `package net

import (
	"context"
	"os"
	"sync"
)

// ProbegapListen is one call to Listen the program made.
type ProbegapListen struct {
	Network string
	Address string
	Host    string
	Port    int
	Err     string
}

var (
	probegapMu      sync.Mutex
	probegapListens []ProbegapListen
	probegapHosts   = map[Listener]string{}

	// ProbegapNotify is set by net/http and called after each Listen.
	ProbegapNotify func()
)

// ProbegapListens returns the listens so far.
func ProbegapListens() []ProbegapListen {
	probegapMu.Lock()
	defer probegapMu.Unlock()
	return append([]ProbegapListen(nil), probegapListens...)
}

// ProbegapHost returns the host a fake listener was asked to bind.
func ProbegapHost(l Listener) (string, bool) {
	probegapMu.Lock()
	defer probegapMu.Unlock()
	h, ok := probegapHosts[l]
	return h, ok
}

type probegapListener struct {
	addr Addr
	done chan struct{}
	once sync.Once
}

func (l *probegapListener) Accept() (Conn, error) {
	<-l.done
	return nil, ErrClosed
}

func (l *probegapListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *probegapListener) Addr() Addr { return l.addr }

func probegapListen(ctx context.Context, network, address string) (Listener, error, bool) {
	if os.Getenv("PROBEGAP_PLAN") == "" {
		return nil, nil, false
	}
	rec := ProbegapListen{Network: network, Address: address}
	var l *probegapListener
	var err error
	switch network {
	case "tcp", "tcp4", "tcp6":
		host, port, serr := SplitHostPort(address)
		if serr != nil {
			err = &OpError{Op: "listen", Net: network, Err: serr}
			break
		}
		p, perr := LookupPort(network, port)
		if perr != nil {
			err = &OpError{Op: "listen", Net: network, Err: perr}
			break
		}
		rec.Host, rec.Port = host, p
		ip := ParseIP(host)
		if host == "localhost" {
			ip = IPv4(127, 0, 0, 1)
		}
		l = &probegapListener{addr: &TCPAddr{IP: ip, Port: p}, done: make(chan struct{})}
	case "unix", "unixpacket":
		rec.Host = address
		l = &probegapListener{addr: &UnixAddr{Name: address, Net: network}, done: make(chan struct{})}
	default:
		err = UnknownNetworkError(network)
	}
	if err != nil {
		rec.Err = err.Error()
	}
	probegapMu.Lock()
	probegapListens = append(probegapListens, rec)
	if l != nil {
		probegapHosts[l] = rec.Host
	}
	notify := ProbegapNotify
	probegapMu.Unlock()
	if notify != nil {
		notify()
	}
	if err != nil {
		return nil, err, true
	}
	return l, nil, true
}
`

// httpHook records each (*http.Server).Serve and ServeTLS call, and runs
// the probes in the plan against the recorded handlers.
const httpHook = `package http

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type probegapPlan struct {
	WaitMS int             ` + "`json:\"wait_ms\"`" + `
	Probes []probegapProbe ` + "`json:\"probes\"`" + `
}

type probegapProbe struct {
	ID        int               ` + "`json:\"id\"`" + `
	Kind      string            ` + "`json:\"kind\"`" + `
	Port      int               ` + "`json:\"port\"`" + `
	Scheme    string            ` + "`json:\"scheme\"`" + `
	Path      string            ` + "`json:\"path\"`" + `
	Headers   [][2]string       ` + "`json:\"headers\"`" + `
	TimeoutMS int               ` + "`json:\"timeout_ms\"`" + `
	PodIP     string            ` + "`json:\"pod_ip\"`" + `
	UserAgent string            ` + "`json:\"user_agent\"`" + `
}

type probegapServer struct {
	srv  *Server
	l    net.Listener
	tls  bool
	host string
	port int
}

type probegapServerOut struct {
	Address string ` + "`json:\"address\"`" + `
	Host    string ` + "`json:\"host\"`" + `
	Port    int    ` + "`json:\"port\"`" + `
	TLS     bool   ` + "`json:\"tls\"`" + `
	Handler string ` + "`json:\"handler\"`" + `
}

type probegapHop struct {
	Path     string ` + "`json:\"path\"`" + `
	Status   int    ` + "`json:\"status\"`" + `
	Location string ` + "`json:\"location,omitempty\"`" + `
}

type probegapResult struct {
	ID       int           ` + "`json:\"id\"`" + `
	Server   int           ` + "`json:\"server\"`" + `
	Status   int           ` + "`json:\"status\"`" + `
	Body     string        ` + "`json:\"body,omitempty\"`" + `
	Hops     []probegapHop ` + "`json:\"hops,omitempty\"`" + `
	Panic    string        ` + "`json:\"panic,omitempty\"`" + `
	TimedOut bool          ` + "`json:\"timed_out,omitempty\"`" + `
	OffHost  string        ` + "`json:\"off_host,omitempty\"`" + `
	Err      string        ` + "`json:\"err,omitempty\"`" + `
}

type probegapOut struct {
	Listens []net.ProbegapListen ` + "`json:\"listens\"`" + `
	Servers []probegapServerOut  ` + "`json:\"servers\"`" + `
	Results []probegapResult     ` + "`json:\"results\"`" + `
	Waited  bool                 ` + "`json:\"waited\"`" + `
}

var (
	probegapMu      sync.Mutex
	probegapServers []*probegapServer
	probegapKickCh  = make(chan struct{}, 1)
)

func init() {
	if os.Getenv("PROBEGAP_PLAN") == "" {
		return
	}
	net.ProbegapNotify = probegapKick
	go probegapMain()
}

func probegapKick() {
	select {
	case probegapKickCh <- struct{}{}:
	default:
	}
}

// probegapServe records a server. It returns true when probegap is active,
// and the caller then blocks instead of serving.
func probegapServe(srv *Server, l net.Listener, isTLS bool) bool {
	if os.Getenv("PROBEGAP_PLAN") == "" {
		return false
	}
	s := &probegapServer{srv: srv, l: l, tls: isTLS}
	if !isTLS && strings.Contains(fmt.Sprintf("%T", l), "tls.") {
		s.tls = true
	}
	if a, ok := l.Addr().(*net.TCPAddr); ok {
		s.port = a.Port
		if a.IP != nil {
			s.host = a.IP.String()
		}
	}
	if h, ok := net.ProbegapHost(l); ok {
		s.host = h
	}
	probegapMu.Lock()
	probegapServers = append(probegapServers, s)
	probegapMu.Unlock()
	probegapKick()
	return true
}

func probegapFail(msg string) {
	fmt.Fprintln(os.Stderr, "probegap hook: "+msg)
	os.Exit(97)
}

func probegapMain() {
	data, err := os.ReadFile(os.Getenv("PROBEGAP_PLAN"))
	if err != nil {
		probegapFail(err.Error())
	}
	var plan probegapPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		probegapFail(err.Error())
	}
	deadline := time.NewTimer(time.Duration(plan.WaitMS) * time.Millisecond)
	waited := false
	// Done when every probe's port has what it needs. When every port has
	// at least a listener, give the program a second more to start
	// serving on it, then go on.
	var quiet <-chan time.Time
	for !waited {
		strict, loose := probegapCovered(plan)
		if strict {
			break
		}
		if loose {
			quiet = time.After(time.Second)
		}
		select {
		case <-probegapKickCh:
		case <-quiet:
			waited = true
		case <-deadline.C:
			waited = true
		}
	}
	probegapMu.Lock()
	servers := append([]*probegapServer(nil), probegapServers...)
	probegapMu.Unlock()
	out := probegapOut{Listens: net.ProbegapListens(), Waited: waited, Results: []probegapResult{}, Servers: []probegapServerOut{}}
	for _, s := range servers {
		h := s.srv.Handler
		name := fmt.Sprintf("%T", h)
		if h == nil {
			name = "http.DefaultServeMux"
		}
		out.Servers = append(out.Servers, probegapServerOut{Address: s.l.Addr().String(), Host: s.host, Port: s.port, TLS: s.tls, Handler: name})
	}
	for _, p := range plan.Probes {
		if p.Kind != "http" {
			continue
		}
		idx := -1
		for i, s := range servers {
			if s.port != p.Port {
				continue
			}
			if idx < 0 || probegapLoopback(servers[idx].host) && !probegapLoopback(s.host) {
				idx = i
			}
		}
		if idx < 0 {
			continue
		}
		r := probegapRun(servers[idx], p)
		r.ID, r.Server = p.ID, idx
		out.Results = append(out.Results, r)
	}
	enc, err := json.Marshal(out)
	if err != nil {
		probegapFail(err.Error())
	}
	if err := os.WriteFile(os.Getenv("PROBEGAP_OUT"), enc, 0o600); err != nil {
		probegapFail(err.Error())
	}
	os.Exit(0)
}

func probegapLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func probegapCovered(plan probegapPlan) (bool, bool) {
	listens := net.ProbegapListens()
	probegapMu.Lock()
	defer probegapMu.Unlock()
	strict, loose := true, true
	for _, p := range plan.Probes {
		listening, serving := false, false
		for _, l := range listens {
			if l.Port == p.Port && l.Err == "" {
				listening = true
			}
		}
		for _, s := range probegapServers {
			if s.port == p.Port {
				listening, serving = true, true
			}
		}
		if !listening {
			strict, loose = false, false
		}
		if p.Kind == "http" && !serving {
			strict = false
		}
	}
	return strict, loose
}

type probegapRecorder struct {
	mu     sync.Mutex
	header Header
	code   int
	body   []byte
}

func (w *probegapRecorder) Header() Header { return w.header }

func (w *probegapRecorder) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.code == 0 && code >= 200 {
		w.code = code
	}
}

func (w *probegapRecorder) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.code == 0 {
		w.code = 200
	}
	if len(w.body) < 2048 {
		w.body = append(w.body, b...)
	}
	return len(b), nil
}

func (w *probegapRecorder) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.code == 0 {
		w.code = 200
	}
}

// probegapRun sends the request the kubelet sends, following redirects on
// the same host the way its HTTP client does.
func probegapRun(s *probegapServer, p probegapProbe) probegapResult {
	var res probegapResult
	if p.Scheme == "HTTPS" && !s.tls {
		res.Err = "plain-http"
		return res
	}
	if p.Scheme != "HTTPS" && s.tls {
		res.Err = "tls-server"
		return res
	}
	hostport := net.JoinHostPort(p.PodIP, strconv.Itoa(p.Port))
	host := hostport
	for _, h := range p.Headers {
		if strings.EqualFold(h[0], "Host") {
			host = h[1]
		}
	}
	target := p.Path
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.TimeoutMS)*time.Millisecond)
	defer cancel()
	for hop := 0; hop < 11; hop++ {
		var b strings.Builder
		b.WriteString("GET " + target + " HTTP/1.1\r\n")
		b.WriteString("Host: " + host + "\r\n")
		seen := map[string]bool{}
		for _, h := range p.Headers {
			if strings.EqualFold(h[0], "Host") {
				continue
			}
			seen[CanonicalHeaderKey(h[0])] = true
			if strings.EqualFold(h[0], "Accept") && h[1] == "" {
				continue // the kubelet removes an empty Accept header
			}
			b.WriteString(h[0] + ": " + h[1] + "\r\n")
		}
		if !seen["User-Agent"] {
			b.WriteString("User-Agent: " + p.UserAgent + "\r\n")
		}
		if !seen["Accept"] {
			b.WriteString("Accept: */*\r\n")
		}
		b.WriteString("Connection: close\r\n\r\n")
		req, err := ReadRequest(bufio.NewReader(strings.NewReader(b.String())))
		if err != nil {
			res.Err = "the kubelet's request for this probe is not valid HTTP: " + err.Error()
			return res
		}
		req.RemoteAddr = "10.0.0.2:41234"
		if s.tls {
			req.TLS = &tls.ConnectionState{HandshakeComplete: true, Version: tls.VersionTLS13}
		}
		rctx := ctx
		if s.srv.BaseContext != nil {
			rctx = probegapMerge(ctx, s.srv.BaseContext(s.l))
		}
		rctx = context.WithValue(rctx, ServerContextKey, s.srv)
		rctx = context.WithValue(rctx, LocalAddrContextKey, s.l.Addr())
		req = req.WithContext(rctx)
		w := &probegapRecorder{header: Header{}}
		done := make(chan string, 1)
		go func() {
			defer func() {
				if v := recover(); v != nil {
					if v == ErrAbortHandler {
						done <- "http.ErrAbortHandler"
						return
					}
					done <- fmt.Sprint(v)
					return
				}
				done <- ""
			}()
			serverHandler{s.srv}.ServeHTTP(w, req)
		}()
		select {
		case pv := <-done:
			if pv != "" {
				res.Panic = pv
				return res
			}
		case <-ctx.Done():
			res.TimedOut = true
			return res
		}
		w.mu.Lock()
		code, body := w.code, string(w.body)
		w.mu.Unlock()
		if code == 0 {
			code = 200
		}
		if len(body) > 300 {
			body = body[:300]
		}
		loc := w.header.Get("Location")
		res.Hops = append(res.Hops, probegapHop{Path: target, Status: code, Location: loc})
		res.Status, res.Body = code, body
		if code < 300 || code >= 400 || loc == "" {
			return res
		}
		base, _ := url.Parse("http://" + host + target)
		next, err := base.Parse(loc)
		if err != nil || base == nil {
			return res
		}
		if next.Host != host {
			res.OffHost = next.String()
			return res
		}
		target = next.RequestURI()
	}
	res.Err = "stopped after 10 redirects"
	return res
}

type probegapCtx struct {
	context.Context
	values context.Context
}

func (c probegapCtx) Value(k any) any { return c.values.Value(k) }

func probegapMerge(deadline, values context.Context) context.Context {
	if values == nil {
		return deadline
	}
	return probegapCtx{Context: deadline, values: values}
}
`

var (
	serveSig    = regexp.MustCompile(`(?m)^func \((\w+) \*Server\) Serve\(l net\.Listener\) error \{$`)
	serveTLSSig = regexp.MustCompile(`(?m)^func \((\w+) \*Server\) ServeTLS\(l net\.Listener, certFile, keyFile string\) error \{$`)
	listenSig   = regexp.MustCompile(`(?m)^func \(lc \*ListenConfig\) Listen\(ctx context\.Context, network, address string\) \(Listener, error\) \{$`)
)

// patchServer puts the hook at the top of Serve and ServeTLS in
// net/http's server.go. It returns "" when either is missing.
func patchServer(src string) string {
	for _, m := range []struct {
		re  *regexp.Regexp
		tls string
	}{{serveSig, "false"}, {serveTLSSig, "true"}} {
		if len(m.re.FindAllString(src, -1)) != 1 {
			return ""
		}
		src = m.re.ReplaceAllString(src, "$0\n\tif probegapServe($1, l, "+m.tls+") {\n\t\tselect {}\n\t}")
	}
	return src
}

// patchDial puts the hook at the top of (*ListenConfig).Listen in net's
// dial.go.
func patchDial(src string) string {
	if len(listenSig.FindAllString(src, -1)) != 1 {
		return ""
	}
	return listenSig.ReplaceAllString(src, "$0\n\tif l, err, ok := probegapListen(ctx, network, address); ok {\n\t\treturn l, err\n\t}")
}

func hasServerHandler(src string) bool {
	return strings.Contains(src, "type serverHandler struct")
}
