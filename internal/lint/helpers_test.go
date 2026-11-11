package lint

import (
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/rahulkumar96t/k8s-manifest-lint/internal/config"
	"github.com/rahulkumar96t/k8s-manifest-lint/internal/manifest"
)

// Rule tests start from a manifest that passes every rule under the default
// config and apply small edits, so each case reads as "this one change
// triggers (or does not trigger) this rule".

const cleanDeployment = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  labels: {app.kubernetes.io/name: web}
spec:
  replicas: 1
  selector: {matchLabels: {app: web}}
  template:
    metadata:
      labels: {app: web}
    spec:
      containers:
        - name: web
          image: registry.example.com/web:1.0.0
          resources:
            requests: {cpu: 100m, memory: 64Mi}
            limits: {cpu: 200m, memory: 128Mi}
          livenessProbe: {httpGet: {path: /healthz, port: 8080}}
          readinessProbe: {httpGet: {path: /ready, port: 8080}}
          securityContext:
            runAsNonRoot: true
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
`

const (
	pod = "spec.template.spec"
	c0  = pod + ".containers[0]"
)

type edit func(t *testing.T, doc map[string]any)

// set assigns v at a dotted path such as "spec.template.spec.containers[0].image",
// creating intermediate maps as needed.
func set(path string, v any) edit {
	return func(t *testing.T, doc map[string]any) {
		parent, last := walk(t, doc, path)
		assign(t, parent, last, v)
	}
}

// del removes the key at path.
func del(path string) edit {
	return func(t *testing.T, doc map[string]any) {
		parent, last := walk(t, doc, path)
		m, ok := parent.(map[string]any)
		if !ok {
			t.Fatalf("del %s: parent is %T", path, parent)
		}
		delete(m, last.(string))
	}
}

func walk(t *testing.T, doc map[string]any, path string) (parent any, last any) {
	t.Helper()
	steps := parsePath(path)
	var cur any = doc
	for _, s := range steps[:len(steps)-1] {
		switch k := s.(type) {
		case int:
			cur = cur.([]any)[k]
		case string:
			m := cur.(map[string]any)
			if m[k] == nil {
				m[k] = map[string]any{}
			}
			cur = m[k]
		}
	}
	return cur, steps[len(steps)-1]
}

func assign(t *testing.T, parent, key, v any) {
	t.Helper()
	switch k := key.(type) {
	case int:
		parent.([]any)[k] = v
	case string:
		parent.(map[string]any)[k] = v
	}
}

func parsePath(path string) []any {
	var steps []any
	for _, part := range strings.Split(path, ".") {
		name, rest, _ := strings.Cut(part, "[")
		if name != "" {
			steps = append(steps, name)
		}
		for rest != "" {
			num, after, _ := strings.Cut(rest, "]")
			i, _ := strconv.Atoi(num)
			steps = append(steps, i)
			rest = strings.TrimPrefix(after, "[")
		}
	}
	return steps
}

// doc renders base with edits applied, as YAML.
func doc(t *testing.T, base string, edits ...edit) string {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal([]byte(base), &m); err != nil {
		t.Fatal(err)
	}
	for _, e := range edits {
		e(t, m)
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// run lints the given YAML documents with cfg (defaults when nil).
func run(t *testing.T, cfg *config.Config, docs ...string) *Result {
	t.Helper()
	if cfg == nil {
		cfg = config.Default()
	}
	objs, err := manifest.Parse(strings.NewReader(strings.Join(docs, "\n---\n")), "test.yaml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	l, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := l.Run(objs)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func byRule(res *Result, id string) []Finding {
	var out []Finding
	for _, f := range res.Findings {
		if f.RuleID == id {
			out = append(out, f)
		}
	}
	return out
}

// cfgWith returns the default config modified by fn.
func cfgWith(fn func(*config.Config)) *config.Config {
	c := config.Default()
	fn(c)
	return c
}
