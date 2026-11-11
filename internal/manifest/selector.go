package manifest

import "slices"

// LabelSelector mirrors metav1.LabelSelector.
type LabelSelector struct {
	MatchLabels      map[string]string `yaml:"matchLabels"`
	MatchExpressions []Requirement     `yaml:"matchExpressions"`
}

// Requirement is one matchExpressions entry.
type Requirement struct {
	Key      string   `yaml:"key"`
	Operator string   `yaml:"operator"`
	Values   []string `yaml:"values"`
}

// Empty reports whether the selector has no terms at all.
func (s *LabelSelector) Empty() bool {
	return len(s.MatchLabels) == 0 && len(s.MatchExpressions) == 0
}

// Matches applies the selector to a label set. A nil selector matches
// nothing; an empty one matches everything. Unknown operators never match,
// which errs on the side of reporting a missing budget.
func (s *LabelSelector) Matches(labels map[string]string) bool {
	if s == nil {
		return false
	}
	if !MatchesLabels(s.MatchLabels, labels) {
		return false
	}
	for _, r := range s.MatchExpressions {
		v, has := labels[r.Key]
		var ok bool
		switch r.Operator {
		case "In":
			ok = has && slices.Contains(r.Values, v)
		case "NotIn":
			ok = !has || !slices.Contains(r.Values, v)
		case "Exists":
			ok = has
		case "DoesNotExist":
			ok = !has
		}
		if !ok {
			return false
		}
	}
	return true
}

// MatchesLabels reports whether every key/value in selector is present in
// labels (equality-based selection, as used by Services).
func MatchesLabels(selector, labels map[string]string) bool {
	for k, v := range selector {
		if got, ok := labels[k]; !ok || got != v {
			return false
		}
	}
	return true
}
