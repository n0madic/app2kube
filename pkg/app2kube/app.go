package app2kube

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/ghodss/yaml"
	appsv1 "k8s.io/api/apps/v1"
	batch "k8s.io/api/batch/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// MaxNameLength is the DNS-1123 *label* limit (RFC 1035): the cap for object
// names that must themselves be labels (Service) and for label values.
const MaxNameLength = 63

// MaxSubdomainNameLength is the DNS-1123 *subdomain* limit (RFC 1123): the cap
// for object names that may legally be longer than a label — ConfigMap, Secret,
// PersistentVolumeClaim, Deployment, PodDisruptionBudget and Ingress. Capping
// those at MaxNameLength would needlessly shorten longer names and diverge from
// what earlier app2kube releases emitted, orphaning the live object on apply.
const MaxSubdomainNameLength = 253

// MaxCronJobNameLength caps a CronJob object name: the CronJob controller
// appends an ~11-char timestamp suffix to the 63-char Job name it spawns, so the
// CronJob name itself must stay within 63-11 = 52 characters — stricter than
// other subdomain-named objects.
const MaxCronJobNameLength = 52

// IngressCommon specification
type IngressCommon struct {
	Annotations map[string]string `yaml:"annotations"`
	Class       string            `yaml:"class"`
	Letsencrypt bool              `yaml:"letsencrypt"`
	ServiceName string            `yaml:"serviceName"`
	ServicePort int32             `yaml:"servicePort"`
	SslRedirect bool              `yaml:"sslRedirect"`
}

// Ingress specification
type Ingress struct {
	IngressCommon
	Aliases       []string `yaml:"aliases"`
	Host          string   `yaml:"host"`
	Path          string   `yaml:"path"`
	TLSCrt        string   `yaml:"tlsCrt"`
	TLSKey        string   `yaml:"tlsKey"`
	TLSSecretName string   `yaml:"tlsSecretName"`
}

// Service specification
type Service struct {
	ExternalPort int32             `yaml:"externalPort"`
	InternalPort int32             `yaml:"internalPort"`
	Port         int32             `yaml:"port"`
	Protocol     apiv1.Protocol    `yaml:"protocol"`
	Type         apiv1.ServiceType `yaml:"type"`
}

// ImageSpec is the common container image configuration.
type ImageSpec struct {
	PullPolicy  apiv1.PullPolicy `yaml:"pullPolicy"`
	PullSecrets string           `yaml:"pullSecrets"`
	Repository  string           `yaml:"repository"`
	Tag         string           `yaml:"tag"`
}

// CommonSpec holds settings shared by all workloads of an App.
type CommonSpec struct {
	CronjobSuspend           bool                        `yaml:"cronjobSuspend"`
	DNSPolicy                apiv1.DNSPolicy             `yaml:"dnsPolicy"`
	EnableServiceLinks       bool                        `yaml:"enableServiceLinks"`
	GracePeriod              int64                       `yaml:"gracePeriod"`
	Image                    ImageSpec                   `yaml:"image"`
	Ingress                  IngressCommon               `yaml:"ingress"`
	MountServiceAccountToken bool                        `yaml:"mountServiceAccountToken"`
	NodeSelector             map[string]string           `yaml:"nodeSelector"`
	PodAntiAffinity          string                      `yaml:"podAntiAffinity"`
	Resources                *apiv1.ResourceRequirements `yaml:"resources"`
	SecurityContext          *apiv1.PodSecurityContext   `yaml:"securityContext"`
	ServiceAccountName       string                      `yaml:"serviceAccountName"`
	SharedData               string                      `yaml:"sharedData"`
	Tolerations              []apiv1.Toleration          `yaml:"tolerations"`
}

// CronjobSpec is a single named cronjob definition.
type CronjobSpec struct {
	ActiveDeadlineSeconds      *int64                     `yaml:"activeDeadlineSeconds"`
	BackoffLimit               *int32                     `yaml:"backoffLimit"`
	ConcurrencyPolicy          batch.ConcurrencyPolicy    `yaml:"concurrencyPolicy"`
	Container                  apiv1.Container            `yaml:"container"`
	Containers                 map[string]apiv1.Container `yaml:"containers"`
	FailedJobsHistoryLimit     *int32                     `yaml:"failedJobsHistoryLimit"`
	RestartPolicy              apiv1.RestartPolicy        `yaml:"restartPolicy"`
	Schedule                   string                     `yaml:"schedule"`
	SuccessfulJobsHistoryLimit *int32                     `yaml:"successfulJobsHistoryLimit"`
	Suspend                    bool                       `yaml:"suspend"`
	TimeZone                   string                     `yaml:"timeZone"`
}

// DeploymentSpec is the App's deployment configuration.
type DeploymentSpec struct {
	BlueGreenColor          string                     `yaml:"blueGreenColor"`
	Containers              map[string]apiv1.Container `yaml:"containers"`
	InitContainers          map[string]apiv1.Container `yaml:"initContainers"`
	ProgressDeadlineSeconds *int32                     `yaml:"progressDeadlineSeconds"`
	ReplicaCount            *int32                     `yaml:"replicaCount"`
	ReplicaCountStaging     int32                      `yaml:"replicaCountStaging"`
	RevisionHistoryLimit    int32                      `yaml:"revisionHistoryLimit"`
	Strategy                appsv1.DeploymentStrategy  `yaml:"strategy"`
}

// VolumeSpec is a single named persistent volume claim and its mount path.
type VolumeSpec struct {
	Spec      apiv1.PersistentVolumeClaimSpec `yaml:"spec"`
	MountPath string                          `yaml:"mountPath"`
}

// App instance
type App struct {
	aesPassword   string
	rsaPublicKey  string
	rsaPrivateKey string
	Branch        string                 `yaml:"branch"`
	Common        CommonSpec             `yaml:"common"`
	ConfigMap     map[string]string      `yaml:"configmap"`
	Cronjob       map[string]CronjobSpec `yaml:"cronjob"`
	Deployment    DeploymentSpec         `yaml:"deployment"`
	Env           map[string]string      `yaml:"env"`
	Ingress       []Ingress              `yaml:"ingress"`
	Labels        map[string]string      `yaml:"labels"`
	Name          string                 `yaml:"name"`
	Namespace     string                 `yaml:"namespace"`
	Secrets       map[string]string      `yaml:"secrets"`
	Service       map[string]Service     `yaml:"service"`
	Staging       string                 `yaml:"staging"`
	Volumes       map[string]VolumeSpec  `yaml:"volumes"`
}

// GetObjectMeta return App metadata. Annotations is left nil by default so
// resources that carry none don't render a noisy `annotations: {}`; the only
// caller that adds annotations (the ingress generator) initializes the map
// lazily before writing to it (#67).
func (app *App) GetObjectMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      name,
		Namespace: app.Namespace,
		Labels:    app.Labels,
	}
}

// GetReleaseName of App. It backs the ConfigMap/Secret object names and their
// envFrom references, which are DNS-1123 subdomains, so it is capped at the
// subdomain limit (253) — the single source of that rule. The Service and
// CronJob helpers build on it and re-cap to their own stricter limits (label/52).
func (app *App) GetReleaseName() string {
	return truncateNameTo(app.releaseName(), MaxSubdomainNameLength)
}

// releaseName composes the raw, uncapped release name from Name/Staging/Branch,
// lowercased. GetReleaseName caps it at the subdomain limit (253) and the Service
// helper caps it at the stricter label limit (63); centralizing the composition
// here means each resource's length rule applies exactly once instead of a
// 253-cap being re-capped to 63.
func (app *App) releaseName() string {
	releaseName := app.Name
	if app.Staging != "" {
		releaseName = app.Name + "-" + app.Staging
		if app.Branch != "" {
			releaseName = app.Name + "-" + app.Branch
		}
	}
	return strings.ToLower(releaseName)
}

// GetDeploymentName of App. The Deployment and PDB object names are DNS-1123
// subdomains, so they are capped at the subdomain limit (253) even when the
// color suffix pushes a long release name past it.
func (app *App) GetDeploymentName() string {
	deploymentName := app.GetReleaseName()
	if app.Deployment.BlueGreenColor != "" {
		deploymentName += "-" + app.Deployment.BlueGreenColor
	}
	return truncateNameTo(strings.ToLower(deploymentName), MaxSubdomainNameLength)
}

// GetServiceName returns the cluster Service name for a named service entry:
// the release name when name is empty, otherwise "<release>-<name>" lowercased.
// Exported alongside GetReleaseName/GetDeploymentName because Service names are
// part of the public contract — Ingress backends reference them and callers may
// want to predict them (#66).
func (app *App) GetServiceName(name string) string {
	if name == "" {
		return truncateName(app.releaseName())
	}
	return truncateName(app.releaseName() + "-" + strings.ToLower(name))
}

// GetVolumeClaimName returns the PersistentVolumeClaim name for a named volume,
// "<release>-<volName>", capped at the DNS-1123 subdomain limit (253) — a PVC
// name is a subdomain. It is the single source of this rule so the emitted PVC
// object (pvc.go) and the pod volume references (deployment.go, cronjob.go)
// cannot drift to mismatched names — a mismatch would make pods fail to schedule
// with "persistentvolumeclaim not found".
func (app *App) GetVolumeClaimName(volName string) string {
	return truncateNameTo(app.GetReleaseName()+"-"+volName, MaxSubdomainNameLength)
}

// GetColorLabels returns a copy of the app labels, adding the blue/green color
// label when a color is set. It backs both the pod template labels and the
// Deployment/Service/PDB spec.selector, which therefore stay byte-identical to
// what pre-v0.7 releases emitted — important because spec.selector is immutable
// and a narrower selector would make `kubectl apply` reject upgrades of existing
// Deployments.
func (app *App) GetColorLabels() map[string]string {
	labels := make(map[string]string, len(app.Labels))
	for k, v := range app.Labels {
		labels[k] = v
	}
	if app.Deployment.BlueGreenColor != "" {
		labels[LabelColor] = app.Deployment.BlueGreenColor
	}
	return labels
}

func (app *App) getAffinity() (*apiv1.Affinity, error) {
	var affinity *apiv1.Affinity
	if app.Common.PodAntiAffinity != "" {
		var podAffinityTerm []apiv1.PodAffinityTerm
		// Iterate in sorted key order so the rendered terms are stable across
		// runs; an unsorted (map-random) order would change the pod template on
		// every render and roll the Deployment on each apply.
		labels := app.GetColorLabels()
		for _, label := range sortedKeys(labels) {
			if label == LabelManagedBy {
				continue
			}
			value := labels[label]
			podAffinityTerm = append(podAffinityTerm, apiv1.PodAffinityTerm{
				LabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      label,
							Operator: metav1.LabelSelectorOpIn,
							Values:   []string{value},
						},
					},
				},
				TopologyKey: "kubernetes.io/hostname",
			})
		}
		switch strings.ToLower(app.Common.PodAntiAffinity) {
		case "preferred":
			var weightedPodAffinityTerm []apiv1.WeightedPodAffinityTerm
			for _, term := range podAffinityTerm {
				weightedPodAffinityTerm = append(weightedPodAffinityTerm, apiv1.WeightedPodAffinityTerm{
					PodAffinityTerm: term,
					Weight:          1,
				})
			}
			affinity = &apiv1.Affinity{
				PodAntiAffinity: &apiv1.PodAntiAffinity{
					PreferredDuringSchedulingIgnoredDuringExecution: weightedPodAffinityTerm,
				},
			}
		case "required":
			affinity = &apiv1.Affinity{
				PodAntiAffinity: &apiv1.PodAntiAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: podAffinityTerm,
				},
			}
		default:
			return nil, fmt.Errorf("unknown podAntiAffinity value: %s", app.Common.PodAntiAffinity)
		}
	}
	return affinity, nil
}

// LoadValues merges the given value sources, unmarshals them into the App and
// applies validation and staging transformations. It returns the merged raw
// YAML. The work is split into parse/validate/applyStaging so each step can be
// understood and tested in isolation.
func (app *App) LoadValues(valueFiles ValueFiles, values, stringValues, fileValues []string) ([]byte, error) {
	rawVals, err := app.parseValues(valueFiles, values, stringValues, fileValues)
	if err != nil {
		return nil, err
	}

	if err := app.validate(); err != nil {
		return nil, err
	}

	app.ensureLabels()

	app.Name = strings.ToLower(strings.ReplaceAll(app.Name, "_", "-"))
	app.Labels[LabelName] = truncateName(app.Name)

	// An explicit `image.tag: ""` in the values overwrites the seeded "latest"
	// default with empty, yielding a malformed "repo:" image reference. Restore
	// the default when the tag ends up empty (#44).
	if app.Common.Image.Tag == "" {
		app.Common.Image.Tag = "latest"
	}

	if err := app.applyStaging(); err != nil {
		return nil, err
	}

	return rawVals, nil
}

// parseValues merges the value sources and unmarshals them into the App.
func (app *App) parseValues(valueFiles ValueFiles, values, stringValues, fileValues []string) ([]byte, error) {
	rawVals, err := vals(valueFiles, values, stringValues, fileValues)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(rawVals, &app); err != nil {
		return nil, err
	}
	return rawVals, nil
}

// validate checks required fields after parsing.
func (app *App) validate() error {
	if app.Name == "" {
		return errors.New("app name is required")
	}
	return nil
}

// applyStaging rewrites replica counts, instance labels, ingress hosts and
// image pull policy when a staging environment is configured; otherwise it just
// normalizes the blue/green color. It assumes app.Labels is already initialized
// (LoadValues and NewApp both run ensureLabels first).
func (app *App) applyStaging() error {
	if app.Staging == "" {
		app.Deployment.BlueGreenColor = strings.ToLower(app.Deployment.BlueGreenColor)
		return nil
	}

	app.Common.Image.PullPolicy = apiv1.PullAlways
	app.Deployment.BlueGreenColor = ""
	app.Deployment.RevisionHistoryLimit = 0
	app.Staging = strings.ToLower(app.Staging)
	app.Branch = strings.ToLower(app.Branch)

	if app.Deployment.ReplicaCountStaging > 0 {
		app.Deployment.ReplicaCount = ptr.To(app.Deployment.ReplicaCountStaging)
	} else {
		app.Deployment.ReplicaCount = ptr.To(int32(1))
	}

	app.Labels[LabelInstance] = truncateName(app.Staging)
	if app.Branch != "" {
		app.Labels[LabelInstance] = truncateName(app.Staging + "-" + app.Branch)
	}

	for i, ingress := range app.Ingress {
		if strings.HasPrefix(ingress.Host, "*") {
			return fmt.Errorf("staging cannot be used with wildcard domain: %s", ingress.Host)
		}
		ingress.Host = app.Staging + "." + ingress.Host
		if app.Branch != "" {
			ingress.Host = app.Branch + "." + ingress.Host
		}
		app.Ingress[i].Host = ingress.Host
	}

	return nil
}

// NewApp return App instance
func NewApp() *App {
	app := &App{}
	// Default settings of App. ensureLabels seeds the default instance/managed-by
	// labels (the same defaults LoadValues relies on) so the rule lives in one
	// place.
	app.ensureLabels()
	app.Common.Image.Tag = "latest"
	app.Deployment.RevisionHistoryLimit = 2
	// Read passwords and keys from environment variables
	app.aesPassword = os.Getenv(EnvPassword)
	app.rsaPublicKey = os.Getenv(EnvEncryptKey)
	app.rsaPrivateKey = os.Getenv(EnvDecryptKey)
	return app
}

// ensureLabels guarantees app.Labels is a writable map carrying the default
// instance label, so a `labels: null` / bare `labels:` in user YAML (which
// ghodss/yaml unmarshals into a nil map) cannot leave it nil and panic on the
// next label assignment. It is idempotent and safe for non-NewApp consumers.
func (app *App) ensureLabels() {
	if app.Labels == nil {
		app.Labels = map[string]string{}
	}
	if _, ok := app.Labels[LabelInstance]; !ok {
		app.Labels[LabelInstance] = "production"
	}
	if _, ok := app.Labels[LabelManagedBy]; !ok {
		app.Labels[LabelManagedBy] = ManagedByValue
	}
}

// truncateName trims a name to the DNS-1123 label limit (the strictest, used by
// Service names and label values).
func truncateName(name string) string {
	return truncateNameTo(name, MaxNameLength)
}

// truncateNameTo trims name to at most max bytes, then strips trailing
// characters that are not letters or digits so the result remains a valid
// DNS-1123 name end.
func truncateNameTo(name string, max int) string {
	if len(name) > max {
		name = name[0:max]
	}
	return strings.TrimRightFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}
