package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Bad manifests, as real tools print them. Each is passed alone, next to a
// real file in either order, inside a directory with a real file, and on
// stdin, always with a real program.
var badManifests = map[string]string{
	"empty":               "",
	"whitespace":          " \n\t\n",
	"comments only":       "# Source: app/templates/deployment.yaml\n---\n",
	"random text":         "lorem ipsum dolor sit amet\n",
	"binary":              "\x00\x01\x02\x03garbage\xff\xfe\n",
	"helm template fail":  "Error: execution error at (app/templates/deployment.yaml:31:20): image.tag is required\n",
	"helm dry-run":        "NAME: shop\nLAST DEPLOYED: Mon Nov  9 10:00:00 2026\nNAMESPACE: default\nSTATUS: pending-install\nREVISION: 1\n",
	"kustomize fail":      "Error: accumulating resources: accumulation err='accumulating resources from 'base': lstat base: no such file or directory'\n",
	"kubectl fail":        "error: the server doesn't have a resource type \"deploy\"\n",
	"go build fail":       "# example.com/shop/cmd/api\ncmd/api/main.go:12:2: undefined: serve\n",
	"go test fail":        "--- FAIL: TestHealthz (0.00s)\n    main_test.go:20: got 404\nFAIL\nexit status 1\nFAIL\texample.com/shop\t0.012s\n",
	"values file":         "replicaCount: 2\nlivenessProbe:\n  httpGet:\n    path: /healthz\n",
	"api error":           "apiVersion: v1\nkind: Status\nmetadata: {}\nstatus: Failure\nmessage: deployments.apps is forbidden\nreason: Forbidden\ncode: 403\n",
	"cut json":            "{\"apiVersion\": \"apps/v1\", \"kind\": \"Deployment\", \"metadata\": {\"name\": \"api\"}, \"spec\": {\"template\":\n",
	"unrendered":          "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: {{ include \"app.fullname\" . }}\n",
	"no final newline":    "apiVersion: v1\nkind: Service\nmetadata:\n  name: api",
	"cut in probe":        "apiVersion: v1\nkind: Pod\nmetadata:\n  name: api\nspec:\n  containers:\n    - name: api\n      image: api:1\n      livenessProbe:\n        httpGet:\n          path: /healthz\n",
	"cut after probe key": "apiVersion: v1\nkind: Pod\nmetadata:\n  name: api\nspec:\n  containers:\n    - name: api\n      image: api:1\n      livenessProbe:\n",
	"probe no handler":    "apiVersion: v1\nkind: Pod\nmetadata:\n  name: api\nspec:\n  containers:\n    - name: api\n      image: api:1\n      livenessProbe:\n        periodSeconds: 10\n---\n",
	"probe no port":       "apiVersion: v1\nkind: Pod\nmetadata:\n  name: api\nspec:\n  containers:\n    - name: api\n      image: api:1\n      livenessProbe:\n        httpGet: {path: /healthz}\n---\n",
	"probe not object":    "apiVersion: v1\nkind: Pod\nmetadata:\n  name: api\nspec:\n  containers:\n    - name: api\n      image: api:1\n      livenessProbe: /healthz\n---\n",
	"bad timeout":         "apiVersion: v1\nkind: Pod\nmetadata:\n  name: api\nspec:\n  containers:\n    - name: api\n      image: api:1\n      livenessProbe:\n        httpGet: {path: /healthz, port: 8080}\n        timeoutSeconds: 1s\n---\n",
	"no containers":       "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\nspec:\n  template:\n    spec:\n      containers: []\n---\n",
}

func expectRefused(t *testing.T, label string, stdin []byte, args ...string) {
	t.Helper()
	for _, extra := range [][]string{nil, {"--fail-on", "warn"}, {"--fail-on", "never"}, {"--format", "json"}} {
		r := runTool(t, stdin, append(append([]string{}, extra...), args...)...)
		if r.code != 2 || r.stdout != "" || !strings.Contains(r.stderr, "can't trust the input") {
			t.Errorf("%s %v: exit %d\nstdout: %s\nstderr: %s", label, extra, r.code, r.stdout, r.stderr)
		}
		if strings.Contains(r.stdout+r.stderr, "No problems") {
			t.Errorf("%s %v: printed a clean result", label, extra)
		}
	}
}

func TestBadManifests(t *testing.T) {
	real := "examples/shop/fixed.yaml"
	prog := []string{"--pkg", "./examples/shop", "--container", "api"}
	for name, content := range badManifests {
		name, content := name, content
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			bad := writeFile(t, dir, "bad.yaml", content)
			expectRefused(t, "alone", nil, append(prog, bad)...)
			expectRefused(t, "after real", nil, append(prog, real, bad)...)
			expectRefused(t, "before real", nil, append(prog, bad, real)...)
			expectRefused(t, "stdin", []byte(content), append(prog, "-")...)
			sub := filepath.Join(dir, "dir")
			os.Mkdir(sub, 0o755)
			writeFile(t, sub, "a-bad.yaml", content)
			data, _ := os.ReadFile("../" + real)
			writeFile(t, sub, "b-real.yaml", string(data))
			expectRefused(t, "dir", nil, append(prog, sub)...)
		})
	}
}

// Manifests that are valid but give nothing to check.
func TestNothingToCheck(t *testing.T) {
	noProbes := "apiVersion: v1\nkind: Pod\nmetadata:\n  name: api\nspec:\n  containers:\n    - name: api\n      image: api:1\n---\n"
	execOnly := "apiVersion: v1\nkind: Pod\nmetadata:\n  name: api\nspec:\n  containers:\n    - name: api\n      image: api:1\n      livenessProbe:\n        exec: {command: [cat, /tmp/ok]}\n---\n"
	service := "apiVersion: v1\nkind: Service\nmetadata:\n  name: api\nspec:\n  ports: [{port: 80}]\n"
	for name, content := range map[string]string{"no probes": noProbes, "exec only": execOnly, "service only": service} {
		f := writeFile(t, t.TempDir(), "m.yaml", content)
		expectRefused(t, name, nil, "--pkg", "./examples/shop", f)
	}
	// A chart or kustomization directory must be rendered first.
	for _, marker := range []string{"Chart.yaml", "kustomization.yaml"} {
		dir := t.TempDir()
		writeFile(t, dir, marker, "name: x\n")
		data, _ := os.ReadFile("../examples/shop/fixed.yaml")
		writeFile(t, dir, "deploy.yaml", string(data))
		expectRefused(t, marker, nil, "--pkg", "./examples/shop", dir)
	}
	expectRefused(t, "wrong container", nil, "--pkg", "./examples/shop", "--container", "web", "examples/shop/fixed.yaml")
	expectRefused(t, "missing file", nil, "--pkg", "./examples/shop", "examples/shop/nope.yaml")
}

// Bad programs, each with a real manifest.
func TestBadPrograms(t *testing.T) {
	manifest := "examples/shop/fixed.yaml"
	for _, p := range []string{"exits0", "panics", "fatal", "hangs", "nohttp", "library", "listenerr"} {
		p := p
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			expectRefused(t, p, nil, "--pkg", "./tests/testdata/"+p, "--wait", "1s", "--container", "api", manifest)
		})
	}
	src, err := os.ReadFile("../examples/shop/main.go")
	if err != nil {
		t.Fatal(err)
	}
	half := len(src) / 2
	line := strings.LastIndexByte(string(src[:half]), '\n') + 1
	sources := map[string]string{
		"empty file":    "",
		"random text":   "lorem ipsum dolor sit amet\n",
		"binary":        "\x00\x01\x02garbage\xff",
		"cut mid-line":  string(src[:half]),
		"cut on a line": string(src[:line]),
		"build output":  "# example.com/shop\n./main.go:12:2: undefined: serve\n",
		"test output":   "--- FAIL: TestHealthz (0.00s)\nFAIL\nexit status 1\n",
		"type error":    "package main\n\nimport \"net/http\"\n\nfunc main() { http.ListenAndServe(8080, nil) }\n",
		"no main func":  "package main\n\nimport \"net/http\"\n\nvar _ = http.NotFoundHandler()\n",
	}
	for name, s := range sources {
		name, s := name, s
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeFile(t, dir, "go.mod", "module example.com/bad\n\ngo 1.22\n")
			writeFile(t, dir, "main.go", s)
			expectRefused(t, name, nil, "--pkg", dir, "--wait", "1s", "--container", "api", manifest)
		})
	}
	t.Run("empty dir", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "go.mod", "module example.com/bad\n\ngo 1.22\n")
		expectRefused(t, "empty dir", nil, "--pkg", dir, "--container", "api", manifest)
	})
	expectRefused(t, "missing pkg", nil, "--pkg", "./tests/testdata/nope", "--container", "api", manifest)
}

// A program that needs a Secret's value tells the user which one.
func TestMissingSecretNamed(t *testing.T) {
	yaml := deployment("app", nil, "livenessProbe:\n  httpGet: {path: /ok, port: 8080}")
	yaml = strings.Replace(yaml, "          ports:\n", "          env:\n            - name: DATABASE_URL\n              valueFrom: {secretKeyRef: {name: db, key: url}}\n          ports:\n", 1)
	f := writeFile(t, t.TempDir(), "app.yaml", yaml)
	r := runTool(t, nil, "--pkg", "./tests/testdata/fatal", f)
	if r.code != 2 || !strings.Contains(r.stderr, "DATABASE_URL, which comes from Secret db") || !strings.Contains(r.stderr, "DATABASE_URL is not set") {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	// With the value given, the program starts. fatal serves nothing at
	// /ok, so the probe fails, and it isn't refused.
	r = runTool(t, nil, "--pkg", "./tests/testdata/fatal", "--env", "DATABASE_URL=postgres://x", f)
	if r.code != 1 || !strings.Contains(r.stdout, "not-found") {
		t.Fatalf("with --env: exit %d: %s %s", r.code, r.stdout, r.stderr)
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--format", "xml", "examples/shop/fixed.yaml"},
		{"--fail-on", "sometimes", "examples/shop/fixed.yaml"},
		{"--wait", "0s", "examples/shop/fixed.yaml"},
		{"--env", "NOEQUALS", "examples/shop/fixed.yaml"},
		{"--nope"},
	} {
		if r := runTool(t, nil, args...); r.code != 2 || r.stdout != "" {
			t.Errorf("%v: exit %d, stdout %q", args, r.code, r.stdout)
		}
	}
	if r := runTool(t, nil, "--version"); r.code != 0 || r.stdout != "probegap 0.1.0\n" {
		t.Errorf("--version: %d %q", r.code, r.stdout)
	}
}
