// Package report renders lint results as text, JSON or SARIF 2.1.0.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"

	"github.com/rahulkumar96t/k8s-manifest-lint/internal/lint"
	"github.com/rahulkumar96t/k8s-manifest-lint/internal/manifest"
)

// Formats lists the supported output formats.
var Formats = []string{"text", "json", "sarif"}

// Write renders res in the named format. version is embedded in SARIF.
func Write(w io.Writer, format string, res *lint.Result, version string) error {
	switch format {
	case "text":
		return Text(w, res)
	case "json":
		return JSON(w, res)
	case "sarif":
		return SARIF(w, res, version)
	}
	return fmt.Errorf("unknown format %q (want %s)", format, strings.Join(Formats, ", "))
}

// Text writes one compiler-style line per finding and a summary line.
func Text(w io.Writer, res *lint.Result) error {
	var b strings.Builder
	for _, f := range res.Findings {
		fmt.Fprintf(&b, "%s: %s %s: %s (%s)\n", position(f), f.Severity, f.RuleID, f.Message, f.Object)
	}
	if len(res.Findings) > 0 {
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "%s checked: ", plural(res.Objects, "object"))
	if len(res.Findings) == 0 {
		b.WriteString("no findings")
	} else {
		var parts []string
		for _, sev := range []lint.Severity{lint.Error, lint.Warning, lint.Info} {
			if n := countExactly(res, sev); n > 0 {
				parts = append(parts, plural(n, sev.String()))
			}
		}
		fmt.Fprintf(&b, "%s (%s)", plural(len(res.Findings), "finding"), strings.Join(parts, ", "))
	}
	if res.Suppressed > 0 {
		fmt.Fprintf(&b, ", %d suppressed", res.Suppressed)
	}
	b.WriteByte('\n')
	_, err := io.WriteString(w, b.String())
	return err
}

func position(f lint.Finding) string {
	if f.Line == 0 {
		return f.File
	}
	return fmt.Sprintf("%s:%d:%d", f.File, f.Line, f.Column)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func countExactly(res *lint.Result, sev lint.Severity) int {
	n := 0
	for _, f := range res.Findings {
		if f.Severity == sev {
			n++
		}
	}
	return n
}

type jsonReport struct {
	Findings []jsonFinding `json:"findings"`
	Summary  jsonSummary   `json:"summary"`
}

type jsonFinding struct {
	RuleID   string     `json:"ruleId"`
	Severity string     `json:"severity"`
	Message  string     `json:"message"`
	File     string     `json:"file"`
	Line     int        `json:"line"`
	Column   int        `json:"column"`
	Field    string     `json:"field"`
	Object   jsonObject `json:"object"`
}

type jsonObject struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

type jsonSummary struct {
	Objects    int `json:"objects"`
	Findings   int `json:"findings"`
	Errors     int `json:"errors"`
	Warnings   int `json:"warnings"`
	Infos      int `json:"infos"`
	Suppressed int `json:"suppressed"`
}

// JSON writes a stable, indented JSON document.
func JSON(w io.Writer, res *lint.Result) error {
	out := jsonReport{Findings: []jsonFinding{}, Summary: jsonSummary{
		Objects: res.Objects, Findings: len(res.Findings), Suppressed: res.Suppressed,
		Errors: countExactly(res, lint.Error), Warnings: countExactly(res, lint.Warning), Infos: countExactly(res, lint.Info),
	}}
	for _, f := range res.Findings {
		o := f.Object
		out.Findings = append(out.Findings, jsonFinding{
			RuleID: f.RuleID, Severity: f.Severity.String(), Message: f.Message,
			File: f.File, Line: f.Line, Column: f.Column, Field: f.Path.String(),
			Object: jsonObject{APIVersion: o.APIVersion, Kind: o.Kind, Namespace: o.Namespace, Name: o.Name},
		})
	}
	return encode(w, out)
}

func encode(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// artifactURI turns a file name into the repository-relative, forward-slash
// URI that GitHub code scanning matches against checked-out files.
func artifactURI(file string) string {
	if file == manifest.StdinName {
		return "stdin"
	}
	return path.Clean(filepath.ToSlash(file))
}
