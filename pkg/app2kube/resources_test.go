package app2kube

import (
	"testing"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestGetServicesPortDefaulting(t *testing.T) {
	app := deployApp(t)
	app.Service = map[string]Service{"web": {Port: 80}}
	svcs, err := app.GetServices()
	if err != nil {
		t.Fatalf("GetServices: %v", err)
	}
	if len(svcs) != 1 {
		t.Fatalf("expected 1 service, got %d", len(svcs))
	}
	p := svcs[0].Spec.Ports[0]
	// When only Port is set, both external and internal default to it.
	if p.Port != 80 {
		t.Errorf("external port: got %d, want 80", p.Port)
	}
	if p.TargetPort != intstr.FromInt(80) {
		t.Errorf("target port: got %v, want 80", p.TargetPort)
	}
	if svcs[0].Name != "example-web" {
		t.Errorf("service name: got %q", svcs[0].Name)
	}
}

func TestGetServicesInternalExternalSplit(t *testing.T) {
	app := deployApp(t)
	app.Service = map[string]Service{"web": {InternalPort: 8080, ExternalPort: 80}}
	svcs, err := app.GetServices()
	if err != nil {
		t.Fatalf("GetServices: %v", err)
	}
	p := svcs[0].Spec.Ports[0]
	if p.Port != 80 {
		t.Errorf("external: got %d, want 80", p.Port)
	}
	if p.TargetPort.IntVal != 8080 {
		t.Errorf("target: got %d, want 8080", p.TargetPort.IntVal)
	}
}

func TestGetServicesOnlyInternal(t *testing.T) {
	// InternalPort with no Port/ExternalPort: external mirrors internal.
	app := deployApp(t)
	app.Service = map[string]Service{"web": {InternalPort: 9000}}
	svcs, err := app.GetServices()
	if err != nil {
		t.Fatalf("GetServices: %v", err)
	}
	p := svcs[0].Spec.Ports[0]
	if p.Port != 9000 || p.TargetPort.IntVal != 9000 {
		t.Errorf("ports: external=%d target=%d, want 9000/9000", p.Port, p.TargetPort.IntVal)
	}
}

func TestGetServicesNoPortError(t *testing.T) {
	app := deployApp(t)
	app.Service = map[string]Service{"web": {}}
	if _, err := app.GetServices(); err == nil {
		t.Errorf("expected error when no port is specified")
	}
}

func TestGetServicesNilWithoutContainers(t *testing.T) {
	app := NewApp()
	app.Name = "example"
	app.Service = map[string]Service{"web": {Port: 80}}
	svcs, err := app.GetServices()
	if err != nil {
		t.Fatalf("GetServices: %v", err)
	}
	if svcs != nil {
		t.Errorf("expected no services without containers, got %+v", svcs)
	}
}

func TestProcessContainerImageFromCommon(t *testing.T) {
	app := NewApp()
	app.Common.Image.Repository = "registry.io/app"
	app.Common.Image.Tag = "v9"
	c := &apiv1.Container{Name: "app"}
	if err := app.processContainer(c, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if c.Image != "registry.io/app:v9" {
		t.Errorf("image: got %q, want registry.io/app:v9", c.Image)
	}
}

func TestProcessContainerNoImageError(t *testing.T) {
	app := NewApp()
	c := &apiv1.Container{Name: "app"}
	if err := app.processContainer(c, false); err == nil {
		t.Errorf("expected error when no image and no common repository")
	}
}

func TestProcessContainerEnvFromConfigAndSecret(t *testing.T) {
	app := NewApp()
	app.Name = "example"
	app.ConfigMap = map[string]string{"K": "v"}
	app.Secrets = map[string]string{"s": "v"}
	c := &apiv1.Container{Name: "app", Image: "example/app:v1"}
	if err := app.processContainer(c, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	var hasCM, hasSecret bool
	for _, ef := range c.EnvFrom {
		if ef.ConfigMapRef != nil && ef.ConfigMapRef.Name == "example" {
			hasCM = true
		}
		if ef.SecretRef != nil && ef.SecretRef.Name == "example" {
			hasSecret = true
		}
	}
	if !hasCM || !hasSecret {
		t.Errorf("expected envFrom configmap=%v secret=%v", hasCM, hasSecret)
	}
}

func TestProcessContainerEnvSorted(t *testing.T) {
	app := NewApp()
	app.Env = map[string]string{"B": "2", "A": "1", "C": "3"}
	c := &apiv1.Container{Name: "app", Image: "example/app:v1"}
	if err := app.processContainer(c, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if len(c.Env) != 3 {
		t.Fatalf("expected 3 env vars, got %d", len(c.Env))
	}
	// Env keys are sorted deterministically.
	if c.Env[0].Name != "A" || c.Env[1].Name != "B" || c.Env[2].Name != "C" {
		t.Errorf("env order not sorted: %+v", c.Env)
	}
}

func TestProcessContainerStagingClearsResources(t *testing.T) {
	app := NewApp()
	app.Staging = "stg"
	c := &apiv1.Container{
		Name:  "app",
		Image: "example/app:v1",
		Resources: apiv1.ResourceRequirements{
			Limits: apiv1.ResourceList{},
		},
	}
	c.Resources.Limits = apiv1.ResourceList{apiv1.ResourceCPU: {}}
	if err := app.processContainer(c, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if len(c.Resources.Limits) != 0 || len(c.Resources.Requests) != 0 {
		t.Errorf("resources must be cleared in staging: %+v", c.Resources)
	}
}

func TestProcessContainerDefaultLivenessProbe(t *testing.T) {
	app := NewApp()
	c := &apiv1.Container{
		Name:  "app",
		Image: "example/app:v1",
		Ports: []apiv1.ContainerPort{{ContainerPort: 8080}},
	}
	if err := app.processContainer(c, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if c.LivenessProbe == nil || c.LivenessProbe.TCPSocket == nil {
		t.Fatalf("expected default TCP liveness probe, got %+v", c.LivenessProbe)
	}
	if c.LivenessProbe.TCPSocket.Port.IntVal != 8080 {
		t.Errorf("probe port: got %d, want 8080", c.LivenessProbe.TCPSocket.Port.IntVal)
	}
}

func TestProcessContainerAutoServiceFromIngress(t *testing.T) {
	// A single container with a named port and an ingress but no service must
	// auto-create a service keyed by the port name.
	app := NewApp()
	app.Name = "example"
	app.Ingress = []Ingress{{Host: "example.com"}}
	app.Deployment.Containers = map[string]apiv1.Container{"app": {}}
	c := &apiv1.Container{
		Name:  "app",
		Image: "example/app:v1",
		Ports: []apiv1.ContainerPort{{Name: "http", ContainerPort: 8080}},
	}
	if err := app.processContainer(c, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	svc, ok := app.Service["http"]
	if !ok {
		t.Fatalf("expected auto-created service 'http', got %+v", app.Service)
	}
	if svc.Port != 8080 {
		t.Errorf("auto service port: got %d, want 8080", svc.Port)
	}
}

func TestGetCronJobsScheduleRequired(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: example
cronjob:
  backup:
    container:
      image: example/app:v1
      command: [echo]
`)
	if _, err := app.GetCronJobs(); err == nil {
		t.Errorf("expected error when schedule is missing")
	}
}

func TestGetCronJobsCommonSuspend(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: example
common:
  cronjobSuspend: true
cronjob:
  backup:
    schedule: "* * * * *"
    suspend: false
    container:
      image: example/app:v1
      command: [echo]
`)
	crons, err := app.GetCronJobs()
	if err != nil {
		t.Fatalf("GetCronJobs: %v", err)
	}
	// common.cronjobSuspend forces suspend=true regardless of the job setting.
	if crons[0].Spec.Suspend == nil || !*crons[0].Spec.Suspend {
		t.Errorf("expected suspend=true from common.cronjobSuspend")
	}
}

func TestGetCronJobsDefaultContainerName(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: example
cronjob:
  backup:
    schedule: "* * * * *"
    container:
      image: example/app:v1
      command: [echo]
`)
	crons, err := app.GetCronJobs()
	if err != nil {
		t.Fatalf("GetCronJobs: %v", err)
	}
	c := crons[0].Spec.JobTemplate.Spec.Template.Spec.Containers
	if len(c) != 1 || c[0].Name != "backup-job" {
		t.Errorf("expected container name backup-job, got %+v", c)
	}
}

func TestGetCronJobsTimeZone(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: example
cronjob:
  backup:
    schedule: "* * * * *"
    timeZone: "Europe/Kyiv"
    container:
      image: example/app:v1
      command: [echo]
`)
	crons, err := app.GetCronJobs()
	if err != nil {
		t.Fatalf("GetCronJobs: %v", err)
	}
	if crons[0].Spec.TimeZone == nil || *crons[0].Spec.TimeZone != "Europe/Kyiv" {
		t.Errorf("timeZone: got %v", crons[0].Spec.TimeZone)
	}
}
