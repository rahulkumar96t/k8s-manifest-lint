package manifest

// Index groups the parsed input by the roles objects play in cross-object
// rules. It is built once and shared, read-only, by every rule.
type Index struct {
	Objects   []*Object
	Workloads []*Workload
	Services  []*Service
	PDBs      []*PDB
}

// Service is a core/v1 Service; only its selector matters here.
type Service struct {
	*Object
	Selector map[string]string
}

// PDB is a PodDisruptionBudget.
type PDB struct {
	*Object
	Selector *LabelSelector
}

// Matches reports whether the budget covers pods with the given labels.
// policy/v1 treats an empty selector as "every pod in the namespace";
// the older policy/v1beta1 treated it as "no pods".
func (p *PDB) Matches(labels map[string]string) bool {
	if p.Selector == nil {
		return false
	}
	if p.Selector.Empty() && p.APIVersion == "policy/v1beta1" {
		return false
	}
	return p.Selector.Matches(labels)
}

// BuildIndex classifies objects and decodes the kinds rules care about.
func BuildIndex(objs []*Object) (*Index, error) {
	idx := &Index{Objects: objs}
	for _, o := range objs {
		w, err := AsWorkload(o)
		if err != nil {
			return nil, err
		}
		if w != nil {
			idx.Workloads = append(idx.Workloads, w)
			continue
		}
		switch {
		case isKind(o, "Service", ""):
			var doc struct {
				Spec struct {
					Selector map[string]string `yaml:"selector"`
				} `yaml:"spec"`
			}
			if err := o.Node.Decode(&doc); err != nil {
				return nil, decodeError(o.File, o.Node, err)
			}
			idx.Services = append(idx.Services, &Service{Object: o, Selector: doc.Spec.Selector})
		case isKind(o, "PodDisruptionBudget", "policy"):
			var doc struct {
				Spec struct {
					Selector *LabelSelector `yaml:"selector"`
				} `yaml:"spec"`
			}
			if err := o.Node.Decode(&doc); err != nil {
				return nil, decodeError(o.File, o.Node, err)
			}
			idx.PDBs = append(idx.PDBs, &PDB{Object: o, Selector: doc.Spec.Selector})
		}
	}
	return idx, nil
}

// SameNamespace reports whether two objects would land in the same
// namespace. Rendered charts often omit metadata.namespace and rely on
// `kubectl apply -n`, so an empty namespace matches any namespace.
func SameNamespace(a, b *Object) bool {
	return a.Namespace == "" || b.Namespace == "" || a.Namespace == b.Namespace
}
