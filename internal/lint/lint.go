// Package lint runs the rule set over an index of parsed manifests and
// applies configuration and inline suppressions.
package lint

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/rahulkumar96t/k8s-manifest-lint/internal/config"
	"github.com/rahulkumar96t/k8s-manifest-lint/internal/manifest"
)

// IgnoreAnnotation lists rule ids to skip for one object, comma separated.
const IgnoreAnnotation = "k8s-lint.io/ignore"

// Severity orders findings; higher is more severe.
type Severity int

// Severities, from least to most severe. None is only meaningful as a
// --fail-on threshold that nothing reaches.
const (
	Info Severity = iota + 1
	Warning
	Error
	None
)

var severityNames = map[Severity]string{Info: "info", Warning: "warning", Error: "error", None: "none"}

func (s Severity) String() string { return severityNames[s] }

// ParseSeverity accepts info, warning, error and none.
func ParseSeverity(s string) (Severity, error) {
	for sev, name := range severityNames {
		if strings.EqualFold(s, name) {
			return sev, nil
		}
	}
	return 0, fmt.Errorf("unknown severity %q (want info, warning, error or none)", s)
}

// Rule is a single check. Check receives the whole index so cross-object
// rules (PDBs, Service selectors) need no special casing.
type Rule struct {
	ID        string
	Severity  Severity // default; config may override
	Category  string   // reliability, security, supply-chain or convention
	Summary   string   // what the rule requires, one line
	Rationale string   // why it matters
	Check     func(*Pass)
}

// Pass is handed to a rule: the input index, the settings and a sink.
type Pass struct {
	Index    *manifest.Index
	Config   *config.Config
	rule     *Rule
	findings []Finding
}

// Report records a finding at path inside obj.
func (p *Pass) Report(obj *manifest.Object, at manifest.Path, format string, args ...any) {
	p.findings = append(p.findings, Finding{
		RuleID: p.rule.ID, Severity: p.rule.Severity,
		Message: fmt.Sprintf(format, args...),
		Object:  obj, Path: at,
	})
}

// Finding is one reported problem, with its resolved position.
type Finding struct {
	RuleID   string
	Severity Severity
	Message  string
	Object   *manifest.Object
	Path     manifest.Path
	File     string
	Line     int
	Column   int
}

// Result is the outcome of a run.
type Result struct {
	Findings   []Finding
	Suppressed int
	Objects    int
	Rules      []*Rule // the enabled rules, with effective severities
}

// Count returns how many findings are at or above sev.
func (r *Result) Count(sev Severity) int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity >= sev {
			n++
		}
	}
	return n
}

// Linter holds the effective rule set for one configuration.
type Linter struct {
	cfg   *config.Config
	rules []*Rule
}

// New validates cfg against the registry and resolves rule overrides.
func New(cfg *config.Config) (*Linter, error) {
	if cfg == nil {
		cfg = config.Default()
	}
	byID := map[string]*Rule{}
	for _, r := range Registry() {
		byID[r.ID] = r
	}
	for id := range cfg.Rules {
		if byID[id] == nil {
			return nil, fmt.Errorf("config: unknown rule %q", id)
		}
	}
	for _, pattern := range cfg.AllowedRegistries {
		if _, err := path.Match(pattern, ""); err != nil {
			return nil, fmt.Errorf("config: allowedRegistries: bad pattern %q", pattern)
		}
	}
	l := &Linter{cfg: cfg}
	for _, r := range Registry() {
		rc := cfg.Rules[r.ID]
		if rc.Enabled != nil && !*rc.Enabled {
			continue
		}
		eff := *r
		if rc.Severity != "" {
			sev, err := ParseSeverity(rc.Severity)
			if err != nil || sev == None {
				return nil, fmt.Errorf("config: rule %s: unknown severity %q", r.ID, rc.Severity)
			}
			eff.Severity = sev
		}
		l.rules = append(l.rules, &eff)
	}
	return l, nil
}

// Rules returns the enabled rules with their effective severities.
func (l *Linter) Rules() []*Rule { return l.rules }

// Run lints objs. It fails only if an object cannot be decoded.
func (l *Linter) Run(objs []*manifest.Object) (*Result, error) {
	idx, err := manifest.BuildIndex(objs)
	if err != nil {
		return nil, err
	}
	res := &Result{Objects: len(objs), Rules: l.rules}
	for _, r := range l.rules {
		pass := &Pass{Index: idx, Config: l.cfg, rule: r}
		r.Check(pass)
		for _, f := range pass.findings {
			if ignored(f.Object, f.RuleID) {
				res.Suppressed++
				continue
			}
			f.File = f.Object.File
			f.Line, f.Column = f.Object.Locate(f.Path)
			res.Findings = append(res.Findings, f)
		}
	}
	sort.SliceStable(res.Findings, func(i, j int) bool {
		a, b := res.Findings[i], res.Findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return a.RuleID < b.RuleID
	})
	return res, nil
}

func ignored(o *manifest.Object, ruleID string) bool {
	for _, id := range strings.Split(o.Annotations[IgnoreAnnotation], ",") {
		if strings.TrimSpace(id) == ruleID {
			return true
		}
	}
	return false
}
