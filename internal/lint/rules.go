package lint

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/rahulkumar96t/k8s-manifest-lint/internal/manifest"
)

// Registry returns every built-in rule in documentation order.
func Registry() []*Rule {
	return []*Rule{
		{
			ID: "resource-requests", Severity: Warning, Category: "reliability",
			Summary:   "Containers set resource requests (cpu and memory by default).",
			Rationale: "The scheduler places pods by their requests; without them pods land on nodes that cannot sustain them and are evicted first under pressure.",
			Check:     resourceCheck("requests"),
		},
		{
			ID: "resource-limits", Severity: Warning, Category: "reliability",
			Summary:   "Containers set resource limits (cpu and memory by default).",
			Rationale: "A container without a memory limit can exhaust its node and take neighbours down with it.",
			Check:     resourceCheck("limits"),
		},
		{
			ID: "liveness-probe", Severity: Warning, Category: "reliability",
			Summary:   "Long-running containers define a livenessProbe (Jobs and CronJobs are exempt).",
			Rationale: "Without a liveness probe a deadlocked process keeps its pod Running forever.",
			Check:     probeCheck("livenessProbe", func(c *manifest.Container) bool { return c.LivenessProbe != nil }),
		},
		{
			ID: "readiness-probe", Severity: Warning, Category: "reliability",
			Summary:   "Long-running containers define a readinessProbe (Jobs and CronJobs are exempt).",
			Rationale: "Without a readiness probe a pod receives traffic as soon as it starts, before it can serve it.",
			Check:     probeCheck("readinessProbe", func(c *manifest.Container) bool { return c.ReadinessProbe != nil }),
		},
		{
			ID: "image-tag", Severity: Error, Category: "supply-chain",
			Summary:   "Images use an explicit tag other than latest, or a digest.",
			Rationale: "latest and untagged images change under you: two nodes can run different code, and rollbacks do not roll back.",
			Check:     checkImageTag,
		},
		{
			ID: "image-registry", Severity: Error, Category: "supply-chain",
			Summary:   "Images come from a registry in allowedRegistries (inactive until configured).",
			Rationale: "Pulling only from vetted registries keeps unscanned public images out of the cluster.",
			Check:     checkImageRegistry,
		},
		{
			ID: "run-as-non-root", Severity: Warning, Category: "security",
			Summary:   "Containers run as non-root: runAsNonRoot: true or a non-zero runAsUser.",
			Rationale: "A process running as root inside the container is one kernel bug away from root on the node.",
			Check:     checkRunAsNonRoot,
		},
		{
			ID: "privilege-escalation", Severity: Warning, Category: "security",
			Summary:   "Containers set allowPrivilegeEscalation: false.",
			Rationale: "The default allows setuid binaries to gain more privileges than the process started with.",
			Check:     boolCheck("allowPrivilegeEscalation", false, func(s *manifest.SecurityContext) *bool { return s.AllowPrivilegeEscalation }),
		},
		{
			ID: "read-only-root-fs", Severity: Warning, Category: "security",
			Summary:   "Containers set readOnlyRootFilesystem: true.",
			Rationale: "A writable root filesystem lets an attacker drop tools or modify binaries; use emptyDir volumes for scratch space.",
			Check:     boolCheck("readOnlyRootFilesystem", true, func(s *manifest.SecurityContext) *bool { return s.ReadOnlyRootFilesystem }),
		},
		{
			ID: "privileged", Severity: Error, Category: "security",
			Summary:   "Containers do not run privileged.",
			Rationale: "A privileged container has every capability and full device access: it is effectively root on the node.",
			Check:     checkPrivileged,
		},
		{
			ID: "host-namespaces", Severity: Error, Category: "security",
			Summary:   "Pods do not use hostNetwork, hostPID or hostIPC.",
			Rationale: "Sharing host namespaces exposes node processes, ports and IPC to the pod.",
			Check:     checkHostNamespaces,
		},
		{
			ID: "host-path", Severity: Error, Category: "security",
			Summary:   "Pods do not mount hostPath volumes.",
			Rationale: "hostPath mounts give the pod direct access to the node filesystem and tie it to one node.",
			Check:     checkHostPath,
		},
		{
			ID: "required-labels", Severity: Warning, Category: "convention",
			Summary:   "Workloads carry the labels in requiredLabels (default: app.kubernetes.io/name).",
			Rationale: "Consistent labels make ownership, cost allocation and dashboards work without guesswork.",
			Check:     checkRequiredLabels,
		},
		{
			ID: "pdb-required", Severity: Warning, Category: "reliability",
			Summary:   "Workloads with replicas > 1 are covered by a PodDisruptionBudget in the same input.",
			Rationale: "Without a budget a node drain can evict every replica at once, turning routine maintenance into an outage.",
			Check:     checkPDB,
		},
		{
			ID: "service-selects-workload", Severity: Warning, Category: "reliability",
			Summary:   "Service selectors match the pod labels of at least one workload in the same input.",
			Rationale: "A selector that matches nothing is almost always a label typo, and the Service silently has no endpoints.",
			Check:     checkServiceSelector,
		},
	}
}

// describe names a container in messages.
func describe(c manifest.ContainerRef) string {
	if c.Init {
		return fmt.Sprintf("init container %q", c.Name)
	}
	return fmt.Sprintf("container %q", c.Name)
}

func forEachContainer(p *Pass, fn func(*manifest.Workload, manifest.ContainerRef)) {
	for _, w := range p.Index.Workloads {
		for _, c := range w.Containers() {
			fn(w, c)
		}
	}
}

func resourceCheck(field string) func(*Pass) {
	return func(p *Pass) {
		want := p.Config.Resources.Requests
		if field == "limits" {
			want = p.Config.Resources.Limits
		}
		forEachContainer(p, func(w *manifest.Workload, c manifest.ContainerRef) {
			have := c.Resources.Requests
			if field == "limits" {
				have = c.Resources.Limits
			}
			var missing []string
			for _, name := range want {
				if have[name] == nil {
					missing = append(missing, name)
				}
			}
			if len(missing) > 0 {
				p.Report(w.Object, c.Path.Key("resources", field),
					"%s has no resources.%s for %s", describe(c), field, strings.Join(missing, ", "))
			}
		})
	}
}

func probeCheck(field string, has func(*manifest.Container) bool) func(*Pass) {
	return func(p *Pass) {
		forEachContainer(p, func(w *manifest.Workload, c manifest.ContainerRef) {
			if w.IsBatch() || c.Init || has(c.Container) {
				return
			}
			p.Report(w.Object, c.Path.Key(field), "%s has no %s", describe(c), field)
		})
	}
}

func checkImageTag(p *Pass) {
	forEachContainer(p, func(w *manifest.Workload, c manifest.ContainerRef) {
		at := c.Path.Key("image")
		ref := parseImage(c.Image)
		switch {
		case c.Image == "":
			p.Report(w.Object, at, "%s has no image", describe(c))
		case ref.Digest != "":
			// Pinned by digest: the tag, if any, is informational.
		case ref.Tag == "":
			p.Report(w.Object, at, "%s uses untagged image %q, which means latest", describe(c), c.Image)
		case ref.Tag == "latest":
			p.Report(w.Object, at, "%s uses image %q with the mutable latest tag", describe(c), c.Image)
		}
	})
}

func checkImageRegistry(p *Pass) {
	allowed := p.Config.AllowedRegistries
	if len(allowed) == 0 {
		return
	}
	forEachContainer(p, func(w *manifest.Workload, c manifest.ContainerRef) {
		if c.Image == "" {
			return
		}
		if ref := parseImage(c.Image); !registryAllowed(ref, allowed) {
			p.Report(w.Object, c.Path.Key("image"),
				"%s pulls %q from %s, which is not in allowedRegistries", describe(c), c.Image, ref.Registry)
		}
	})
}

func checkRunAsNonRoot(p *Pass) {
	forEachContainer(p, func(w *manifest.Workload, c manifest.ContainerRef) {
		// Container settings override pod settings field by field.
		var nonRoot *bool
		var user *int64
		userAt := c.Path.Key("securityContext", "runAsUser")
		if pod := w.Spec.SecurityContext; pod != nil {
			nonRoot, user = pod.RunAsNonRoot, pod.RunAsUser
			if user != nil {
				userAt = w.SpecPath.Key("securityContext", "runAsUser")
			}
		}
		if sc := c.SecurityContext; sc != nil {
			if sc.RunAsNonRoot != nil {
				nonRoot = sc.RunAsNonRoot
			}
			if sc.RunAsUser != nil {
				user, userAt = sc.RunAsUser, c.Path.Key("securityContext", "runAsUser")
			}
		}
		at := c.Path.Key("securityContext", "runAsNonRoot")
		switch {
		case user != nil && *user == 0:
			p.Report(w.Object, userAt, "%s runs as user 0 (root)", describe(c))
		case nonRoot != nil && *nonRoot, user != nil && *user > 0:
			// Non-root, either enforced by the kubelet or by a fixed UID.
		case nonRoot != nil:
			p.Report(w.Object, at, "%s sets runAsNonRoot: false", describe(c))
		default:
			p.Report(w.Object, at, "%s does not set runAsNonRoot: true or a non-zero runAsUser", describe(c))
		}
	})
}

// boolCheck reports containers whose securityContext field is not want.
func boolCheck(field string, want bool, get func(*manifest.SecurityContext) *bool) func(*Pass) {
	return func(p *Pass) {
		forEachContainer(p, func(w *manifest.Workload, c manifest.ContainerRef) {
			var v *bool
			if c.SecurityContext != nil {
				v = get(c.SecurityContext)
			}
			at := c.Path.Key("securityContext", field)
			switch {
			case v == nil:
				p.Report(w.Object, at, "%s does not set %s: %t", describe(c), field, want)
			case *v != want:
				p.Report(w.Object, at, "%s sets %s: %t", describe(c), field, *v)
			}
		})
	}
}

func checkPrivileged(p *Pass) {
	forEachContainer(p, func(w *manifest.Workload, c manifest.ContainerRef) {
		if sc := c.SecurityContext; sc != nil && sc.Privileged != nil && *sc.Privileged {
			p.Report(w.Object, c.Path.Key("securityContext", "privileged"), "%s runs privileged", describe(c))
		}
	})
}

func checkHostNamespaces(p *Pass) {
	for _, w := range p.Index.Workloads {
		for _, ns := range []struct {
			field string
			on    bool
		}{{"hostNetwork", w.Spec.HostNetwork}, {"hostPID", w.Spec.HostPID}, {"hostIPC", w.Spec.HostIPC}} {
			if ns.on {
				p.Report(w.Object, w.SpecPath.Key(ns.field), "pod shares a host namespace (%s: true)", ns.field)
			}
		}
	}
}

func checkHostPath(p *Pass) {
	for _, w := range p.Index.Workloads {
		for i, v := range w.Spec.Volumes {
			if v.HostPath != nil {
				p.Report(w.Object, w.SpecPath.Key("volumes").Index(i).Key("hostPath"),
					"volume %q mounts host path %q", v.Name, v.HostPath.Path)
			}
		}
	}
}

func checkRequiredLabels(p *Pass) {
	for _, w := range p.Index.Workloads {
		var missing []string
		for _, l := range p.Config.RequiredLabels {
			if _, ok := w.Labels[l]; !ok {
				missing = append(missing, l)
			}
		}
		if len(missing) > 0 {
			p.Report(w.Object, manifest.Path{"metadata", "labels"}, "missing required labels: %s", strings.Join(missing, ", "))
		}
	}
}

func checkPDB(p *Pass) {
	for _, w := range p.Index.Workloads {
		if w.Replicas == nil || *w.Replicas <= 1 {
			continue
		}
		covered := slices.ContainsFunc(p.Index.PDBs, func(pdb *manifest.PDB) bool {
			return manifest.SameNamespace(pdb.Object, w.Object) && pdb.Matches(w.PodLabels)
		})
		if !covered {
			p.Report(w.Object, manifest.Path{"spec", "replicas"},
				"%d replicas but no PodDisruptionBudget in the input selects its pods", *w.Replicas)
		}
	}
}

func checkServiceSelector(p *Pass) {
	for _, s := range p.Index.Services {
		if len(s.Selector) == 0 {
			continue // ExternalName or manually managed endpoints
		}
		matched := slices.ContainsFunc(p.Index.Workloads, func(w *manifest.Workload) bool {
			return manifest.SameNamespace(s.Object, w.Object) && manifest.MatchesLabels(s.Selector, w.PodLabels)
		})
		if !matched {
			p.Report(s.Object, manifest.Path{"spec", "selector"},
				"selector %s matches no workload in the input", formatLabels(s.Selector))
		}
	}
}

func formatLabels(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
