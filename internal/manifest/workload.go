package manifest

// The structs below are deliberately minimal: they decode only the fields
// the rules look at. Everything else in the manifest is ignored, which keeps
// the tool independent of Kubernetes API versions and of client-go.

// PodSpec is the subset of core/v1 PodSpec the rules need.
type PodSpec struct {
	Containers      []Container         `yaml:"containers"`
	InitContainers  []Container         `yaml:"initContainers"`
	HostNetwork     bool                `yaml:"hostNetwork"`
	HostPID         bool                `yaml:"hostPID"`
	HostIPC         bool                `yaml:"hostIPC"`
	SecurityContext *PodSecurityContext `yaml:"securityContext"`
	Volumes         []Volume            `yaml:"volumes"`
}

// PodSecurityContext holds pod-level defaults that containers inherit.
type PodSecurityContext struct {
	RunAsNonRoot *bool  `yaml:"runAsNonRoot"`
	RunAsUser    *int64 `yaml:"runAsUser"`
}

// Container is the subset of core/v1 Container the rules need.
type Container struct {
	Name            string           `yaml:"name"`
	Image           string           `yaml:"image"`
	Resources       Resources        `yaml:"resources"`
	LivenessProbe   *Probe           `yaml:"livenessProbe"`
	ReadinessProbe  *Probe           `yaml:"readinessProbe"`
	SecurityContext *SecurityContext `yaml:"securityContext"`
}

// Resources keeps quantities untyped: only presence matters to the rules.
type Resources struct {
	Requests map[string]any `yaml:"requests"`
	Limits   map[string]any `yaml:"limits"`
}

// Probe is only checked for presence.
type Probe struct{}

// SecurityContext is the container-level security context.
type SecurityContext struct {
	RunAsNonRoot             *bool  `yaml:"runAsNonRoot"`
	RunAsUser                *int64 `yaml:"runAsUser"`
	AllowPrivilegeEscalation *bool  `yaml:"allowPrivilegeEscalation"`
	ReadOnlyRootFilesystem   *bool  `yaml:"readOnlyRootFilesystem"`
	Privileged               *bool  `yaml:"privileged"`
}

// Volume records only whether a volume is a hostPath.
type Volume struct {
	Name     string `yaml:"name"`
	HostPath *struct {
		Path string `yaml:"path"`
	} `yaml:"hostPath"`
}

// Workload is an object that owns a pod template (or is a Pod).
type Workload struct {
	*Object
	Spec PodSpec
	// SpecPath points at the pod spec inside the object, e.g.
	// spec.jobTemplate.spec.template.spec for a CronJob.
	SpecPath Path
	// PodLabels are the labels the pods will carry: the template labels, or
	// the object's own labels for a bare Pod.
	PodLabels map[string]string
	// Replicas is the declared replica count; nil when the kind has no
	// replicas field or it is unset (the API server then defaults it to 1).
	Replicas *int32
}

// IsBatch reports whether the workload runs to completion (Job, CronJob).
func (w *Workload) IsBatch() bool { return w.Kind == "Job" || w.Kind == "CronJob" }

// ContainerRef is a container together with its location in the object.
type ContainerRef struct {
	*Container
	Path Path
	Init bool
}

// Containers returns init containers followed by regular containers.
func (w *Workload) Containers() []ContainerRef {
	out := make([]ContainerRef, 0, len(w.Spec.InitContainers)+len(w.Spec.Containers))
	for i := range w.Spec.InitContainers {
		out = append(out, ContainerRef{&w.Spec.InitContainers[i], w.SpecPath.Key("initContainers").Index(i), true})
	}
	for i := range w.Spec.Containers {
		out = append(out, ContainerRef{&w.Spec.Containers[i], w.SpecPath.Key("containers").Index(i), false})
	}
	return out
}

type podTemplate struct {
	Metadata struct {
		Labels map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec PodSpec `yaml:"spec"`
}

// workloadKinds maps kind to the API groups it is accepted from, so that a
// custom resource that happens to be called "Deployment" is not misread.
var workloadKinds = map[string][]string{
	"Pod":         {""},
	"Deployment":  {"apps", "extensions"},
	"StatefulSet": {"apps"},
	"DaemonSet":   {"apps", "extensions"},
	"ReplicaSet":  {"apps", "extensions"},
	"Job":         {"batch"},
	"CronJob":     {"batch"},
}

func isKind(o *Object, kind string, groups ...string) bool {
	if o.Kind != kind {
		return false
	}
	for _, g := range groups {
		if o.Group() == g {
			return true
		}
	}
	return false
}

// AsWorkload decodes o as a workload. It returns nil, nil for other kinds.
func AsWorkload(o *Object) (*Workload, error) {
	groups, ok := workloadKinds[o.Kind]
	if !ok || !isKind(o, o.Kind, groups...) {
		return nil, nil
	}
	w := &Workload{Object: o}
	switch o.Kind {
	case "Pod":
		var doc struct {
			Spec PodSpec `yaml:"spec"`
		}
		if err := o.Node.Decode(&doc); err != nil {
			return nil, decodeError(o.File, o.Node, err)
		}
		w.Spec, w.SpecPath, w.PodLabels = doc.Spec, Path{"spec"}, o.Labels
	case "CronJob":
		var doc struct {
			Spec struct {
				JobTemplate struct {
					Spec struct {
						Template podTemplate `yaml:"template"`
					} `yaml:"spec"`
				} `yaml:"jobTemplate"`
			} `yaml:"spec"`
		}
		if err := o.Node.Decode(&doc); err != nil {
			return nil, decodeError(o.File, o.Node, err)
		}
		t := doc.Spec.JobTemplate.Spec.Template
		w.Spec, w.PodLabels = t.Spec, t.Metadata.Labels
		w.SpecPath = Path{"spec", "jobTemplate", "spec", "template", "spec"}
	default:
		var doc struct {
			Spec struct {
				Replicas *int32      `yaml:"replicas"`
				Template podTemplate `yaml:"template"`
			} `yaml:"spec"`
		}
		if err := o.Node.Decode(&doc); err != nil {
			return nil, decodeError(o.File, o.Node, err)
		}
		t := doc.Spec.Template
		w.Spec, w.PodLabels = t.Spec, t.Metadata.Labels
		w.SpecPath = Path{"spec", "template", "spec"}
		if o.Kind != "DaemonSet" && o.Kind != "Job" {
			w.Replicas = doc.Spec.Replicas
		}
	}
	return w, nil
}
