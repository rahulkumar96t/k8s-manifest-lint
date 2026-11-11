// Package manifest parses Kubernetes manifests into small, purpose-built
// structs while keeping the underlying YAML nodes, so every finding can be
// mapped back to a file, line and column.
package manifest

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// StdinName is the file name used for objects read from standard input.
const StdinName = "<stdin>"

// Object is one Kubernetes object from the input.
type Object struct {
	APIVersion  string
	Kind        string
	Name        string
	Namespace   string
	Labels      map[string]string
	Annotations map[string]string

	File string     // file name as given on the command line, or StdinName
	Node *yaml.Node // mapping node of the object, used for positions
}

// Group returns the API group ("" for the core group).
func (o *Object) Group() string {
	if i := strings.Index(o.APIVersion, "/"); i >= 0 {
		return o.APIVersion[:i]
	}
	return ""
}

// String renders Kind/name or Kind/namespace/name.
func (o *Object) String() string {
	if o.Namespace == "" {
		return o.Kind + "/" + o.Name
	}
	return o.Kind + "/" + o.Namespace + "/" + o.Name
}

// Locate returns the position of the deepest existing node on p.
func (o *Object) Locate(p Path) (line, col int) { return Locate(o.Node, p) }

// Parse reads every YAML document from r. Empty documents (for example the
// comment-only ones `helm template` emits) and documents without apiVersion
// and kind (values files, Chart.yaml) are skipped. List kinds are expanded
// into their items.
func Parse(r io.Reader, name string) ([]*Object, error) {
	dec := yaml.NewDecoder(r)
	var objs []*Object
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return objs, nil
		}
		if err != nil {
			// "yaml: line 4: did not find expected key" -> "file:4: yaml: ..."
			if m := syntaxLine.FindStringSubmatch(err.Error()); m != nil {
				return nil, fmt.Errorf("%s:%s: yaml: %s", name, m[1], m[2])
			}
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if len(doc.Content) == 0 {
			continue
		}
		got, err := fromNode(doc.Content[0], name)
		if err != nil {
			return nil, err
		}
		objs = append(objs, got...)
	}
}

func fromNode(n *yaml.Node, file string) ([]*Object, error) {
	n = deref(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil
	}
	apiVersion, kind := scalar(n, "apiVersion"), scalar(n, "kind")
	if apiVersion == "" || kind == "" {
		return nil, nil
	}
	if _, items := lookup(n, "items"); strings.HasSuffix(kind, "List") && deref(items) != nil {
		items = deref(items)
		if items.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("%s:%d: %s.items must be a sequence", file, items.Line, kind)
		}
		var out []*Object
		for _, item := range items.Content {
			got, err := fromNode(item, file)
			if err != nil {
				return nil, err
			}
			out = append(out, got...)
		}
		return out, nil
	}
	var doc struct {
		Metadata struct {
			Name        string            `yaml:"name"`
			Namespace   string            `yaml:"namespace"`
			Labels      map[string]string `yaml:"labels"`
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"metadata"`
	}
	if err := n.Decode(&doc); err != nil {
		return nil, decodeError(file, n, err)
	}
	m := doc.Metadata
	return []*Object{{
		APIVersion: apiVersion, Kind: kind,
		Name: m.Name, Namespace: m.Namespace, Labels: m.Labels, Annotations: m.Annotations,
		File: file, Node: n,
	}}, nil
}

var syntaxLine = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

// decodeError reports a type mismatch (say, replicas: "three") as
// file:line: Kind name: message, using the line of the first bad field.
func decodeError(file string, n *yaml.Node, err error) error {
	line, msg := strconv.Itoa(n.Line), err.Error()
	var te *yaml.TypeError
	if errors.As(err, &te) && len(te.Errors) > 0 {
		errs := slices.Clone(te.Errors)
		if m := typeErrLine.FindStringSubmatch(errs[0]); m != nil {
			line, errs[0] = m[1], strings.TrimPrefix(errs[0], m[0]+" ")
		}
		msg = strings.Join(errs, "; ")
	}
	what := strings.TrimSpace(scalar(n, "kind") + " " + objectName(n))
	return fmt.Errorf("%s:%s: %s: %s", file, line, what, msg)
}

var typeErrLine = regexp.MustCompile(`^line (\d+):`)

func objectName(n *yaml.Node) string {
	_, meta := lookup(n, "metadata")
	return scalar(deref(meta), "name")
}

// Load reads manifests from paths. "-" means stdin. Directories are walked
// recursively for *.yaml and *.yml files in lexical order; hidden files and
// directories (.git, .k8s-lint.yaml) are skipped.
func Load(paths []string, stdin io.Reader) ([]*Object, error) {
	var objs []*Object
	add := func(r io.Reader, name string) error {
		got, err := Parse(r, name)
		objs = append(objs, got...)
		return err
	}
	for _, p := range paths {
		if p == "-" {
			if err := add(stdin, StdinName); err != nil {
				return nil, err
			}
			continue
		}
		files, err := expand(p)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if err := loadFile(f, add); err != nil {
				return nil, err
			}
		}
	}
	return objs, nil
}

func loadFile(name string, add func(io.Reader, string) error) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return add(f, name)
}

func expand(p string) ([]string, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{p}, nil
	}
	var files []string
	err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		hidden := path != p && strings.HasPrefix(d.Name(), ".")
		if d.IsDir() {
			if hidden {
				return filepath.SkipDir
			}
			return nil
		}
		if ext := filepath.Ext(path); !hidden && (ext == ".yaml" || ext == ".yml") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
