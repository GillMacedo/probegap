package tests

import (
	"os"
	"strings"
	"testing"
)

// TestCutManifest cuts a file holding the broken and the fixed example at
// every line break and at every fifth byte. A mid-line cut must be refused. A cut on a line break
// must be refused or give only findings the whole file gives, and never a
// clean result.
func TestCutManifest(t *testing.T) {
	broken, err := os.ReadFile("../examples/shop/broken.yaml")
	if err != nil {
		t.Fatal(err)
	}
	fixed, err := os.ReadFile("../examples/shop/fixed.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	data := append(append([]byte{}, broken...), strings.Replace(string(fixed), "name: api\n  namespace", "name: api-fixed\n  namespace", 1)...)
	both := writeFile(t, dir, "both.yaml", string(data))
	whole, r := runJSON(t, nil, "--pkg", "./examples/shop", "--container", "api", both)
	if r.code != 1 {
		t.Fatalf("whole file: exit %d %s", r.code, r.stderr)
	}
	want := map[string]bool{}
	for _, f := range whole.Findings {
		want[f.Code+" "+f.Probe] = true
	}
	checked := 0
	for i := 1; i < len(data)-len("---\n"); i++ {
		onLine := data[i-1] == '\n'
		if !onLine && i%5 != 0 {
			continue
		}
		f := writeFile(t, dir, "cut.yaml", string(data[:i]))
		rep, r := runJSON(t, nil, "--pkg", "./examples/shop", "--container", "api", f)
		if r.code == 2 {
			continue
		}
		if !onLine {
			t.Errorf("cut mid-line at byte %d was not refused: exit %d", i, r.code)
			continue
		}
		checked++
		if r.code == 0 {
			t.Errorf("cut at byte %d gave a clean result", i)
		}
		for _, f := range rep.Findings {
			if !want[f.Code+" "+f.Probe] {
				t.Errorf("cut at byte %d: finding the whole file doesn't have: %+v", i, f)
			}
		}
	}
	// The cuts inside the second document that still parse.
	if checked == 0 {
		t.Fatal("no cut was checked, so this test proves nothing")
	}
	t.Logf("%d cuts checked, the rest refused", checked)
}
