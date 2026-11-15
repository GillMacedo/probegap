package tests

import (
	"sort"
	"strings"
	"testing"
)

// TestChiRouter checks probes against a program that routes with
// go-chi/chi v5 (vendored in tests/testdata/chimod), with a sub-router,
// a group behind basic auth and chi's own 404 and 405 answers.
func TestChiRouter(t *testing.T) {
	good := deployment("chi", nil, `startupProbe:
  httpGet: {path: /internal/healthz, port: 3000}
livenessProbe:
  httpGet:
    path: /admin/status
    port: 3000
    httpHeaders: [{name: Authorization, value: Basic YWRtaW46c2VjcmV0}]
readinessProbe:
  httpGet: {path: /internal/readyz, port: 3000}`)
	f := writeFile(t, t.TempDir(), "good.yaml", good)
	rep, r := runJSON(t, nil, "--pkg", "./tests/testdata/chimod", f)
	if r.code != 0 || len(rep.Findings) != 0 || rep.Checked != 3 {
		t.Fatalf("healthy chi probes: exit %d %+v %s", r.code, rep.Findings, r.stderr)
	}

	bad := deployment("chi", nil, `startupProbe:
  httpGet: {path: /healthz, port: 3000}
livenessProbe:
  httpGet: {path: /admin/status, port: 3000}
readinessProbe:
  httpGet: {path: /internal/readyz, port: 8080}`)
	f = writeFile(t, t.TempDir(), "bad.yaml", bad)
	rep, r = runJSON(t, nil, "--pkg", "./tests/testdata/chimod", "--wait", "2s", f)
	if r.code != 1 {
		t.Fatalf("exit %d %s", r.code, r.stderr)
	}
	var codes []string
	for _, f := range rep.Findings {
		codes = append(codes, f.Code+" "+f.Probe)
	}
	sort.Strings(codes)
	want := "needs-auth liveness httpGet /admin/status port 3000\nno-listener readiness httpGet /internal/readyz port 8080\nnot-found startup httpGet /healthz port 3000"
	if strings.Join(codes, "\n") != want {
		t.Fatalf("got:\n%s", strings.Join(codes, "\n"))
	}
}
