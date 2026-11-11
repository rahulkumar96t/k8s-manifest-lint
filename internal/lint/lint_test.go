package lint

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/rahulkumar96t/k8s-manifest-lint/internal/config"
)

func TestInlineSuppression(t *testing.T) {
	noisy := doc(t, cleanDeployment,
		set(c0+".image", "nginx:latest"),
		del(c0+".securityContext.readOnlyRootFilesystem"),
		set("metadata.annotations", map[string]any{IgnoreAnnotation: " image-tag , read-only-root-fs "}),
	)
	other := doc(t, cleanDeployment, set("metadata.name", "other"), set(c0+".image", "nginx:latest"))

	res := run(t, nil, noisy, other)
	if res.Suppressed != 2 {
		t.Errorf("Suppressed = %d, want 2", res.Suppressed)
	}
	// The annotation applies to its own object only.
	got := byRule(res, "image-tag")
	if len(got) != 1 || got[0].Object.Name != "other" {
		t.Fatalf("image-tag findings = %+v, want one on Deployment/other", got)
	}
	if n := len(byRule(res, "read-only-root-fs")); n != 0 {
		t.Errorf("read-only-root-fs findings = %d, want 0", n)
	}
}

func TestSuppressionMatchesWholeIDsOnly(t *testing.T) {
	src := doc(t, cleanDeployment,
		set(c0+".image", "nginx:latest"),
		set("metadata.annotations", map[string]any{IgnoreAnnotation: "image"}),
	)
	if res := run(t, nil, src); len(byRule(res, "image-tag")) != 1 || res.Suppressed != 0 {
		t.Fatalf("prefix should not suppress: %+v", res)
	}
}

func TestConfigDisablesAndRescoresRules(t *testing.T) {
	off, warn := false, "info"
	cfg := cfgWith(func(c *config.Config) {
		c.Rules["image-tag"] = config.RuleConfig{Enabled: &off}
		c.Rules["liveness-probe"] = config.RuleConfig{Severity: warn}
	})
	src := doc(t, cleanDeployment, set(c0+".image", "nginx"), del(c0+".livenessProbe"))
	res := run(t, cfg, src)
	if len(byRule(res, "image-tag")) != 0 {
		t.Error("disabled rule still reported")
	}
	lp := byRule(res, "liveness-probe")
	if len(lp) != 1 || lp[0].Severity != Info {
		t.Fatalf("liveness-probe = %+v, want one info finding", lp)
	}
	l, _ := New(cfg)
	for _, r := range l.Rules() {
		if r.ID == "image-tag" {
			t.Error("disabled rule listed in Rules()")
		}
		if r.ID == "liveness-probe" && r.Severity != Info {
			t.Error("override not reflected in Rules()")
		}
	}
	// Overrides must not leak into the shared registry.
	for _, r := range Registry() {
		if r.ID == "liveness-probe" && r.Severity != Warning {
			t.Error("registry default was modified")
		}
	}
}

func TestConfigValidation(t *testing.T) {
	cases := map[string]string{
		"rules: {no-such-rule: {enabled: false}}":      `unknown rule "no-such-rule"`,
		"rules: {image-tag: {severity: critical}}":     `unknown severity "critical"`,
		"rules: {image-tag: {severity: none}}":         `unknown severity "none"`,
		"allowedRegistries: ['registry[.example.com']": "bad pattern",
	}
	for src, want := range cases {
		cfg, err := config.Parse([]byte(src))
		if err != nil {
			t.Fatalf("Parse(%q): %v", src, err)
		}
		if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("New(%q) error = %v, want %q", src, err, want)
		}
	}
}

func TestFindingsAreSortedAndLocated(t *testing.T) {
	src := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  template:
    spec:
      hostNetwork: true
      containers:
        - name: web
          image: nginx
`
	res := run(t, nil, src)
	for i := 1; i < len(res.Findings); i++ {
		a, b := res.Findings[i-1], res.Findings[i]
		if a.Line > b.Line || a.Line == b.Line && a.Column == b.Column && a.RuleID > b.RuleID {
			t.Fatalf("not sorted: %+v before %+v", a, b)
		}
	}
	pos := map[string]string{}
	for _, f := range res.Findings {
		pos[f.RuleID] = fmt.Sprintf("%s:%d:%d", f.File, f.Line, f.Column)
	}
	want := map[string]string{
		"required-labels": "test.yaml:3:1",   // no labels: points at metadata
		"host-namespaces": "test.yaml:8:7",   // the hostNetwork key
		"image-tag":       "test.yaml:11:11", // the image key
		"resource-limits": "test.yaml:10:11", // missing block: the container
	}
	for id, w := range want {
		if pos[id] != w {
			t.Errorf("%s at %s, want %s", id, pos[id], w)
		}
	}
}

func TestSeverityThresholds(t *testing.T) {
	res := &Result{Findings: []Finding{{Severity: Info}, {Severity: Warning}, {Severity: Warning}, {Severity: Error}}}
	for sev, want := range map[Severity]int{Info: 4, Warning: 3, Error: 1, None: 0} {
		if got := res.Count(sev); got != want {
			t.Errorf("Count(%s) = %d, want %d", sev, got, want)
		}
	}
	for _, s := range []string{"info", "WARNING", "error", "none"} {
		if _, err := ParseSeverity(s); err != nil {
			t.Errorf("ParseSeverity(%q): %v", s, err)
		}
	}
	if _, err := ParseSeverity("fatal"); err == nil {
		t.Error("ParseSeverity(fatal): want error")
	}
}

func TestRegistryIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Registry() {
		if seen[r.ID] {
			t.Errorf("duplicate rule id %s", r.ID)
		}
		seen[r.ID] = true
		if r.Summary == "" || r.Rationale == "" || r.Category == "" || r.Check == nil || r.Severity < Info || r.Severity > Error {
			t.Errorf("rule %s is incomplete", r.ID)
		}
	}
}

// The README rule table is the user-facing reference; keep it in sync.
func TestReadmeDocumentsEveryRule(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range Registry() {
		row := "| `" + r.ID + "` | " + r.Severity.String() + " |"
		if !strings.Contains(string(readme), row) {
			t.Errorf("README.md has no table row starting %q", row)
		}
	}
}
