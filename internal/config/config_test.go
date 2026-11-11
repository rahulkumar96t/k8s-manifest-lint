package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	c := Default()
	if !reflect.DeepEqual(c.RequiredLabels, []string{"app.kubernetes.io/name"}) ||
		!reflect.DeepEqual(c.Resources.Requests, []string{"cpu", "memory"}) ||
		!reflect.DeepEqual(c.Resources.Limits, []string{"cpu", "memory"}) ||
		len(c.AllowedRegistries) != 0 || c.Rules == nil || c.FailOn != "" {
		t.Fatalf("Default() = %+v", c)
	}
}

func TestParseFullConfig(t *testing.T) {
	src := `
failOn: error
allowedRegistries: ["111111111111.dkr.ecr.*.amazonaws.com", artifactory.example.com/docker-local]
requiredLabels: [app.kubernetes.io/name, team]
resources:
  limits: [memory]
rules:
  read-only-root-fs: {enabled: false}
  pdb-required: {severity: error}
`
	c, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if c.FailOn != "error" || len(c.AllowedRegistries) != 2 || c.RequiredLabels[1] != "team" {
		t.Errorf("top-level fields: %+v", c)
	}
	// Unset lists keep their defaults; set lists replace them.
	if !reflect.DeepEqual(c.Resources.Requests, []string{"cpu", "memory"}) || !reflect.DeepEqual(c.Resources.Limits, []string{"memory"}) {
		t.Errorf("resources = %+v", c.Resources)
	}
	if e := c.Rules["read-only-root-fs"].Enabled; e == nil || *e {
		t.Errorf("read-only-root-fs enabled = %v", e)
	}
	if c.Rules["pdb-required"].Severity != "error" {
		t.Errorf("pdb-required severity = %q", c.Rules["pdb-required"].Severity)
	}
}

func TestExplicitEmptyListMeansNone(t *testing.T) {
	c, err := Parse([]byte("requiredLabels: []\nresources: {limits: []}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.RequiredLabels == nil || len(c.RequiredLabels) != 0 || len(c.Resources.Limits) != 0 {
		t.Fatalf("empty lists were replaced by defaults: %+v", c)
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	for _, src := range []string{"requiredLabel: [team]\n", "rules: {image-tag: {enable: false}}\n"} {
		if _, err := Parse([]byte(src)); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("Parse(%q) error = %v, want unknown field error", src, err)
		}
	}
	if c, err := Parse(nil); err != nil || c.RequiredLabels == nil {
		t.Errorf("empty file: %+v, %v", c, err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lint.yaml")
	if err := os.WriteFile(path, []byte("failOn: info\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(path); err != nil || c.FailOn != "info" {
		t.Fatalf("Load(explicit) = %+v, %v", c, err)
	}
	if _, err := Load(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Error("explicit missing file: want error")
	}
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("rules: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad); err == nil || !strings.Contains(err.Error(), "bad.yaml") {
		t.Errorf("bad file error = %v, want it to name the file", err)
	}

	// Without --config, .k8s-lint.yaml in the working directory is used if present.
	chdir(t, dir)
	if c, err := Load(""); err != nil || c.FailOn != "" {
		t.Fatalf("Load(\"\") without default file = %+v, %v", c, err)
	}
	if err := os.WriteFile(DefaultFile, []byte("failOn: error\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(""); err != nil || c.FailOn != "error" {
		t.Fatalf("Load(\"\") with default file = %+v, %v", c, err)
	}
}

// chdir changes the working directory for the rest of the test
// (testing.T.Chdir needs Go 1.24; this module supports 1.23).
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}
