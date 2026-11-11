package manifest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) []*Object {
	t.Helper()
	objs, err := Parse(strings.NewReader(src), "in.yaml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return objs
}

func names(objs []*Object) []string {
	var out []string
	for _, o := range objs {
		out = append(out, o.String())
	}
	return out
}

func TestParseMultiDocumentHelmOutput(t *testing.T) {
	// Shape of `helm template` output: leading separator, comment-only and
	// empty documents, and a trailing separator.
	src := `---
# Source: app/templates/serviceaccount.yaml
---
# Source: app/templates/service.yaml
apiVersion: v1
kind: Service
metadata:
  name: web
  namespace: shop
---
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  labels: {app: web}
  annotations: {k8s-lint.io/ignore: "image-tag"}
---
`
	objs := mustParse(t, src)
	if got, want := names(objs), []string{"Service/shop/web", "Deployment/web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("objects = %v, want %v", got, want)
	}
	d := objs[1]
	if d.Node.Line != 12 || d.File != "in.yaml" || d.Group() != "apps" {
		t.Errorf("deployment line=%d file=%q group=%q", d.Node.Line, d.File, d.Group())
	}
	if d.Labels["app"] != "web" || d.Annotations["k8s-lint.io/ignore"] != "image-tag" {
		t.Errorf("metadata not decoded: %+v %+v", d.Labels, d.Annotations)
	}
	if objs[0].Group() != "" {
		t.Errorf("core group = %q", objs[0].Group())
	}
}

func TestParseExpandsLists(t *testing.T) {
	src := `apiVersion: v1
kind: List
items:
  - apiVersion: v1
    kind: ConfigMap
    metadata: {name: a}
  - apiVersion: apps/v1
    kind: DeploymentList
    items:
      - apiVersion: apps/v1
        kind: Deployment
        metadata: {name: b}
`
	objs := mustParse(t, src)
	if got, want := names(objs), []string{"ConfigMap/a", "Deployment/b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("objects = %v, want %v", got, want)
	}
	if objs[1].Node.Line != 10 {
		t.Errorf("nested item line = %d, want 10", objs[1].Node.Line)
	}
}

func TestParseSkipsNonKubernetesDocuments(t *testing.T) {
	src := `replicaCount: 3        # a values.yaml
---
apiVersion: v2         # a Chart.yaml: apiVersion but no kind
name: app
---
- just
- a list
---
plain scalar
---
apiVersion: v1
kind: ConfigMapList    # a List kind without items is an ordinary object
metadata: {name: odd}
`
	if got := names(mustParse(t, src)); !reflect.DeepEqual(got, []string{"ConfigMapList/odd"}) {
		t.Fatalf("objects = %v", got)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"syntax", "apiVersion: v1\nkind: Pod\nmetadata:\n  name: x\n bad: [\n", "in.yaml:4: yaml:"},
		{"type", "apiVersion: v1\nkind: Pod\nmetadata:\n  name: [x]\n", "in.yaml:4: Pod: cannot unmarshal"},
		{"list items", "apiVersion: v1\nkind: List\nitems: {a: b}\n", "in.yaml:3: List.items must be a sequence"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tc.src), "in.yaml")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestLoadWalksDirectoriesAndStdin(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, kind string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "apiVersion: v1\nkind: " + kind + "\nmetadata: {name: " + strings.ReplaceAll(rel, "/", "-") + "}\n"
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("b.yml", "ConfigMap")
	write("a.yaml", "ConfigMap")
	write("nested/c.yaml", "Secret")
	write("notes.txt", "ConfigMap")      // wrong extension
	write(".hidden/d.yaml", "ConfigMap") // hidden directory
	write(".k8s-lint.yaml", "ConfigMap") // hidden file
	write("nested/.e.yaml", "ConfigMap") // hidden file, nested

	stdin := strings.NewReader("apiVersion: v1\nkind: Namespace\nmetadata: {name: piped}\n")
	objs, err := Load([]string{dir, "-"}, stdin)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ConfigMap/a.yaml", "ConfigMap/b.yml", "Secret/nested-c.yaml", "Namespace/piped"}
	if got := names(objs); !reflect.DeepEqual(got, want) {
		t.Fatalf("objects = %v, want %v", got, want)
	}
	if objs[2].File != filepath.Join(dir, "nested", "c.yaml") || objs[3].File != StdinName {
		t.Errorf("files = %q, %q", objs[2].File, objs[3].File)
	}

	if _, err := Load([]string{filepath.Join(dir, "missing.yaml")}, nil); err == nil {
		t.Error("missing file: want error")
	}
}

func TestLocate(t *testing.T) {
	src := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  template:
    spec:
      containers:
        - name: app
          image: nginx
        - name: sidecar
          resources: {}
`
	obj := mustParse(t, src)[0]
	cases := []struct {
		path      Path
		line, col int
	}{
		{nil, 1, 1},
		{Path{"metadata", "name"}, 4, 3},
		{Path{"spec", "template", "spec", "containers"}, 8, 7},
		{Path{"spec", "template", "spec", "containers"}.Index(0).Key("image"), 10, 11},
		// Missing leaf: falls back to the container itself.
		{Path{"spec", "template", "spec", "containers"}.Index(0).Key("resources", "limits"), 9, 11},
		// Present parent, missing child.
		{Path{"spec", "template", "spec", "containers"}.Index(1).Key("resources", "limits"), 12, 11},
		// Index out of range: stays on the sequence key.
		{Path{"spec", "template", "spec", "containers"}.Index(5), 8, 7},
		// Indexing a mapping or keying a scalar stops early.
		{Path{"metadata"}.Index(0), 3, 1},
		{Path{"metadata", "name", "x"}, 4, 3},
	}
	for _, tc := range cases {
		if line, col := obj.Locate(tc.path); line != tc.line || col != tc.col {
			t.Errorf("Locate(%s) = %d:%d, want %d:%d", tc.path, line, col, tc.line, tc.col)
		}
	}
}

func TestPathIsImmutable(t *testing.T) {
	base := Path{"spec", "template"}
	a := base.Key("spec")
	b := base.Key("metadata")
	c := a.Key("containers").Index(2).Key("image")
	if a.String() != "spec.template.spec" || b.String() != "spec.template.metadata" {
		t.Fatalf("shared prefix was modified: %s, %s", a, b)
	}
	if c.String() != "spec.template.spec.containers[2].image" {
		t.Fatalf("String() = %s", c)
	}
}

func TestParseFollowsAliases(t *testing.T) {
	src := `apiVersion: v1
kind: Pod
metadata:
  name: aliased
  labels: &labels {app: web}
  annotations: *labels
`
	o := mustParse(t, src)[0]
	if o.Annotations["app"] != "web" {
		t.Errorf("alias not resolved: %v", o.Annotations)
	}
}
