package lint

import (
	"strings"
	"testing"

	"github.com/rahulkumar96t/k8s-manifest-lint/internal/config"
)

type ruleCase struct {
	name  string
	edits []edit
	cfg   func(*config.Config)
	extra []string // additional documents in the same input
	want  int      // findings for the rule under test
	msg   string   // substring every finding message must contain
}

func runRuleCases(t *testing.T, rule string, cases []ruleCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			if tc.cfg != nil {
				tc.cfg(cfg)
			}
			docs := append([]string{doc(t, cleanDeployment, tc.edits...)}, tc.extra...)
			got := byRule(run(t, cfg, docs...), rule)
			if len(got) != tc.want {
				t.Fatalf("%s: got %d findings, want %d: %+v", rule, len(got), tc.want, got)
			}
			for _, f := range got {
				if !strings.Contains(f.Message, tc.msg) {
					t.Errorf("message %q does not contain %q", f.Message, tc.msg)
				}
				if f.Line == 0 {
					t.Errorf("finding has no line: %+v", f)
				}
			}
		})
	}
}

func TestCleanDeploymentPassesEveryRule(t *testing.T) {
	if res := run(t, nil, cleanDeployment); len(res.Findings) != 0 {
		t.Fatalf("clean fixture has findings: %+v", res.Findings)
	}
}

func TestResourceRequests(t *testing.T) {
	runRuleCases(t, "resource-requests", []ruleCase{
		{name: "missing requests", edits: []edit{del(c0 + ".resources.requests")}, want: 1, msg: "resources.requests for cpu, memory"},
		{name: "missing cpu only", edits: []edit{del(c0 + ".resources.requests.cpu")}, want: 1, msg: "for cpu"},
		{name: "integer quantity counts", edits: []edit{set(c0+".resources.requests.cpu", 1)}},
		{name: "config requires memory only", edits: []edit{del(c0 + ".resources.requests.cpu")},
			cfg: func(c *config.Config) { c.Resources.Requests = []string{"memory"} }},
		{name: "init containers are checked", edits: []edit{set(pod+".initContainers", []any{map[string]any{"name": "init", "image": "busybox:1.36"}})},
			want: 1, msg: `init container "init"`},
	})
}

func TestResourceLimits(t *testing.T) {
	runRuleCases(t, "resource-limits", []ruleCase{
		{name: "missing limits", edits: []edit{del(c0 + ".resources.limits")}, want: 1, msg: "resources.limits for cpu, memory"},
		{name: "missing cpu limit", edits: []edit{del(c0 + ".resources.limits.cpu")}, want: 1, msg: "for cpu"},
		{name: "cpu limits relaxed by config", edits: []edit{del(c0 + ".resources.limits.cpu")},
			cfg: func(c *config.Config) { c.Resources.Limits = []string{"memory"} }},
		{name: "empty list disables the check", edits: []edit{del(c0 + ".resources")},
			cfg: func(c *config.Config) { c.Resources.Limits = []string{} }},
	})
}

func TestProbes(t *testing.T) {
	for _, rule := range []string{"liveness-probe", "readiness-probe"} {
		field := map[string]string{"liveness-probe": "livenessProbe", "readiness-probe": "readinessProbe"}[rule]
		runRuleCases(t, rule, []ruleCase{
			{name: "missing", edits: []edit{del(c0 + "." + field)}, want: 1, msg: "has no " + field},
			{name: "Job is exempt", edits: []edit{set("apiVersion", "batch/v1"), set("kind", "Job"), del(c0 + "." + field)}},
			{name: "bare Pod is checked", edits: []edit{
				set("apiVersion", "v1"), set("kind", "Pod"),
				set("spec", map[string]any{"containers": []any{map[string]any{"name": "p", "image": "registry.example.com/p:1"}}}),
			}, want: 1},
			{name: "init containers are exempt", edits: []edit{set(pod+".initContainers", []any{map[string]any{"name": "init", "image": "x:1"}})}},
		})
	}
}

func TestProbesExemptForCronJob(t *testing.T) {
	cron := `
apiVersion: batch/v1
kind: CronJob
metadata: {name: report, labels: {app.kubernetes.io/name: report}}
spec:
  schedule: "0 2 * * *"
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - name: report
              image: registry.example.com/report:1.0
`
	res := run(t, nil, cron)
	if n := len(byRule(res, "liveness-probe")) + len(byRule(res, "readiness-probe")); n != 0 {
		t.Fatalf("CronJob probe findings = %d, want 0", n)
	}
	// The CronJob's containers are still checked by the other rules.
	if len(byRule(res, "resource-requests")) != 1 {
		t.Errorf("CronJob containers were not checked: %+v", res.Findings)
	}
}

func TestImageTag(t *testing.T) {
	image := func(s string) []edit { return []edit{set(c0+".image", s)} }
	runRuleCases(t, "image-tag", []ruleCase{
		{name: "latest", edits: image("nginx:latest"), want: 1, msg: "mutable latest tag"},
		{name: "untagged", edits: image("nginx"), want: 1, msg: "untagged"},
		{name: "registry port is not a tag", edits: image("localhost:5000/web"), want: 1, msg: "untagged"},
		{name: "registry port with tag", edits: image("registry.example.com:5000/team/web:1.2.3")},
		{name: "digest", edits: image("nginx@sha256:0f4c3a6b2d1e9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1f0a9b8c7d6e5f4a")},
		{name: "digest wins over latest", edits: image("nginx:latest@sha256:0f4c3a6b2d1e9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1f0a9b8c7d6e5f4a")},
		{name: "tag named like latest", edits: image("nginx:latest-alpine")},
		{name: "empty image", edits: image(""), want: 1, msg: "has no image"},
	})
}

func TestImageRegistry(t *testing.T) {
	allow := func(entries ...string) func(*config.Config) {
		return func(c *config.Config) { c.AllowedRegistries = entries }
	}
	image := func(s string) []edit { return []edit{set(c0+".image", s)} }
	runRuleCases(t, "image-registry", []ruleCase{
		{name: "inactive without config", edits: image("nginx:1.25")},
		{name: "allowed host", cfg: allow("registry.example.com")},
		{name: "docker hub not allowed", edits: image("nginx:1.25"), cfg: allow("registry.example.com"), want: 1, msg: "from docker.io"},
		{name: "docker hub official images", edits: image("nginx:1.25"), cfg: allow("docker.io/library")},
		{name: "index.docker.io is docker.io", edits: image("index.docker.io/library/nginx:1.25"), cfg: allow("docker.io")},
		{name: "ECR glob", edits: image("111111111111.dkr.ecr.eu-west-1.amazonaws.com/app:1"), cfg: allow("*.dkr.ecr.*.amazonaws.com")},
		{name: "glob does not cross segments", edits: image("evil.example.net/111111111111.dkr.ecr.x.amazonaws.com/app:1"),
			cfg: allow("*.dkr.ecr.*.amazonaws.com"), want: 1},
		{name: "path prefix", edits: image("artifactory.example.com/docker-local/team/app:1"), cfg: allow("artifactory.example.com/docker-local")},
		{name: "path prefix respects segment boundary", edits: image("artifactory.example.com/docker-local-evil/app:1"),
			cfg: allow("artifactory.example.com/docker-local"), want: 1},
		{name: "trailing slash in entry", edits: image("artifactory.example.com/docker-local/app:1"), cfg: allow("artifactory.example.com/docker-local/")},
	})
}

func TestParseImage(t *testing.T) {
	cases := map[string]imageRef{
		"nginx":                           {Registry: "docker.io", Repository: "library/nginx"},
		"bitnami/redis:7":                 {Registry: "docker.io", Repository: "bitnami/redis", Tag: "7"},
		"localhost/app":                   {Registry: "localhost", Repository: "app"},
		"registry.example.com:5000/a/b:1": {Registry: "registry.example.com:5000", Repository: "a/b", Tag: "1"},
		"ghcr.io/org/app@sha256:abc":      {Registry: "ghcr.io", Repository: "org/app", Digest: "sha256:abc"},
		"app:1@sha256:abc":                {Registry: "docker.io", Repository: "library/app", Tag: "1", Digest: "sha256:abc"},
	}
	for in, want := range cases {
		if got := parseImage(in); got != want {
			t.Errorf("parseImage(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestRunAsNonRoot(t *testing.T) {
	sc := c0 + ".securityContext"
	psc := pod + ".securityContext"
	runRuleCases(t, "run-as-non-root", []ruleCase{
		{name: "unset", edits: []edit{del(sc + ".runAsNonRoot")}, want: 1, msg: "does not set runAsNonRoot"},
		{name: "explicit false", edits: []edit{set(sc+".runAsNonRoot", false)}, want: 1, msg: "runAsNonRoot: false"},
		{name: "inherited from pod", edits: []edit{del(sc + ".runAsNonRoot"), set(psc+".runAsNonRoot", true)}},
		{name: "non-zero uid on pod", edits: []edit{del(sc + ".runAsNonRoot"), set(psc+".runAsUser", 10001)}},
		{name: "root uid on pod beats runAsNonRoot", edits: []edit{set(psc+".runAsUser", 0)}, want: 1, msg: "user 0"},
		{name: "container overrides pod runAsNonRoot", edits: []edit{set(psc+".runAsNonRoot", true), set(sc+".runAsNonRoot", false)}, want: 1},
		{name: "container uid overrides pod uid", edits: []edit{del(sc + ".runAsNonRoot"), set(psc+".runAsUser", 1000), set(sc+".runAsUser", 0)}, want: 1, msg: "user 0"},
		{name: "container uid fixes root pod uid", edits: []edit{set(psc+".runAsUser", 0), set(sc+".runAsUser", 1000)}},
	})
}

func TestRootUserFindingPointsAtTheSetting(t *testing.T) {
	src := doc(t, cleanDeployment, set(pod+".securityContext.runAsUser", 0))
	f := byRule(run(t, nil, src), "run-as-non-root")
	if len(f) != 1 || f[0].Path.String() != pod+".securityContext.runAsUser" {
		t.Fatalf("findings = %+v", f)
	}
}

func TestPrivilegeEscalationAndReadOnlyRootFS(t *testing.T) {
	sc := c0 + ".securityContext"
	runRuleCases(t, "privilege-escalation", []ruleCase{
		{name: "unset", edits: []edit{del(sc + ".allowPrivilegeEscalation")}, want: 1, msg: "does not set allowPrivilegeEscalation: false"},
		{name: "true", edits: []edit{set(sc+".allowPrivilegeEscalation", true)}, want: 1, msg: "sets allowPrivilegeEscalation: true"},
		{name: "no securityContext", edits: []edit{del(sc)}, want: 1},
	})
	runRuleCases(t, "read-only-root-fs", []ruleCase{
		{name: "unset", edits: []edit{del(sc + ".readOnlyRootFilesystem")}, want: 1, msg: "does not set readOnlyRootFilesystem: true"},
		{name: "false", edits: []edit{set(sc+".readOnlyRootFilesystem", false)}, want: 1, msg: "sets readOnlyRootFilesystem: false"},
	})
}

func TestPrivilegedAndHostAccess(t *testing.T) {
	runRuleCases(t, "privileged", []ruleCase{
		{name: "privileged", edits: []edit{set(c0+".securityContext.privileged", true)}, want: 1, msg: "runs privileged"},
		{name: "explicitly not privileged", edits: []edit{set(c0+".securityContext.privileged", false)}},
	})
	runRuleCases(t, "host-namespaces", []ruleCase{
		{name: "hostNetwork", edits: []edit{set(pod+".hostNetwork", true)}, want: 1, msg: "hostNetwork: true"},
		{name: "hostPID and hostIPC", edits: []edit{set(pod+".hostPID", true), set(pod+".hostIPC", true)}, want: 2, msg: "host namespace"},
		{name: "explicit false", edits: []edit{set(pod+".hostNetwork", false)}},
	})
	runRuleCases(t, "host-path", []ruleCase{
		{name: "hostPath volume", edits: []edit{set(pod+".volumes", []any{
			map[string]any{"name": "tmp", "emptyDir": map[string]any{}},
			map[string]any{"name": "sock", "hostPath": map[string]any{"path": "/var/run/docker.sock"}},
		})}, want: 1, msg: `volume "sock" mounts host path "/var/run/docker.sock"`},
		{name: "emptyDir only", edits: []edit{set(pod+".volumes", []any{map[string]any{"name": "tmp", "emptyDir": map[string]any{}}})}},
	})
}

func TestRequiredLabels(t *testing.T) {
	runRuleCases(t, "required-labels", []ruleCase{
		{name: "no labels", edits: []edit{del("metadata.labels")}, want: 1, msg: "app.kubernetes.io/name"},
		{name: "configured list", cfg: func(c *config.Config) { c.RequiredLabels = []string{"app.kubernetes.io/name", "team", "cost-center"} },
			want: 1, msg: "missing required labels: team, cost-center"},
		{name: "empty list disables", edits: []edit{del("metadata.labels")}, cfg: func(c *config.Config) { c.RequiredLabels = []string{} }},
		{name: "pod template labels do not count", edits: []edit{del("metadata.labels"),
			set("spec.template.metadata.labels", map[string]any{"app.kubernetes.io/name": "web"})}, want: 1},
	})
}

const pdbFor = `
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata: {name: web}
spec: {maxUnavailable: 1, selector: {matchLabels: {app: web}}}
`

func TestPDBRequired(t *testing.T) {
	three := set("spec.replicas", 3)
	runRuleCases(t, "pdb-required", []ruleCase{
		{name: "three replicas, no PDB", edits: []edit{three}, want: 1, msg: "3 replicas but no PodDisruptionBudget"},
		{name: "one replica", edits: []edit{set("spec.replicas", 1)}},
		{name: "replicas unset defaults to one", edits: []edit{del("spec.replicas")}},
		{name: "matching PDB", edits: []edit{three}, extra: []string{pdbFor}},
		{name: "PDB selects other pods", edits: []edit{three}, want: 1,
			extra: []string{strings.Replace(pdbFor, "app: web", "app: api", 1)}},
		{name: "PDB in another namespace", edits: []edit{three, set("metadata.namespace", "a")}, want: 1,
			extra: []string{strings.Replace(pdbFor, "{name: web}", "{name: web, namespace: b}", 1)}},
		{name: "PDB without namespace", edits: []edit{three, set("metadata.namespace", "a")}, extra: []string{pdbFor}},
		{name: "PDB with matchExpressions", edits: []edit{three}, extra: []string{
			strings.Replace(pdbFor, "{matchLabels: {app: web}}", "{matchExpressions: [{key: app, operator: In, values: [web, api]}]}", 1)}},
		{name: "policy/v1 empty selector covers all", edits: []edit{three}, extra: []string{strings.Replace(pdbFor, "{matchLabels: {app: web}}", "{}", 1)}},
		{name: "policy/v1beta1 empty selector covers none", edits: []edit{three}, want: 1, extra: []string{
			strings.Replace(strings.Replace(pdbFor, "{matchLabels: {app: web}}", "{}", 1), "policy/v1", "policy/v1beta1", 1)}},
		{name: "DaemonSet has no replicas", edits: []edit{three, set("kind", "DaemonSet")}},
		{name: "StatefulSet is checked", edits: []edit{three, set("kind", "StatefulSet")}, want: 1},
	})
}

const serviceFor = `
apiVersion: v1
kind: Service
metadata: {name: web}
spec: {selector: {app: web}, ports: [{port: 80}]}
`

func TestServiceSelectsWorkload(t *testing.T) {
	runRuleCases(t, "service-selects-workload", []ruleCase{
		{name: "matches", extra: []string{serviceFor}},
		{name: "typo", extra: []string{strings.Replace(serviceFor, "app: web", "app: wbe", 1)}, want: 1, msg: "selector app=wbe matches no workload"},
		{name: "selector must be a subset of pod labels", extra: []string{strings.Replace(serviceFor, "{app: web}", "{app: web, tier: front}", 1)}, want: 1},
		{name: "no selector", extra: []string{"apiVersion: v1\nkind: Service\nmetadata: {name: db}\nspec: {type: ExternalName, externalName: db.example.com}\n"}},
		{name: "other namespace", edits: []edit{set("metadata.namespace", "a")}, want: 1,
			extra: []string{strings.Replace(serviceFor, "{name: web}", "{name: web, namespace: b}", 1)}},
		{name: "non-core Service kind is ignored", extra: []string{strings.Replace(serviceFor, "apiVersion: v1", "apiVersion: serving.knative.dev/v1", 1)}},
		{name: "selects a bare Pod by its labels", extra: []string{
			strings.Replace(serviceFor, "app: web", "app: standalone", 1),
			"apiVersion: v1\nkind: Pod\nmetadata: {name: p, labels: {app: standalone}}\nspec: {containers: []}\n",
		}},
	})
}
