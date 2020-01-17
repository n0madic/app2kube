package app2kube

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/ghodss/yaml"
	appsv1 "k8s.io/api/apps/v1"
	batch "k8s.io/api/batch/v1beta1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MaxNameLength of App
const MaxNameLength = 63

// IngressCommon specification
type IngressCommon struct {
	Annotations map[string]string `yaml:"annotations"`
	Class       string            `yaml:"class"`
	ServiceName string            `yaml:"serviceName"`
	ServicePort int32             `yaml:"servicePort"`
}

// Ingress specification
type Ingress struct {
	IngressCommon
	Aliases       []string `yaml:"aliases"`
	Host          string   `yaml:"host"`
	Letsencrypt   bool     `yaml:"letsencrypt"`
	Path          string   `yaml:"path"`
	SslRedirect   bool     `yaml:"sslRedirect"`
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

// App instance
type App struct {
	Branch string `yaml:"branch"`
	Common struct {
		CronjobSuspend     bool            `yaml:"cronjobSuspend"`
		DNSPolicy          apiv1.DNSPolicy `yaml:"dnsPolicy"`
		EnableServiceLinks bool            `yaml:"enableServiceLinks"`
		GracePeriod        int64           `yaml:"gracePeriod"`
		Image              struct {
			PullPolicy  apiv1.PullPolicy `yaml:"pullPolicy"`
			PullSecrets string           `yaml:"pullSecrets"`
			Repository  string           `yaml:"repository"`
			Tag         string           `yaml:"tag"`
		} `yaml:"image"`
		Ingress struct {
			IngressCommon
		} `yaml:"ingress"`
		MountServiceAccountToken bool               `yaml:"mountServiceAccountToken"`
		NodeSelector             map[string]string  `yaml:"nodeSelector"`
		SharedData               string             `yaml:"sharedData"`
		Tolerations              []apiv1.Toleration `yaml:"tolerations"`
	} `yaml:"common"`
	ConfigMap map[string]string `yaml:"configmap"`
	Cronjob   map[string]struct {
		ConcurrencyPolicy          batch.ConcurrencyPolicy `yaml:"concurrencyPolicy"`
		Container                  apiv1.Container         `yaml:"container"`
		FailedJobsHistoryLimit     int32                   `yaml:"failedJobsHistoryLimit"`
		RestartPolicy              apiv1.RestartPolicy     `yaml:"restartPolicy"`
		Schedule                   string                  `yaml:"schedule"`
		SuccessfulJobsHistoryLimit int32                   `yaml:"successfulJobsHistoryLimit"`
		Suspend                    bool                    `yaml:"suspend"`
	} `yaml:"cronjob"`
	Deployment struct {
		BlueGreenColor       string                     `yaml:"blueGreenColor"`
		Containers           map[string]apiv1.Container `yaml:"containers"`
		ReplicaCount         int32                      `yaml:"replicaCount"`
		RevisionHistoryLimit int32                      `yaml:"revisionHistoryLimit"`
		Strategy             appsv1.DeploymentStrategy  `yaml:"strategy"`
	} `yaml:"deployment"`
	Env       map[string]string  `yaml:"env"`
	Ingress   []Ingress          `yaml:"ingress"`
	Labels    map[string]string  `yaml:"labels"`
	Name      string             `yaml:"name"`
	Namespace string             `yaml:"namespace"`
	Secrets   map[string]string  `yaml:"secrets"`
	Service   map[string]Service `yaml:"service"`
	Staging   string             `yaml:"staging"`
	Volumes   map[string]struct {
		Spec      apiv1.PersistentVolumeClaimSpec `yaml:"spec"`
		MountPath string                          `yaml:"mountPath"`
	} `yaml:"volumes"`
}

// GetObjectMeta return App metadata
func (app *App) GetObjectMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:        name,
		Namespace:   app.Namespace,
		Labels:      app.Labels,
		Annotations: make(map[string]string),
	}
}

// GetReleaseName of App
func (app *App) GetReleaseName() string {
	releaseName := app.Name
	if app.Staging != "" {
		releaseName = app.Name + "-" + app.Staging
		if app.Branch != "" {
			releaseName = app.Name + "-" + app.Branch
		}
	}
	return strings.ToLower(releaseName)
}

// GetDeploymentName of App
func (app *App) GetDeploymentName() string {
	deploymentName := app.GetReleaseName()
	if app.Deployment.BlueGreenColor != "" {
		deploymentName += "-" + app.Deployment.BlueGreenColor
	}
	return strings.ToLower(deploymentName)
}

func (app *App) getServiceName(name string) string {
	if name == "" {
		return app.GetReleaseName()
	}
	return app.GetReleaseName() + "-" + strings.ToLower(name)
}

// GetColorLabels return labels for blue/green deployment
func (app *App) GetColorLabels() map[string]string {
	labels := make(map[string]string, len(app.Labels))
	for k, v := range app.Labels {
		labels[k] = v
	}
	if app.Deployment.BlueGreenColor != "" {
		labels["app.kubernetes.io/color"] = app.Deployment.BlueGreenColor
	}
	return labels
}

// LoadValues for App
func (app *App) LoadValues(valueFiles ValueFiles, values, stringValues, fileValues []string) ([]byte, error) {
	rawVals, err := vals(valueFiles, values, stringValues, fileValues)
	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal(rawVals, &app)
	if err != nil {
		return nil, err
	}

	if app.Name == "" {
		return nil, errors.New("App name is required")
	}

	app.Name = strings.ToLower(strings.ReplaceAll(app.Name, "_", "-"))
	app.Labels["app.kubernetes.io/name"] = truncateName(app.Name)

	if app.Staging != "" {
		app.Common.Image.PullPolicy = apiv1.PullAlways
		app.Deployment.BlueGreenColor = ""
		app.Deployment.RevisionHistoryLimit = 0
		app.Staging = strings.ToLower(app.Staging)
		app.Branch = strings.ToLower(app.Branch)

		app.Labels["app.kubernetes.io/instance"] = truncateName(app.Staging)
		if app.Branch != "" {
			app.Labels["app.kubernetes.io/instance"] = truncateName(app.Staging + "-" + app.Branch)
		}

		for i, ingress := range app.Ingress {
			if strings.HasPrefix(ingress.Host, "*") {
				return nil, fmt.Errorf("staging cannot be used with wildcard domain: %s", ingress.Host)
			}
			ingress.Host = app.Staging + "." + ingress.Host
			if app.Branch != "" {
				ingress.Host = app.Branch + "." + ingress.Host
			}
			app.Ingress[i].Host = ingress.Host
		}
	} else {
		app.Deployment.BlueGreenColor = strings.ToLower(app.Deployment.BlueGreenColor)
	}

	return rawVals, nil
}

// NewApp return App instance
func NewApp() *App {
	app := &App{}
	// Default settings of App
	app.Labels = map[string]string{
		"app.kubernetes.io/instance": "production",
	}
	app.Common.Image.Tag = "latest"
	app.Deployment.RevisionHistoryLimit = 2
	return app
}

func truncateName(name string) string {
	if len(name) > MaxNameLength {
		name = name[0:MaxNameLength]
	}
	return strings.TrimRightFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}
