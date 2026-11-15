package tests

import (
	"strings"
	"testing"
)

// Each case points one probe at a route of tests/testdata/cases and
// checks the finding probegap reports, or that it stays quiet.
var probeCases = []struct {
	name  string
	args  []string
	probe string
	code  string // "" means no finding
	sev   string
}{
	{"ok", nil, "livenessProbe:\n  httpGet: {path: /ok, port: http}", "", ""},
	{"default handler 200", nil, "livenessProbe:\n  httpGet: {path: /any, port: 8080}", "", ""},
	{"path without slash", nil, "livenessProbe:\n  httpGet: {path: ok, port: 8080}", "", ""},
	{"query", nil, "readinessProbe:\n  httpGet: {path: '/query?full=1', port: 8080}", "", ""},
	{"same-host redirect", nil, "readinessProbe:\n  httpGet: {path: /old, port: 8080}", "", ""},
	{"header given", nil, "readinessProbe:\n  httpGet:\n    path: /needs-header\n    port: 8080\n    httpHeaders: [{name: X-Probe, value: 'yes'}]", "", ""},
	{"kubelet user agent", nil, "readinessProbe:\n  httpGet: {path: /kubelet-only, port: 8080}", "", ""},
	{"base context", nil, "readinessProbe:\n  httpGet: {path: /base-context, port: 8080}", "", ""},
	{"https", nil, "readinessProbe:\n  httpGet: {path: /ok, port: https, scheme: HTTPS}", "", ""},
	{"tcp", nil, "readinessProbe:\n  tcpSocket: {port: raw}", "", ""},
	{"tcp on http", nil, "readinessProbe:\n  tcpSocket: {port: 8080}", "", ""},
	{"vhost with host header", nil, "readinessProbe:\n  httpGet:\n    path: /vhost\n    port: 8080\n    httpHeaders: [{name: Host, value: internal.example.com}]", "", ""},
	{"pod ip from fieldRef", []string{"-host-addr=:6000"}, "readinessProbe:\n  tcpSocket: {port: 6000}", "", ""},

	{"404", nil, "livenessProbe:\n  httpGet: {path: /healthz, port: http}", "not-found", "ERROR"},
	{"vhost without host header", nil, "livenessProbe:\n  httpGet: {path: /vhost, port: 8080}", "not-found", "ERROR"},
	{"redirect to 404", nil, "livenessProbe:\n  httpGet: {path: /old-missing, port: 8080}", "not-found", "ERROR"},
	{"405", nil, "livenessProbe:\n  httpGet: {path: /post-only, port: 8080}", "method-not-allowed", "ERROR"},
	{"403", nil, "livenessProbe:\n  httpGet: {path: /forbidden, port: 8080}", "needs-auth", "ERROR"},
	{"missing header", nil, "livenessProbe:\n  httpGet: {path: /needs-header, port: 8080}", "needs-auth", "ERROR"},
	{"400", nil, "livenessProbe:\n  httpGet: {path: /bad, port: 8080}", "client-error", "ERROR"},
	{"query missing", nil, "readinessProbe:\n  httpGet: {path: /query, port: 8080}", "client-error", "ERROR"},
	{"redirect loop", nil, "readinessProbe:\n  httpGet: {path: /loop, port: 8080}", "probe-error", "ERROR"},
	{"503", nil, "readinessProbe:\n  httpGet: {path: /unavailable, port: 8080}", "server-error", "WARN"},
	{"slow", nil, "readinessProbe:\n  httpGet: {path: /slow, port: 8080}\n  timeoutSeconds: 1", "timeout", "WARN"},
	{"panic", nil, "readinessProbe:\n  httpGet: {path: /panics, port: 8080}", "handler-panicked", "WARN"},
	{"off-host redirect", nil, "readinessProbe:\n  httpGet: {path: /elsewhere, port: 8080}", "redirect-off-host", "note"},
	{"no listener", nil, "livenessProbe:\n  httpGet: {path: /ok, port: 8081}", "no-listener", "ERROR"},
	{"tcp no listener", nil, "livenessProbe:\n  tcpSocket: {port: 5432}", "no-listener", "ERROR"},
	{"loopback v4", nil, "livenessProbe:\n  httpGet: {path: /ok, port: 9090}", "loopback-only", "ERROR"},
	{"loopback v6", nil, "livenessProbe:\n  tcpSocket: {port: 9091}", "loopback-only", "ERROR"},
	{"not http", nil, "livenessProbe:\n  httpGet: {path: /ok, port: raw}", "not-http", "ERROR"},
	{"https probe on http", nil, "livenessProbe:\n  httpGet: {path: /ok, port: 8080, scheme: HTTPS}", "scheme-mismatch", "ERROR"},
	{"http probe on https", nil, "livenessProbe:\n  httpGet: {path: /ok, port: 8443}", "scheme-mismatch", "ERROR"},
	{"named port missing", nil, "livenessProbe:\n  httpGet: {path: /ok, port: metrics}\nreadinessProbe:\n  httpGet: {path: /ok, port: 8080}", "named-port-missing", "ERROR"},
	{"port as digit string", nil, "livenessProbe:\n  httpGet: {path: /ok, port: '8080'}\nreadinessProbe:\n  httpGet: {path: /ok, port: 8080}", "invalid-probe", "ERROR"},
	{"bad scheme", nil, "livenessProbe:\n  httpGet: {path: /ok, port: 8080, scheme: http}\nreadinessProbe:\n  httpGet: {path: /ok, port: 8080}", "invalid-probe", "ERROR"},
	{"specific address", []string{"-host-addr=:6000"}, "readinessProbe:\n  tcpSocket: {port: 60001}", "specific-address", "WARN"},
	{"flag moves the server", []string{"-addr=:8000"}, "livenessProbe:\n  httpGet: {path: /ok, port: 8080}", "no-listener", "ERROR"},
	{"grpc not checked", nil, "livenessProbe:\n  grpc: {port: 9000}\nreadinessProbe:\n  httpGet: {path: /ok, port: 8080}", "not-checked", "note"},
	{"exec not checked", nil, "livenessProbe:\n  exec: {command: [cat, /tmp/ok]}\nreadinessProbe:\n  httpGet: {path: /ok, port: 8080}", "not-checked", "note"},
	{"host not checked", nil, "livenessProbe:\n  httpGet: {path: /ok, port: 8080, host: 10.9.9.9}\nreadinessProbe:\n  httpGet: {path: /ok, port: 8080}", "not-checked", "note"},
}

func TestProbeCases(t *testing.T) {
	for _, tc := range probeCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			probes := tc.probe
			// POD_IP for -host-addr comes from the pod, as in a real manifest.
			yaml := deployment("app", tc.args, probes)
			yaml = strings.Replace(yaml, "          ports:\n", "          env:\n            - name: POD_IP\n              valueFrom: {fieldRef: {fieldPath: status.podIP}}\n          ports:\n", 1)
			f := writeFile(t, t.TempDir(), "app.yaml", yaml)
			rep, r := runJSON(t, nil, "--pkg", "./tests/testdata/cases", "--wait", "3s", f)
			if r.code == 2 {
				t.Fatalf("refused: %s", r.stderr)
			}
			var got []jsonFinding
			for _, f := range rep.Findings {
				got = append(got, f)
			}
			if tc.code == "" {
				if len(got) != 0 || r.code != 0 {
					t.Fatalf("want no findings and exit 0, got exit %d: %+v", r.code, got)
				}
				return
			}
			found := false
			for _, f := range got {
				if f.Code == tc.code && f.Severity == tc.sev {
					found = true
				}
			}
			if !found {
				t.Fatalf("want %s %s, got exit %d: %+v", tc.sev, tc.code, r.code, got)
			}
			wantExit := 0
			if tc.sev == "ERROR" {
				wantExit = 1
			}
			if r.code != wantExit {
				t.Fatalf("exit %d, want %d", r.code, wantExit)
			}
			// --fail-on warn fails on warnings, never always exits 0.
			if tc.sev == "WARN" {
				if r := runTool(t, nil, "--pkg", "./tests/testdata/cases", "--wait", "3s", "--fail-on", "warn", f); r.code != 1 {
					t.Fatalf("--fail-on warn: exit %d", r.code)
				}
			}
			if r := runTool(t, nil, "--pkg", "./tests/testdata/cases", "--wait", "3s", "--fail-on", "never", f); r.code != 0 {
				t.Fatalf("--fail-on never: exit %d", r.code)
			}
		})
	}
}

// A server that starts after --wait is reported, never passed.
func TestLateServer(t *testing.T) {
	f := writeFile(t, t.TempDir(), "app.yaml", deployment("app", []string{"-late=3s"}, "livenessProbe:\n  httpGet: {path: /ok, port: 8080}"))
	rep, r := runJSON(t, nil, "--pkg", "./tests/testdata/cases", "--wait", "1s", f)
	if r.code != 1 || len(rep.Findings) == 0 || rep.Findings[0].Code != "no-listener" {
		t.Fatalf("exit %d: %+v %s", r.code, rep.Findings, r.stderr)
	}
	rep, r = runJSON(t, nil, "--pkg", "./tests/testdata/cases", "--wait", "6s", f)
	if r.code != 0 || len(rep.Findings) != 0 {
		t.Fatalf("with a longer wait: exit %d: %+v %s", r.code, rep.Findings, r.stderr)
	}
}
