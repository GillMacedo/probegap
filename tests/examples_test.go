package tests

import (
	"sort"
	"strings"
	"testing"
)

func TestExamples(t *testing.T) {
	rep, r := runJSON(t, nil, "--pkg", "./examples/shop", "examples/shop/broken.yaml")
	if r.code != 1 {
		t.Fatalf("broken: exit %d %s", r.code, r.stderr)
	}
	var codes []string
	for _, f := range rep.Findings {
		codes = append(codes, f.Severity+" "+f.Code+" "+f.Probe)
	}
	sort.Strings(codes)
	want := []string{
		"ERROR loopback-only readiness httpGet /livez port admin",
		"ERROR needs-auth startup httpGet /api/ping port http",
		"ERROR not-found liveness httpGet /health port http",
		"note unknown-value ",
	}
	if strings.Join(codes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("broken findings:\n%s", strings.Join(codes, "\n"))
	}
	if rep.Checked != 3 || rep.Program != "github.com/GillMacedo/probegap/examples/shop" {
		t.Fatalf("checked %d, program %s", rep.Checked, rep.Program)
	}

	// The fixed manifest is healthy and must stay quiet.
	for _, extra := range [][]string{nil, {"--fail-on", "warn"}} {
		r := runTool(t, nil, append(extra, "--pkg", "./examples/shop", "examples/shop/fixed.yaml")...)
		if r.code != 0 || !strings.Contains(r.stdout, "No problems in 3 checked probes in 1 container") || strings.Contains(r.stdout, "ERROR") || strings.Contains(r.stdout, "WARN") {
			t.Fatalf("fixed %v: exit %d\n%s%s", extra, r.code, r.stdout, r.stderr)
		}
	}
}
