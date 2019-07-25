package app2kube

import (
	"errors"
	"strings"

	"github.com/ghodss/yaml"
	appsv1 "k8s.io/api/apps/v1"
	batch "k8s.io/api/batch/v1beta1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// App instance
type App struct {
	CommitHash string `yaml:"commitHash"`
	Common     struct {
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
		MountServiceAccountToken bool               `yaml:"mountServiceAccountToken"`
		NodeSelector             map[string]string  `yaml:"nodeSelector"`
		Tolerations              []apiv1.Toleration `yaml:"tolerations"`
	} `yaml:"common"`
	Configmap map[string]string `yaml:"configmap"`
	Cronjob   map[string]struct {
		Args                       []string                   `yaml:"args"`
		Command                    []string                   `yaml:"command"`
		ConcurrencyPolicy          batch.ConcurrencyPolicy    `yaml:"concurrencyPolicy"`
		FailedJobsHistoryLimit     int32                      `yaml:"failedJobsHistoryLimit"`
		Image                      string                     `yaml:"image"`
		ImagePullPolicy            apiv1.PullPolicy           `yaml:"imagePullPolicy"`
		Resources                  apiv1.ResourceRequirements `yaml:"resources"`
		RestartPolicy              string                     `yaml:"restartPolicy"`
		Schedule                   string                     `yaml:"schedule"`
		SuccessfulJobsHistoryLimit int32                      `yaml:"successfulJobsHistoryLimit"`
		Suspend                    bool                       `yaml:"suspend"`
	} `yaml:"cronjob"`
	Deployment struct {
		Containers map[string]apiv1.Container `yaml:"containers"`
		Ingress    []struct {
			Aliases       []string          `yaml:"aliases"`
			Annotations   map[string]string `yaml:"annotations"`
			Host          string            `yaml:"host"`
			Letsencrypt   bool              `yaml:"letsencrypt"`
			Path          string            `yaml:"path"`
			ServiceName   string            `yaml:"serviceName"`
			ServicePort   int32             `yaml:"servicePort"`
			SslRedirect   bool              `yaml:"sslRedirect"`
			TLSCrt        string            `yaml:"tlsCrt"`
			TLSKey        string            `yaml:"tlsKey"`
			TLSSecretName string            `yaml:"tlsSecretName"`
		} `yaml:"ingress"`
		ReplicaCount         int32 `yaml:"replicaCount"`
		RevisionHistoryLimit int32 `yaml:"revisionHistoryLimit"`
		Service              []struct {
			ExternalPort int32             `yaml:"externalPort"`
			InternalPort int32             `yaml:"internalPort"`
			Port         int32             `yaml:"port"`
			Name         string            `yaml:"name"`
			Protocol     apiv1.Protocol    `yaml:"protocol"`
			Type         apiv1.ServiceType `yaml:"type"`
		} `yaml:"service"`
		SharedData string                    `yaml:"sharedData"`
		Strategy   appsv1.DeploymentStrategy `yaml:"strategy"`
	} `yaml:"deployment"`
	Labels    map[string]string `yaml:"labels"`
	Name      string            `yaml:"name"`
	Namespace string            `yaml:"namespace"`
	Secrets   map[string]string `yaml:"secrets"`
	Volumes   map[string]struct {
		Spec      apiv1.PersistentVolumeClaimSpec `yaml:"spec"`
		MountPath string                          `yaml:"mountPath"`
	} `yaml:"volumes"`
}

// GetObjectMeta return App metadata
func (app *App) GetObjectMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      name,
		Namespace: app.Namespace,
		Labels:    app.Labels,
	}
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

	return rawVals, nil
}

// SetName of App
func (app *App) SetName(name string) {
	app.Name = strings.ToLower(strings.ReplaceAll(app.Name, "_", "-"))
}

// NewApp return App instance
func NewApp() *App {
	app := &App{}
	app.Labels = make(map[string]string)
	// Default settings of App
	app.Common.Image.Tag = "latest"
	app.Deployment.RevisionHistoryLimit = 2
	app.Deployment.Strategy.Type = appsv1.RecreateDeploymentStrategyType
	return app
}
