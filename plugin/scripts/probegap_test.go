package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func goroot(t *testing.T) string {
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// The hooks must fit the standard library of the Go running the tests.
func TestPatchStdlib(t *testing.T) {
	root := goroot(t)
	srv, err := os.ReadFile(filepath.Join(root, "src", "net", "http", "server.go"))
	if err != nil {
		t.Fatal(err)
	}
	p := patchServer(string(srv))
	if p == "" || strings.Count(p, "probegapServe(") != 2 || !hasServerHandler(string(srv)) {
		t.Fatal("net/http/server.go: hook not added")
	}
	dial, err := os.ReadFile(filepath.Join(root, "src", "net", "dial.go"))
	if err != nil {
		t.Fatal(err)
	}
	if d := patchDial(string(dial)); d == "" || strings.Count(d, "probegapListen(") != 1 {
		t.Fatal("net/dial.go: hook not added")
	}
	if patchServer("package http\n") != "" || patchDial("package net\n") != "" {
		t.Fatal("patching a file without the functions must fail")
	}
}

func TestProbesParse(t *testing.T) {
	c := &container{at: where{Kind: "Pod", Name: "p", Container: "c"}, raw: map[string]any{
		"ports":          []any{map[string]any{"name": "http", "containerPort": 8080}},
		"livenessProbe":  map[string]any{"httpGet": map[string]any{"port": "http", "path": "healthz"}},
		"readinessProbe": map[string]any{"tcpSocket": map[string]any{"port": 9000}, "timeoutSeconds": 3},
		"startupProbe":   map[string]any{"grpc": map[string]any{"port": 9000}},
	}}
	ps, err := c.probes()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 3 {
		t.Fatalf("got %d probes", len(ps))
	}
	byWhich := map[string]*probe{}
	for _, p := range ps {
		byWhich[p.which] = p
	}
	if l := byWhich["liveness"]; l.port != 8080 || l.path != "/healthz" || l.scheme != "HTTP" || l.timeout != 1 {
		t.Fatalf("liveness: %+v", l)
	}
	if r := byWhich["readiness"]; r.kind != "tcp" || r.port != 9000 || r.timeout != 3 {
		t.Fatalf("readiness: %+v", r)
	}
	if s := byWhich["startup"]; s.skip == "" {
		t.Fatalf("grpc probe should be skipped: %+v", s)
	}
	// An init container that isn't a sidecar never runs probes.
	ic := &container{init: true, raw: map[string]any{"livenessProbe": map[string]any{"tcpSocket": map[string]any{"port": 1}}}}
	if ps, _ := ic.probes(); len(ps) != 0 {
		t.Fatal("init container probes counted")
	}
	ic.raw["restartPolicy"] = "Always"
	if ps, _ := ic.probes(); len(ps) != 1 {
		t.Fatal("sidecar probes not counted")
	}
}

func TestLoopback(t *testing.T) {
	for h, want := range map[string]bool{"127.0.0.1": true, "::1": true, "localhost": true, "127.1.2.3": true, "": false, "0.0.0.0": false, "::": false, "10.1.2.3": false} {
		if loopback(h) != want {
			t.Errorf("loopback(%q) = %v", h, !want)
		}
	}
}
