package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rahulkumar96t/k8s-manifest-lint/internal/lint"
)

const examplesConfig = "../../examples/.k8s-lint.yaml"

func run(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Only warnings under the default config: no probes and no securityContext.
const warningsOnly = `apiVersion: v1
kind: Pod
metadata: {name: p, labels: {app.kubernetes.io/name: p}}
spec:
  containers:
    - name: c
      image: registry.example.com/c:1.0
      resources: {requests: {cpu: 1, memory: 1Gi}, limits: {cpu: 1, memory: 1Gi}}
`

func TestExamplesGoodAreClean(t *testing.T) {
	code, out, errOut := run(t, "", "--config", examplesConfig, "../../examples/good")
	if code != ExitOK || !strings.Contains(out, "no findings") {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
}

func TestExamplesBadTriggerEveryRule(t *testing.T) {
	code, out, _ := run(t, "", "-c", examplesConfig, "-f", "json", "../../examples/bad")
	if code != ExitFindings {
		t.Fatalf("exit %d, want %d", code, ExitFindings)
	}
	var rep struct {
		Findings []struct {
			RuleID string `json:"ruleId"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range rep.Findings {
		seen[f.RuleID] = true
	}
	for _, r := range lint.Registry() {
		if !seen[r.ID] {
			t.Errorf("examples/bad does not trigger %s", r.ID)
		}
	}
}

func TestFailOnThreshold(t *testing.T) {
	cases := []struct {
		args []string
		want int
	}{
		{nil, ExitFindings}, // default threshold: warning
		{[]string{"--fail-on", "warning"}, ExitFindings},
		{[]string{"--fail-on", "error"}, ExitOK},
		{[]string{"--fail-on=info"}, ExitFindings},
		{[]string{"--fail-on", "none"}, ExitOK},
		{[]string{"--fail-on", "fatal"}, ExitError},
	}
	for _, tc := range cases {
		if code, _, errOut := run(t, warningsOnly, tc.args...); code != tc.want {
			t.Errorf("%v: exit %d, want %d (%s)", tc.args, code, tc.want, errOut)
		}
	}
}

func TestFailOnFromConfigAndFlagOverride(t *testing.T) {
	cfg := write(t, t.TempDir(), "cfg.yaml", "failOn: error\n")
	if code, _, _ := run(t, warningsOnly, "-c", cfg); code != ExitOK {
		t.Errorf("config failOn: error: exit %d, want 0", code)
	}
	if code, _, _ := run(t, warningsOnly, "-c", cfg, "--fail-on", "warning"); code != ExitFindings {
		t.Errorf("flag should override config: exit %d, want 1", code)
	}
}

func TestStdinInput(t *testing.T) {
	// No path: read stdin.
	code, out, _ := run(t, warningsOnly)
	if code != ExitFindings || !strings.Contains(out, "<stdin>:6:7: warning liveness-probe") {
		t.Fatalf("exit %d, stdout:\n%s", code, out)
	}
	// "-" mixed with a file.
	file := write(t, t.TempDir(), "a.yaml", warningsOnly)
	_, out, _ = run(t, warningsOnly, file, "-", "--fail-on", "none")
	if !strings.Contains(out, "<stdin>:") || !strings.Contains(out, "a.yaml:") || !strings.Contains(out, "2 objects checked") {
		t.Fatalf("stdout:\n%s", out)
	}
}

func TestFlagsAfterPathsAndDoubleDash(t *testing.T) {
	dir := t.TempDir()
	odd := write(t, dir, "-odd.yaml", warningsOnly)
	code, out, errOut := run(t, "", "--format", "json", "--", odd)
	if code != ExitFindings || !json.Valid([]byte(out)) {
		t.Fatalf("exit %d, stderr %s", code, errOut)
	}
	normal := write(t, dir, "p.yaml", warningsOnly)
	code, out, _ = run(t, "", normal, "--format", "json", "--fail-on", "error")
	if code != ExitOK || !json.Valid([]byte(out)) {
		t.Fatalf("interleaved flags: exit %d, stdout %s", code, out)
	}
}

func TestOutputFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.sarif")
	code, out, _ := run(t, warningsOnly, "-f", "sarif", "-o", path)
	if code != ExitFindings || out != "" {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte(`"version": "2.1.0"`)) {
		t.Fatalf("output file: %v\n%s", err, data)
	}
	if code, _, _ := run(t, warningsOnly, "-o", filepath.Join(t.TempDir(), "missing", "x")); code != ExitError {
		t.Errorf("unwritable output: exit %d, want 2", code)
	}
}

func TestUsageAndInputErrors(t *testing.T) {
	dir := t.TempDir()
	badYAML := write(t, dir, "bad.yaml", "apiVersion: v1\nkind: Pod\nmetadata:\n  name: x\n bad: [\n")
	badType := write(t, dir, "type.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: d}\nspec:\n  replicas: three\n")
	badCfg := write(t, dir, "cfg.yaml", "rules: {not-a-rule: {enabled: false}}\n")
	cases := []struct {
		name string
		args []string
		want string // stderr substring
	}{
		{"unknown flag", []string{"--bogus"}, "flag provided but not defined"},
		{"unknown format", []string{"--format", "xml", badYAML}, `unknown format "xml"`},
		{"missing file", []string{filepath.Join(dir, "nope.yaml")}, "nope.yaml"},
		{"yaml syntax", []string{badYAML}, "bad.yaml:4: yaml:"},
		{"type error", []string{badType}, "type.yaml:5: Deployment d: cannot unmarshal"},
		{"bad config", []string{"-c", badCfg, badYAML}, `unknown rule "not-a-rule"`},
		{"missing config", []string{"-c", filepath.Join(dir, "none.yaml"), badYAML}, "none.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, errOut := run(t, "", tc.args...)
			if code != ExitError || !strings.Contains(errOut, tc.want) {
				t.Fatalf("exit %d, stderr %q; want 2 and %q", code, errOut, tc.want)
			}
		})
	}
}

func TestHelpVersionAndListRules(t *testing.T) {
	if code, _, errOut := run(t, "", "-h"); code != ExitOK || !strings.Contains(errOut, "Usage: k8s-manifest-lint") {
		t.Errorf("-h: exit %d, stderr %q", code, errOut)
	}
	Version = "v9.9.9"
	t.Cleanup(func() { Version = "" })
	if code, out, _ := run(t, "", "--version"); code != ExitOK || out != "v9.9.9\n" {
		t.Errorf("--version: exit %d, stdout %q", code, out)
	}

	cfg := write(t, t.TempDir(), "cfg.yaml", "rules: {host-path: {enabled: false}, pdb-required: {severity: error}}\n")
	code, out, _ := run(t, "", "--list-rules", "-c", cfg)
	if code != ExitOK {
		t.Fatalf("--list-rules: exit %d", code)
	}
	for _, want := range []string{"host-path                 off", "pdb-required              error", "image-tag                 error"} {
		if !strings.Contains(out, want) {
			t.Errorf("--list-rules output lacks %q:\n%s", want, out)
		}
	}
}

func TestNoPathsWithCharacterDeviceStdinPrintsUsage(t *testing.T) {
	// os.DevNull is a character device, like a terminal: with no paths and
	// nothing piped in, print usage instead of waiting for input.
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()
	var out, errOut bytes.Buffer
	if code := Run(nil, f, &out, &errOut); code != ExitError || !strings.Contains(errOut.String(), "Usage:") {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
}
