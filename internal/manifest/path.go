package manifest

import (
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Path addresses a node inside an object, for example
// spec.template.spec.containers[0].image. Elements are string mapping keys
// or int sequence indices. Paths are values: Key and Index never modify the
// receiver, so a shared prefix can be extended safely.
type Path []any

// Key returns a copy of p extended with one or more mapping keys.
func (p Path) Key(keys ...string) Path {
	out := make(Path, len(p), len(p)+len(keys))
	copy(out, p)
	for _, k := range keys {
		out = append(out, k)
	}
	return out
}

// Index returns a copy of p extended with a sequence index.
func (p Path) Index(i int) Path {
	out := make(Path, len(p), len(p)+1)
	copy(out, p)
	return append(out, i)
}

// String renders the path in the familiar kubectl/jsonpath-like form.
func (p Path) String() string {
	var b strings.Builder
	for _, e := range p {
		switch v := e.(type) {
		case int:
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(v))
			b.WriteByte(']')
		case string:
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(v)
		}
	}
	return b.String()
}

// Locate walks p from root as far as the document allows and returns the
// position of the deepest node it reached. A finding about a missing field
// (say, resources.limits) therefore points at the closest existing parent.
// For mapping entries the key's position is used, so the result points at
// "image:" rather than at the start of the value.
func Locate(root *yaml.Node, p Path) (line, col int) {
	n := deref(root)
	if n == nil {
		return 0, 0
	}
	line, col = n.Line, n.Column
	for _, e := range p {
		n = deref(n)
		switch v := e.(type) {
		case string:
			key, val := lookup(n, v)
			if val == nil {
				return line, col
			}
			line, col = key.Line, key.Column
			n = val
		case int:
			if n.Kind != yaml.SequenceNode || v < 0 || v >= len(n.Content) {
				return line, col
			}
			n = n.Content[v]
			line, col = n.Line, n.Column
		}
	}
	return line, col
}

// lookup returns the key and value nodes for key in a mapping node.
func lookup(n *yaml.Node, key string) (k, v *yaml.Node) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i], n.Content[i+1]
		}
	}
	return nil, nil
}

// scalar returns the value of a scalar child, or "" when absent.
func scalar(n *yaml.Node, key string) string {
	_, v := lookup(n, key)
	v = deref(v)
	if v == nil || v.Kind != yaml.ScalarNode {
		return ""
	}
	return v.Value
}

func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}
