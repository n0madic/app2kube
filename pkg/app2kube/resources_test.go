package app2kube

import (
	"testing"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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

// Regression (#17): a global app.Env key that collides with a container-level
// env var must not be appended again (which emitted a duplicate key, and
// Kubernetes resolving to the LAST silently let the global value win). The
// explicit container value must win; global-only keys are still injected.
func TestProcessContainerEnvContainerWins(t *testing.T) {
	app := NewApp()
	app.Env = map[string]string{"FOO": "global", "BAR": "globalbar"}
	c := &apiv1.Container{
		Name:  "app",
		Image: "example/app:v1",
		Env:   []apiv1.EnvVar{{Name: "FOO", Value: "container"}},
	}
	if err := app.processContainer(c, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}

	fooCount, fooVal, hasBar := 0, "", false
	for _, e := range c.Env {
		switch e.Name {
		case "FOO":
			fooCount++
			fooVal = e.Value
		case "BAR":
			hasBar = e.Value == "globalbar"
		}
	}
	if fooCount != 1 {
		t.Errorf("expected a single FOO env var, got %d: %+v", fooCount, c.Env)
	}
	if fooVal != "container" {
		t.Errorf("container env must win: got FOO=%q, want container", fooVal)
	}
	if !hasBar {
		t.Errorf("global-only env BAR must still be injected: %+v", c.Env)
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

// #19: app-image containers without an explicit securityContext get the
// conservative default (allowPrivilegeEscalation:false, drop ALL). A
// user-provided context is preserved, and third-party images get nothing.
func TestProcessContainerDefaultSecurityContext(t *testing.T) {
	app := NewApp()
	app.Common.Image.Repository = "example/app"

	c := &apiv1.Container{Name: "app", Image: "example/app:v1"}
	if err := app.processContainer(c, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if c.SecurityContext == nil {
		t.Fatal("expected default SecurityContext, got nil")
	}
	if c.SecurityContext.AllowPrivilegeEscalation == nil || *c.SecurityContext.AllowPrivilegeEscalation {
		t.Errorf("expected AllowPrivilegeEscalation=false, got %v", c.SecurityContext.AllowPrivilegeEscalation)
	}
	if c.SecurityContext.Capabilities == nil ||
		len(c.SecurityContext.Capabilities.Drop) != 1 ||
		c.SecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Errorf("expected capabilities drop [ALL], got %+v", c.SecurityContext.Capabilities)
	}

	// A user-provided securityContext is left untouched.
	runAsUser := int64(1000)
	userSC := &apiv1.SecurityContext{RunAsUser: &runAsUser}
	c2 := &apiv1.Container{Name: "app", Image: "example/app:v1", SecurityContext: userSC}
	if err := app.processContainer(c2, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if c2.SecurityContext != userSC {
		t.Errorf("user SecurityContext overwritten: %+v", c2.SecurityContext)
	}

	// A third-party image gets no default securityContext.
	c3 := &apiv1.Container{Name: "side", Image: "other.io/lib:1.0"}
	if err := app.processContainer(c3, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if c3.SecurityContext != nil {
		t.Errorf("third-party container must not get a default SecurityContext, got %+v", c3.SecurityContext)
	}
}

// #20: an opt-in common.resources baseline is applied to app-image containers
// that declare no resources (non-staging only); user resources and third-party
// images are untouched, and staging still strips everything.
func TestProcessContainerCommonResourcesDefault(t *testing.T) {
	res := apiv1.ResourceRequirements{
		Requests: apiv1.ResourceList{apiv1.ResourceCPU: resource.MustParse("10m")},
	}
	newApp := func() *App {
		app := NewApp()
		app.Common.Image.Repository = "example/app"
		app.Common.Resources = &res
		return app
	}

	// App-image container without resources inherits the common default.
	app := newApp()
	c := &apiv1.Container{Name: "app", Image: "example/app:v1"}
	if err := app.processContainer(c, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if c.Resources.Requests.Cpu().MilliValue() != 10 {
		t.Errorf("expected default cpu request 10m, got %v", c.Resources.Requests.Cpu())
	}

	// A container with its own resources is left untouched.
	app = newApp()
	own := apiv1.ResourceRequirements{Requests: apiv1.ResourceList{apiv1.ResourceCPU: resource.MustParse("50m")}}
	c2 := &apiv1.Container{Name: "app", Image: "example/app:v1", Resources: own}
	if err := app.processContainer(c2, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if c2.Resources.Requests.Cpu().MilliValue() != 50 {
		t.Errorf("expected user cpu request 50m preserved, got %v", c2.Resources.Requests.Cpu())
	}

	// Third-party images do not inherit the default.
	app = newApp()
	c3 := &apiv1.Container{Name: "side", Image: "other.io/lib:1.0"}
	if err := app.processContainer(c3, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if len(c3.Resources.Requests) != 0 {
		t.Errorf("third-party container must not inherit common resources, got %+v", c3.Resources)
	}

	// Staging still strips resources (the common default is not applied there).
	app = newApp()
	app.Staging = "stg"
	c4 := &apiv1.Container{Name: "app", Image: "example/app:v1"}
	if err := app.processContainer(c4, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if len(c4.Resources.Requests) != 0 || len(c4.Resources.Limits) != 0 {
		t.Errorf("staging must not carry resources, got %+v", c4.Resources)
	}
}

// #21: a single-port container with no readiness probe gets a TCP readiness
// probe mirroring the auto liveness target. User probes are preserved and init
// containers get none.
func TestProcessContainerDefaultReadinessProbe(t *testing.T) {
	app := NewApp()

	c := &apiv1.Container{
		Name:  "app",
		Image: "example/app:v1",
		Ports: []apiv1.ContainerPort{{ContainerPort: 8080}},
	}
	if err := app.processContainer(c, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if c.ReadinessProbe == nil || c.ReadinessProbe.TCPSocket == nil {
		t.Fatalf("expected default TCP readiness probe, got %+v", c.ReadinessProbe)
	}
	if got := c.ReadinessProbe.TCPSocket.Port.IntVal; got != 8080 {
		t.Errorf("readiness probe port: expected 8080, got %d", got)
	}

	// A user-provided readiness probe is preserved (and its unset port filled).
	userRP := &apiv1.Probe{ProbeHandler: apiv1.ProbeHandler{HTTPGet: &apiv1.HTTPGetAction{Path: "/healthz"}}}
	c2 := &apiv1.Container{
		Name:           "app",
		Image:          "example/app:v1",
		Ports:          []apiv1.ContainerPort{{ContainerPort: 8080}},
		ReadinessProbe: userRP,
	}
	if err := app.processContainer(c2, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if c2.ReadinessProbe.HTTPGet == nil || c2.ReadinessProbe.HTTPGet.Path != "/healthz" {
		t.Fatalf("user readiness probe overwritten: %+v", c2.ReadinessProbe)
	}
	if c2.ReadinessProbe.HTTPGet.Port.IntVal != 8080 {
		t.Errorf("readiness HTTPGet port not filled: %+v", c2.ReadinessProbe.HTTPGet.Port)
	}

	// Init containers get no probes at all.
	c3 := &apiv1.Container{
		Name:  "init",
		Image: "example/app:v1",
		Ports: []apiv1.ContainerPort{{ContainerPort: 8080}},
	}
	if err := app.processContainer(c3, true); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if c3.ReadinessProbe != nil {
		t.Errorf("init container must not get a readiness probe, got %+v", c3.ReadinessProbe)
	}

	// Third-party sidecars get no auto readiness probe (so their probe cannot
	// gate whole-pod readiness).
	tpApp := NewApp()
	tpApp.Common.Image.Repository = "example/app"
	c4 := &apiv1.Container{
		Name:  "cache",
		Image: "redis:latest",
		Ports: []apiv1.ContainerPort{{ContainerPort: 6379}},
	}
	if err := tpApp.processContainer(c4, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if c4.ReadinessProbe != nil {
		t.Errorf("third-party container must not get a readiness probe, got %+v", c4.ReadinessProbe)
	}

	// Multi-port containers get no auto readiness probe.
	c5 := &apiv1.Container{
		Name:  "app",
		Image: "example/app:v1",
		Ports: []apiv1.ContainerPort{{ContainerPort: 8080}, {ContainerPort: 9090}},
	}
	if err := app.processContainer(c5, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if c5.ReadinessProbe != nil {
		t.Errorf("multi-port container must not get a readiness probe, got %+v", c5.ReadinessProbe)
	}

	// A user liveness probe but no readiness still gets an auto TCP readiness probe.
	c6 := &apiv1.Container{
		Name:          "app",
		Image:         "example/app:v1",
		Ports:         []apiv1.ContainerPort{{ContainerPort: 8080}},
		LivenessProbe: &apiv1.Probe{ProbeHandler: apiv1.ProbeHandler{HTTPGet: &apiv1.HTTPGetAction{Path: "/live"}}},
	}
	if err := app.processContainer(c6, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if c6.ReadinessProbe == nil || c6.ReadinessProbe.TCPSocket == nil ||
		c6.ReadinessProbe.TCPSocket.Port.IntVal != 8080 {
		t.Errorf("expected auto TCP readiness probe alongside user liveness, got %+v", c6.ReadinessProbe)
	}

	// A user-provided TCPSocket readiness probe is preserved unchanged.
	userTCP := &apiv1.Probe{ProbeHandler: apiv1.ProbeHandler{TCPSocket: &apiv1.TCPSocketAction{Port: intstr.FromInt(7000)}}}
	c7 := &apiv1.Container{
		Name:           "app",
		Image:          "example/app:v1",
		Ports:          []apiv1.ContainerPort{{ContainerPort: 8080}},
		ReadinessProbe: userTCP,
	}
	if err := app.processContainer(c7, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if c7.ReadinessProbe != userTCP || c7.ReadinessProbe.TCPSocket.Port.IntVal != 7000 {
		t.Errorf("user TCPSocket readiness probe must be preserved, got %+v", c7.ReadinessProbe)
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

// #19: cronjob pod templates also carry the conservative default pod
// securityContext (seccompProfile: RuntimeDefault), shared with the deployment.
func TestGetCronJobsDefaultPodSecurityContext(t *testing.T) {
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
	sc := crons[0].Spec.JobTemplate.Spec.Template.Spec.SecurityContext
	if sc == nil || sc.SeccompProfile == nil ||
		sc.SeccompProfile.Type != apiv1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("expected default pod securityContext with seccomp RuntimeDefault, got %+v", sc)
	}
}

// Regression (#12): the cronjob pod template must carry the app labels so
// cronjob-spawned pods match the prune selector and the status pod listing.
// Without them the pods are invisible to pruning, tracking and status.
func TestGetCronJobsPodTemplateLabels(t *testing.T) {
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
	podLabels := crons[0].Spec.JobTemplate.Spec.Template.Labels
	if podLabels[LabelManagedBy] != ManagedByValue {
		t.Errorf("cronjob pod template missing managed-by: %+v", podLabels)
	}
	if podLabels[LabelInstance] != "production" {
		t.Errorf("cronjob pod template missing instance: %+v", podLabels)
	}
}

// #22: the cronjob pod template must also carry the checksum/configmap
// annotation so a config change rolls the cronjob pods, mirroring the
// deployment.
func TestGetCronJobsConfigChecksumAnnotations(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: web
configmap:
  LOG_LEVEL: info
cronjob:
  backup:
    schedule: "* * * * *"
    container:
      image: example/web:v1
      command: [echo]
`)
	crons, err := app.GetCronJobs()
	if err != nil {
		t.Fatalf("GetCronJobs: %v", err)
	}
	ann := crons[0].Spec.JobTemplate.Spec.Template.Annotations
	want := dataChecksum(map[string][]byte{"LOG_LEVEL": []byte("info")})
	if ann["checksum/configmap"] != want {
		t.Errorf("cronjob checksum/configmap: got %q, want %q", ann["checksum/configmap"], want)
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
