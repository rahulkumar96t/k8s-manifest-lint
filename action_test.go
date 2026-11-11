// Tests for the composite GitHub Action in action.yml. They run the action's
// own shell steps with the environment a runner would provide, so the
// install and lint logic is exercised without GitHub. The setup-go and
// upload-sarif steps are third-party actions and are not run here.
package action_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type step struct {
	Name  string            `yaml:"name"`
	ID    string            `yaml:"id"`
	If    string            `yaml:"if"`
	Uses  string            `yaml:"uses"`
	Shell string            `yaml:"shell"`
	Run   string            `yaml:"run"`
	Env   map[string]string `yaml:"env"`
}

type action struct {
	Inputs map[string]struct {
		Default string `yaml:"default"`
	} `yaml:"inputs"`
	Runs struct {
		Using string `yaml:"using"`
		Steps []step `yaml:"steps"`
	} `yaml:"runs"`
}

func load(t *testing.T) (action, string) {
	t.Helper()
	raw, err := os.ReadFile("action.yml")
	if err != nil {
		t.Fatal(err)
	}
	var a action
	if err := yaml.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	if a.Runs.Using != "composite" {
		t.Fatalf("runs.using = %q", a.Runs.Using)
	}
	return a, string(raw)
}

func stepNamed(t *testing.T, a action, name string) step {
	t.Helper()
	for _, s := range a.Runs.Steps {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no step %q", name)
	return step{}
}

func TestActionInputsAreDeclaredAndUsed(t *testing.T) {
	a, raw := load(t)
	used := map[string]bool{}
	for _, m := range regexp.MustCompile(`inputs\.([a-z-]+)`).FindAllStringSubmatch(raw, -1) {
		used[m[1]] = true
		if _, ok := a.Inputs[m[1]]; !ok {
			t.Errorf("inputs.%s is referenced but not declared", m[1])
		}
	}
	for name := range a.Inputs {
		if !used[name] {
			t.Errorf("input %s is declared but never used", name)
		}
	}
}

func TestActionScriptsTakeInputsFromEnv(t *testing.T) {
	a, _ := load(t)
	for _, s := range a.Runs.Steps {
		if s.Run != "" && strings.Contains(s.Run, "${{") {
			t.Errorf("step %q interpolates an expression into its script; pass it through env", s.Name)
		}
	}
}

func TestActionScriptsPassShellcheck(t *testing.T) {
	sc, err := exec.LookPath("shellcheck")
	if err != nil {
		t.Skip("shellcheck not installed")
	}
	a, _ := load(t)
	dir := t.TempDir()
	for i, s := range a.Runs.Steps {
		if s.Run == "" {
			continue
		}
		p := filepath.Join(dir, "step"+string(rune('a'+i))+".sh")
		if err := os.WriteFile(p, []byte("#!/usr/bin/env bash\n"+s.Run), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(sc, "--shell=bash", p).CombinedOutput(); err != nil {
			t.Errorf("shellcheck %q:\n%s", s.Name, out)
		}
	}
}

// runStep runs a step's script the way a runner does (bash -eo pipefail)
// and returns the key=value pairs it wrote to GITHUB_OUTPUT.
func runStep(t *testing.T, s step, dir string, env map[string]string) (map[string]string, error) {
	t.Helper()
	outFile := filepath.Join(t.TempDir(), "github_output")
	cmd := exec.Command("bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", s.Run)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GITHUB_OUTPUT="+outFile)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	log, err := cmd.CombinedOutput()
	t.Logf("%s:\n%s", s.Name, log)
	data, _ := os.ReadFile(outFile)
	outputs := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			outputs[k] = v
		}
	}
	return outputs, err
}

func TestActionInstallAndLintSteps(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	a, _ := load(t)
	repo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	// Install: the default (no version) builds the bundled source.
	runnerTemp := t.TempDir()
	githubPath := filepath.Join(runnerTemp, "github_path")
	if _, err := runStep(t, stepNamed(t, a, "Install k8s-manifest-lint"), repo, map[string]string{
		"INPUT_VERSION": "", "ACTION_PATH": repo, "ACTION_REF": "v0.0.0-test",
		"RUNNER_TEMP": runnerTemp, "GITHUB_PATH": githubPath,
	}); err != nil {
		t.Fatalf("install step failed: %v", err)
	}
	binDir, _ := os.ReadFile(githubPath)
	pathEnv := strings.TrimSpace(string(binDir)) + string(os.PathListSeparator) + os.Getenv("PATH")
	if out, err := exec.Command(filepath.Join(strings.TrimSpace(string(binDir)), "k8s-manifest-lint"), "--version").Output(); err != nil || string(out) != "v0.0.0-test\n" {
		t.Fatalf("installed binary --version = %q, %v", out, err)
	}

	lint := stepNamed(t, a, "Run k8s-manifest-lint")
	examples := filepath.Join(repo, "examples")
	broken := filepath.Join(t.TempDir(), "broken.yaml")
	if err := os.WriteFile(broken, []byte("kind: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, paths, failOn string
		wantCode            string
		wantSARIF           bool
	}{
		{"clean", filepath.Join(examples, "good"), "", "0", true},
		{"findings", filepath.Join(examples, "good") + "\n" + filepath.Join(examples, "bad"), "", "1", true},
		{"findings below threshold", filepath.Join(examples, "bad"), "none", "0", true},
		{"parse error", broken, "", "2", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			outputs, err := runStep(t, lint, work, map[string]string{
				"PATH": pathEnv, "INPUT_PATHS": tc.paths, "INPUT_CONFIG": filepath.Join(examples, ".k8s-lint.yaml"),
				"INPUT_FAIL_ON": tc.failOn, "INPUT_FORMAT": "text", "INPUT_SARIF_FILE": "reports/k8s-manifest-lint.sarif",
			})
			if err != nil {
				t.Fatalf("lint step must not fail on its own (exit code is reported as an output): %v", err)
			}
			if outputs["exit-code"] != tc.wantCode {
				t.Errorf("exit-code = %q, want %q", outputs["exit-code"], tc.wantCode)
			}
			sarif, wrote := outputs["sarif-file"]
			if wrote != tc.wantSARIF {
				t.Fatalf("sarif-file output = %q, want written=%v", sarif, tc.wantSARIF)
			}
			if wrote {
				if !filepath.IsAbs(sarif) {
					t.Errorf("sarif-file %q is not absolute", sarif)
				}
				if data, err := os.ReadFile(sarif); err != nil || !strings.Contains(string(data), `"version": "2.1.0"`) {
					t.Errorf("SARIF file: %v", err)
				}
			}
		})
	}
}
