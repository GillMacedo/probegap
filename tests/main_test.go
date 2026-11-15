// Package tests runs probegap as a binary, the way the skill does, against
// test programs and the examples.
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "probegap-tests")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "probegap")
	cmd := exec.Command("go", "build", "-o", bin, "./plugin/scripts")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building probegap: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type result struct {
	code           int
	stdout, stderr string
}

// runTool runs probegap from the repository root.
func runTool(t *testing.T, stdin []byte, args ...string) result {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = ".."
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{code, out.String(), errb.String()}
}

type jsonFinding struct {
	Severity, Code, Kind, Name, Container, Probe, Message, Fix string
}

type jsonReport struct {
	Program    string
	Probes     int
	Checked    int `json:"checked_probes"`
	Containers []struct {
		Name    string
		Servers []struct {
			Address string
			Port    int
			TLS     bool
		}
	}
	Findings []jsonFinding
}

func runJSON(t *testing.T, stdin []byte, args ...string) (jsonReport, result) {
	t.Helper()
	r := runTool(t, stdin, append([]string{"--format", "json"}, args...)...)
	var rep jsonReport
	if r.code == 2 {
		return rep, r
	}
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatalf("bad JSON (%v):\n%s\nstderr: %s", err, r.stdout, r.stderr)
	}
	return rep, r
}

// deployment writes a Deployment whose container "app" has the given
// probes (YAML, indented for the container) and args.
func deployment(name string, args []string, probes string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: test
spec:
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
        - name: app
          image: example.com/app:1
`, name, name, name)
	if len(args) > 0 {
		q, _ := json.Marshal(args)
		fmt.Fprintf(&b, "          args: %s\n", q)
	}
	b.WriteString(`          ports:
            - name: http
              containerPort: 8080
            - name: https
              containerPort: 8443
            - name: raw
              containerPort: 7000
`)
	for _, l := range strings.Split(strings.TrimRight(probes, "\n"), "\n") {
		b.WriteString("          " + l + "\n")
	}
	b.WriteString("---\n")
	return b.String()
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
