package manifest

import "testing"

func TestLabelSelector(t *testing.T) {
	pod := map[string]string{"app": "web", "tier": "front"}
	cases := []struct {
		name string
		sel  *LabelSelector
		want bool
	}{
		{"nil matches nothing", nil, false},
		{"empty matches everything", &LabelSelector{}, true},
		{"matchLabels subset", &LabelSelector{MatchLabels: map[string]string{"app": "web"}}, true},
		{"matchLabels wrong value", &LabelSelector{MatchLabels: map[string]string{"app": "api"}}, false},
		{"matchLabels missing key", &LabelSelector{MatchLabels: map[string]string{"team": "a"}}, false},
		{"In", sel(Requirement{"app", "In", []string{"api", "web"}}), true},
		{"In miss", sel(Requirement{"app", "In", []string{"api"}}), false},
		{"NotIn", sel(Requirement{"app", "NotIn", []string{"api"}}), true},
		{"NotIn hit", sel(Requirement{"app", "NotIn", []string{"web"}}), false},
		{"NotIn absent key", sel(Requirement{"team", "NotIn", []string{"a"}}), true},
		{"Exists", sel(Requirement{Key: "tier", Operator: "Exists"}), true},
		{"Exists miss", sel(Requirement{Key: "team", Operator: "Exists"}), false},
		{"DoesNotExist", sel(Requirement{Key: "team", Operator: "DoesNotExist"}), true},
		{"DoesNotExist hit", sel(Requirement{Key: "app", Operator: "DoesNotExist"}), false},
		{"unknown operator", sel(Requirement{Key: "app", Operator: "Gt"}), false},
		{"labels and expressions are ANDed", &LabelSelector{
			MatchLabels:      map[string]string{"app": "web"},
			MatchExpressions: []Requirement{{Key: "tier", Operator: "In", Values: []string{"back"}}},
		}, false},
	}
	for _, tc := range cases {
		if got := tc.sel.Matches(pod); got != tc.want {
			t.Errorf("%s: Matches = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func sel(r Requirement) *LabelSelector { return &LabelSelector{MatchExpressions: []Requirement{r}} }

func TestPDBEmptySelectorDependsOnAPIVersion(t *testing.T) {
	pod := map[string]string{"app": "web"}
	v1 := &PDB{Object: &Object{APIVersion: "policy/v1"}, Selector: &LabelSelector{}}
	beta := &PDB{Object: &Object{APIVersion: "policy/v1beta1"}, Selector: &LabelSelector{}}
	none := &PDB{Object: &Object{APIVersion: "policy/v1"}}
	if !v1.Matches(pod) || beta.Matches(pod) || none.Matches(pod) {
		t.Errorf("v1=%v v1beta1=%v nil=%v; want true false false", v1.Matches(pod), beta.Matches(pod), none.Matches(pod))
	}
}

func TestSameNamespace(t *testing.T) {
	ns := func(n string) *Object { return &Object{Namespace: n} }
	cases := []struct {
		a, b string
		want bool
	}{{"a", "a", true}, {"a", "b", false}, {"", "b", true}, {"a", "", true}, {"", "", true}}
	for _, tc := range cases {
		if got := SameNamespace(ns(tc.a), ns(tc.b)); got != tc.want {
			t.Errorf("SameNamespace(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}
