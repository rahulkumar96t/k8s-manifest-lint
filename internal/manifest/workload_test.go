package manifest

import (
	"strings"
	"testing"
)

func TestAsWorkloadKinds(t *testing.T) {
	template := `
  template:
    metadata: {labels: {app: x}}
    spec:
      initContainers: [{name: init, image: busybox:1}]
      containers: [{name: main, image: app:1}]
`
	cases := []struct {
		kind, apiVersion, spec string
		specPath               string
		replicas               int32 // -1 = nil
	}{
		{"Deployment", "apps/v1", "spec:\n  replicas: 3" + template, "spec.template.spec", 3},
		{"Deployment", "apps/v1", "spec:" + template, "spec.template.spec", -1},
		{"StatefulSet", "apps/v1", "spec:\n  replicas: 2" + template, "spec.template.spec", 2},
		{"ReplicaSet", "apps/v1", "spec:\n  replicas: 2" + template, "spec.template.spec", 2},
		{"DaemonSet", "apps/v1", "spec:" + template, "spec.template.spec", -1},
		{"Job", "batch/v1", "spec:\n  parallelism: 4" + template, "spec.template.spec", -1},
		{"CronJob", "batch/v1", "spec:\n  schedule: '* * * * *'\n  jobTemplate:\n    spec:\n" + indent(template, "    "), "spec.jobTemplate.spec.template.spec", -1},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			src := "apiVersion: " + tc.apiVersion + "\nkind: " + tc.kind + "\nmetadata: {name: w}\n" + tc.spec
			w, err := AsWorkload(mustParse(t, src)[0])
			if err != nil || w == nil {
				t.Fatalf("AsWorkload = %v, %v", w, err)
			}
			if w.SpecPath.String() != tc.specPath {
				t.Errorf("SpecPath = %s, want %s", w.SpecPath, tc.specPath)
			}
			if w.PodLabels["app"] != "x" {
				t.Errorf("PodLabels = %v", w.PodLabels)
			}
			if tc.replicas < 0 && w.Replicas != nil || tc.replicas >= 0 && (w.Replicas == nil || *w.Replicas != tc.replicas) {
				t.Errorf("Replicas = %v, want %d", w.Replicas, tc.replicas)
			}
			cs := w.Containers()
			if len(cs) != 2 || !cs[0].Init || cs[0].Name != "init" || cs[1].Init || cs[1].Name != "main" {
				t.Fatalf("Containers = %+v", cs)
			}
			if got := cs[1].Path.String(); got != tc.specPath+".containers[0]" {
				t.Errorf("container path = %s", got)
			}
			if line, _ := w.Locate(cs[1].Path.Key("image")); line == 0 {
				t.Error("container image not locatable")
			}
		})
	}
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}

func TestAsWorkloadPodAndNonWorkloads(t *testing.T) {
	pod := mustParse(t, "apiVersion: v1\nkind: Pod\nmetadata: {name: p, labels: {app: p}}\nspec:\n  hostNetwork: true\n  containers: [{name: c, image: x:1}]\n")[0]
	w, err := AsWorkload(pod)
	if err != nil || w == nil {
		t.Fatalf("pod: %v %v", w, err)
	}
	if w.SpecPath.String() != "spec" || w.PodLabels["app"] != "p" || !w.Spec.HostNetwork || w.IsBatch() {
		t.Errorf("pod workload = %+v", w)
	}

	for _, src := range []string{
		"apiVersion: v1\nkind: ConfigMap\nmetadata: {name: c}\n",
		// A custom resource that reuses a built-in kind name is not a workload.
		"apiVersion: example.com/v1\nkind: Deployment\nmetadata: {name: d}\nspec: {template: {spec: {containers: [{name: c}]}}}\n",
	} {
		if w, err := AsWorkload(mustParse(t, src)[0]); w != nil || err != nil {
			t.Errorf("AsWorkload(%q) = %v, %v; want nil", src, w, err)
		}
	}
}

func TestBuildIndex(t *testing.T) {
	src := `apiVersion: apps/v1
kind: Deployment
metadata: {name: web}
spec: {template: {metadata: {labels: {app: web}}, spec: {containers: [{name: c, image: x:1}]}}}
---
apiVersion: v1
kind: Service
metadata: {name: web}
spec: {selector: {app: web}}
---
apiVersion: serving.knative.dev/v1
kind: Service
metadata: {name: not-core}
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata: {name: web}
spec: {selector: {matchLabels: {app: web}}}
`
	idx, err := BuildIndex(mustParse(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Objects) != 4 || len(idx.Workloads) != 1 || len(idx.Services) != 1 || len(idx.PDBs) != 1 {
		t.Fatalf("index sizes: objects=%d workloads=%d services=%d pdbs=%d",
			len(idx.Objects), len(idx.Workloads), len(idx.Services), len(idx.PDBs))
	}
	if idx.Services[0].Selector["app"] != "web" || !idx.PDBs[0].Matches(map[string]string{"app": "web"}) {
		t.Error("service selector or PDB selector not decoded")
	}

	_, err = BuildIndex(mustParse(t, "apiVersion: v1\nkind: Service\nmetadata: {name: s}\nspec: {selector: [a]}\n"))
	if err == nil {
		t.Error("malformed Service selector: want error")
	}
}
