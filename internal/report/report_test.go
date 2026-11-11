package report

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/rahulkumar96t/k8s-manifest-lint/internal/config"
	"github.com/rahulkumar96t/k8s-manifest-lint/internal/lint"
	"github.com/rahulkumar96t/k8s-manifest-lint/internal/manifest"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

// result lints testdata/input.yaml with liveness-probe lowered to info so
// all three severities appear in the output.
func result(t *testing.T) *lint.Result {
	t.Helper()
	objs, err := manifest.Load([]string{filepath.Join("testdata", "input.yaml")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse([]byte("rules: {liveness-probe: {severity: info}}"))
	if err != nil {
		t.Fatal(err)
	}
	l, err := lint.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := l.Run(objs)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestGolden(t *testing.T) {
	res := result(t)
	for _, format := range Formats {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Write(&buf, format, res, "0.0.0-test"); err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", "report."+format+".golden")
			if *update {
				if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test ./internal/report -update to create it)", err)
			}
			if !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("%s output differs from %s\n--- got ---\n%s\n--- want ---\n%s", format, golden, buf.String(), want)
			}
		})
	}
}

func TestEmptyResult(t *testing.T) {
	res := &lint.Result{Objects: 1, Rules: lint.Registry()}
	var text, js, sarif bytes.Buffer
	if err := Text(&text, res); err != nil || text.String() != "1 object checked: no findings\n" {
		t.Errorf("text = %q, %v", text.String(), err)
	}
	if err := JSON(&js, res); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(js.Bytes(), &decoded); err != nil || decoded["findings"] == nil {
		t.Errorf("JSON findings must be [] not null: %s", js.String())
	}
	if err := SARIF(&sarif, res, ""); err != nil {
		t.Fatal(err)
	}
	var log sarifLog
	if err := json.Unmarshal(sarif.Bytes(), &log); err != nil {
		t.Fatal(err)
	}
	if log.Version != "2.1.0" || len(log.Runs) != 1 || log.Runs[0].Results == nil || len(log.Runs[0].Tool.Driver.Rules) != len(lint.Registry()) {
		t.Errorf("SARIF skeleton = %+v", log)
	}
}

func TestSARIFRuleIndexAndURIs(t *testing.T) {
	res := result(t)
	var buf bytes.Buffer
	if err := SARIF(&buf, res, "x"); err != nil {
		t.Fatal(err)
	}
	var log sarifLog
	if err := json.Unmarshal(buf.Bytes(), &log); err != nil {
		t.Fatal(err)
	}
	run := log.Runs[0]
	for _, r := range run.Results {
		if run.Tool.Driver.Rules[r.RuleIndex].ID != r.RuleID {
			t.Errorf("result %s has ruleIndex %d pointing at %s", r.RuleID, r.RuleIndex, run.Tool.Driver.Rules[r.RuleIndex].ID)
		}
		if uri := r.Locations[0].PhysicalLocation.ArtifactLocation.URI; uri != "testdata/input.yaml" {
			t.Errorf("uri = %q", uri)
		}
	}
	for in, want := range map[string]string{
		"./k8s/app.yaml":       "k8s/app.yaml",
		"rendered.yaml":        "rendered.yaml",
		manifest.StdinName:     "stdin",
		"a/../b/rendered.yaml": "b/rendered.yaml",
	} {
		if got := artifactURI(in); got != want {
			t.Errorf("artifactURI(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnknownFormat(t *testing.T) {
	if err := Write(&bytes.Buffer{}, "xml", &lint.Result{}, ""); err == nil {
		t.Fatal("want error")
	}
}
