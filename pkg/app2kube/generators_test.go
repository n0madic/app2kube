package app2kube

import (
	"testing"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

// deployApp returns a minimal App with a single named container, ready for
// resource generation. Behavior-locking tests build on top of it.
func deployApp(t *testing.T) *App {
	t.Helper()
	app := NewApp()
	app.Name = "example"
	app.Deployment.Containers = map[string]apiv1.Container{
		"App": {Image: "example/app:v1"},
	}
	return app
}

// cronApp returns a minimal App with one valid cronjob, ready for generation.
func cronApp(t *testing.T) *App {
	t.Helper()
	app := deployApp(t)
	app.Cronjob = map[string]CronjobSpec{
		"backup": {Schedule: "* * * * *", Container: apiv1.Container{Image: "example/app:v1", Command: []string{"echo"}}},
	}
	return app
}

// #50: an optional common.serviceAccountName must be propagated to both the
// Deployment and CronJob pod specs so workloads can run as a dedicated SA.
func TestServiceAccountNameSet(t *testing.T) {
	app := cronApp(t)
	app.Common.ServiceAccountName = "my-sa"

	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if dep.Spec.Template.Spec.ServiceAccountName != "my-sa" {
		t.Errorf("deployment serviceAccountName: got %q, want my-sa", dep.Spec.Template.Spec.ServiceAccountName)
	}

	crons, err := app.GetCronJobs()
	if err != nil {
		t.Fatalf("GetCronJobs: %v", err)
	}
	if got := crons[0].Spec.JobTemplate.Spec.Template.Spec.ServiceAccountName; got != "my-sa" {
		t.Errorf("cronjob serviceAccountName: got %q, want my-sa", got)
	}
}

// #51: cronjob pointer fields must be copies (ptr.To), not aliases of the live
// App field — mutating app.Common after generation must not change the object.
func TestCronJobPointerFieldsAreCopies(t *testing.T) {
	app := cronApp(t)
	app.Common.EnableServiceLinks = true

	crons, err := app.GetCronJobs()
	if err != nil {
		t.Fatalf("GetCronJobs: %v", err)
	}
	app.Common.EnableServiceLinks = false // mutate the live field after generation

	if got := *crons[0].Spec.JobTemplate.Spec.Template.Spec.EnableServiceLinks; got != true {
		t.Errorf("EnableServiceLinks aliased the live App field (got %v after mutation)", got)
	}
}

func TestGetDeploymentNilWhenNoContainers(t *testing.T) {
	app := NewApp()
	app.Name = "example"
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if dep != nil {
		t.Errorf("expected nil deployment without containers, got %+v", dep)
	}
}

func TestGetDeploymentDefaults(t *testing.T) {
	app := deployApp(t)
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if dep.Name != "example" {
		t.Errorf("name: got %q", dep.Name)
	}
	if *dep.Spec.Replicas != 1 {
		t.Errorf("replicas default: got %d, want 1", *dep.Spec.Replicas)
	}
	if *dep.Spec.RevisionHistoryLimit != 2 {
		t.Errorf("revisionHistoryLimit default: got %d, want 2", *dep.Spec.RevisionHistoryLimit)
	}
	if len(dep.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(dep.Spec.Template.Spec.Containers))
	}
	// Container name must be lowercased.
	if dep.Spec.Template.Spec.Containers[0].Name != "app" {
		t.Errorf("container name: got %q, want app", dep.Spec.Template.Spec.Containers[0].Name)
	}
	if *dep.Spec.Template.Spec.EnableServiceLinks != false {
		t.Errorf("EnableServiceLinks default: got true, want false")
	}
	if *dep.Spec.Template.Spec.AutomountServiceAccountToken != false {
		t.Errorf("AutomountServiceAccountToken default: got true, want false")
	}
}

func TestGetDeploymentReplicaCount(t *testing.T) {
	app := deployApp(t)
	app.Deployment.ReplicaCount = ptr.To(int32(5))
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if *dep.Spec.Replicas != 5 {
		t.Errorf("replicas: got %d, want 5", *dep.Spec.Replicas)
	}
}

// #42: an unset replicaCount defaults to 1, but an explicit 0 must be honored
// (scale-to-zero), which is only distinguishable because the field is *int32.
func TestGetDeploymentReplicaCountZeroAndDefault(t *testing.T) {
	// Explicit 0 → 0 replicas.
	app := deployApp(t)
	app.Deployment.ReplicaCount = ptr.To(int32(0))
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if *dep.Spec.Replicas != 0 {
		t.Errorf("explicit replicaCount 0: got %d, want 0", *dep.Spec.Replicas)
	}

	// Unset (nil) → default 1.
	app = deployApp(t)
	dep, err = app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if *dep.Spec.Replicas != 1 {
		t.Errorf("unset replicaCount: got %d, want 1", *dep.Spec.Replicas)
	}
}

func TestGetDeploymentPullSecretsAndGracePeriod(t *testing.T) {
	app := deployApp(t)
	app.Common.Image.PullSecrets = "regcred"
	app.Common.GracePeriod = 30
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	ps := dep.Spec.Template.Spec.ImagePullSecrets
	if len(ps) != 1 || ps[0].Name != "regcred" {
		t.Errorf("ImagePullSecrets: got %+v", ps)
	}
	if dep.Spec.Template.Spec.TerminationGracePeriodSeconds == nil ||
		*dep.Spec.Template.Spec.TerminationGracePeriodSeconds != 30 {
		t.Errorf("TerminationGracePeriodSeconds: got %v, want 30",
			dep.Spec.Template.Spec.TerminationGracePeriodSeconds)
	}
}

func TestGetDeploymentSharedDataVolumeMatchesMount(t *testing.T) {
	// #18 edge: processContainer mounts shared-data on every app-image container
	// whenever SharedData is set, so the EmptyDir volume must exist even with a
	// single container — otherwise the pod references a missing volume (invalid
	// spec). The volume is keyed off SharedData, not the container count.
	app := deployApp(t)
	app.Common.SharedData = "/shared"
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}

	var mountFound bool
	for _, m := range dep.Spec.Template.Spec.Containers[0].VolumeMounts {
		if m.Name == "shared-data" {
			mountFound = true
		}
	}
	if !mountFound {
		t.Fatalf("single container must mount shared-data: %+v",
			dep.Spec.Template.Spec.Containers[0].VolumeMounts)
	}
	var volFound bool
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Name == "shared-data" && v.EmptyDir != nil {
			volFound = true
		}
	}
	if !volFound {
		t.Errorf("shared-data volume must exist to match the single container's mount: %+v",
			dep.Spec.Template.Spec.Volumes)
	}

	// With two containers it is still added and mounted.
	app = deployApp(t)
	app.Common.SharedData = "/shared"
	app.Deployment.Containers["sidecar"] = apiv1.Container{Image: "example/side:v1"}
	dep, err = app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	var found bool
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Name == "shared-data" && v.EmptyDir != nil {
			found = true
		}
	}
	if !found {
		t.Errorf("shared-data emptyDir volume expected with two containers")
	}
}

// #22: the Deployment pod template must carry checksum/configmap and
// checksum/secret annotations (sha256 of the rendered data) so a config/secret
// change rolls the Deployment. The values must match the hash of the actual
// data the ConfigMap/Secret objects carry.
func TestGetDeploymentConfigChecksumAnnotations(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: web
configmap:
  LOG_LEVEL: info
secrets:
  DB_PASSWORD: s3cr3t
deployment:
  containers:
    app:
      image: example/web:v1
`)
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	ann := dep.Spec.Template.Annotations

	wantCM := dataChecksum(map[string][]byte{"LOG_LEVEL": []byte("info")})
	if ann["checksum/configmap"] != wantCM {
		t.Errorf("checksum/configmap: got %q, want %q", ann["checksum/configmap"], wantCM)
	}
	wantSecret := dataChecksum(map[string][]byte{"DB_PASSWORD": []byte("s3cr3t")})
	if ann["checksum/secret"] != wantSecret {
		t.Errorf("checksum/secret: got %q, want %q", ann["checksum/secret"], wantSecret)
	}
}

// #22: the secret checksum is computed over the values as loaded (ciphertext or
// plaintext), so rendering a Deployment that references encrypted secrets never
// requires the decrypt key, and the digest stays deterministic across renders.
func TestGetDeploymentSecretChecksumNeedsNoDecryptKey(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: web
secrets:
  TOKEN: "AES#not-a-real-ciphertext"
deployment:
  containers:
    app:
      image: example/web:v1
`)
	// No decrypt key is configured, so decrypting would fail; the checksum must
	// still render without error (it hashes the raw loaded value).
	app.aesPassword = ""
	app.rsaPrivateKey = ""

	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment must not require the decrypt key for the checksum: %v", err)
	}
	want := dataChecksum(map[string][]byte{"TOKEN": []byte("AES#not-a-real-ciphertext")})
	if got := dep.Spec.Template.Annotations["checksum/secret"]; got != want {
		t.Errorf("checksum/secret over raw value: got %q, want %q", got, want)
	}
}

// #22 refinement: the checksum annotation is added only to workloads whose
// containers actually reference the config via envFrom. A workload built solely
// from a third-party image (no envFrom injection) must get no checksum.
func TestGetDeploymentChecksumOnlyWhenConfigWired(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: web
common:
  image:
    repository: example/app
configmap:
  LOG_LEVEL: info
deployment:
  containers:
    side:
      image: other.io/lib:1.0
`)
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if _, ok := dep.Spec.Template.Annotations["checksum/configmap"]; ok {
		t.Errorf("third-party-only workload must not get a config checksum: %+v",
			dep.Spec.Template.Annotations)
	}
}

// #18 edge (cronjob): a single-container cronjob with shared-data set must also
// get the EmptyDir volume to match the mount processContainer adds.
func TestGetCronJobsSharedDataVolumeMatchesMount(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: example
common:
  sharedData: /shared
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
	spec := crons[0].Spec.JobTemplate.Spec.Template.Spec

	var mountFound bool
	for _, m := range spec.Containers[0].VolumeMounts {
		if m.Name == "shared-data" {
			mountFound = true
		}
	}
	if !mountFound {
		t.Fatalf("cronjob container must mount shared-data: %+v", spec.Containers[0].VolumeMounts)
	}
	var volFound bool
	for _, v := range spec.Volumes {
		if v.Name == "shared-data" && v.EmptyDir != nil {
			volFound = true
		}
	}
	if !volFound {
		t.Errorf("cronjob shared-data volume must exist to match the mount: %+v", spec.Volumes)
	}
}

// #18: a single main container plus an app-image init container, with shared
// data, must still get the shared-data volume — the init container mounts it in
// processContainer, so without the volume the pod spec would be invalid.
func TestGetDeploymentSharedDataWithInitContainer(t *testing.T) {
	app := deployApp(t)
	app.Common.SharedData = "/shared"
	app.Deployment.InitContainers = map[string]apiv1.Container{
		"migrate": {Image: "example/migrate:v1"},
	}
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	var volFound bool
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Name == "shared-data" && v.EmptyDir != nil {
			volFound = true
		}
	}
	if !volFound {
		t.Fatalf("shared-data volume must exist when an init container mounts it: %+v",
			dep.Spec.Template.Spec.Volumes)
	}
	// The init container mounts shared-data, and now has a matching volume.
	var mountFound bool
	for _, m := range dep.Spec.Template.Spec.InitContainers[0].VolumeMounts {
		if m.Name == "shared-data" {
			mountFound = true
		}
	}
	if !mountFound {
		t.Errorf("init container expected to mount shared-data, got %+v",
			dep.Spec.Template.Spec.InitContainers[0].VolumeMounts)
	}
}

func TestGetDeploymentVolumes(t *testing.T) {
	app := deployApp(t)
	app.Volumes = map[string]VolumeSpec{
		"data": {MountPath: "/data"},
	}
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	var found bool
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Name == "data" && v.PersistentVolumeClaim != nil &&
			v.PersistentVolumeClaim.ClaimName == "example-data" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected PVC volume 'data' with claim 'example-data', got %+v",
			dep.Spec.Template.Spec.Volumes)
	}
}

func TestGetDeploymentBlueGreenLabels(t *testing.T) {
	app := deployApp(t)
	app.Deployment.BlueGreenColor = "blue"
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if dep.Name != "example-blue" {
		t.Errorf("name: got %q, want example-blue", dep.Name)
	}
	if dep.Labels["app.kubernetes.io/color"] != "blue" {
		t.Errorf("color label: got %q", dep.Labels["app.kubernetes.io/color"])
	}
	if dep.Spec.Selector.MatchLabels["app.kubernetes.io/color"] != "blue" {
		t.Errorf("selector color label missing: %+v", dep.Spec.Selector.MatchLabels)
	}
}

// Regression (#24): spec.selector is immutable. It must carry only the stable
// identity (name + instance, plus color for blue/green) and must NOT include
// managed-by or arbitrary user labels — otherwise adding/changing any such
// label, or dropping the color on a later release, makes the apiserver reject a
// plain `kubectl apply`. The full label set still lives on the object and pod
// template via GetColorLabels.
func TestDeploymentSelectorExcludesMutableLabels(t *testing.T) {
	app := deployApp(t)
	app.Labels[LabelName] = "example"
	app.Labels["team"] = "payments" // arbitrary user label

	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	sel := dep.Spec.Selector.MatchLabels

	if _, ok := sel["team"]; ok {
		t.Errorf("user label must not be in immutable selector: %+v", sel)
	}
	if _, ok := sel[LabelManagedBy]; ok {
		t.Errorf("managed-by must not be in immutable selector: %+v", sel)
	}
	if sel[LabelName] != "example" {
		t.Errorf("selector must keep name: %+v", sel)
	}
	if sel[LabelInstance] != "production" {
		t.Errorf("selector must keep instance: %+v", sel)
	}
	// The pod template must still carry the full label set (incl. the user label).
	if dep.Spec.Template.Labels["team"] != "payments" {
		t.Errorf("pod template must keep user labels: %+v", dep.Spec.Template.Labels)
	}
}

func TestGetDeploymentAffinity(t *testing.T) {
	cases := []struct {
		value     string
		wantErr   bool
		preferred bool
		required  bool
	}{
		{"", false, false, false},
		{"preferred", false, true, false},
		{"required", false, false, true},
		{"bogus", true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			app := deployApp(t)
			app.Common.PodAntiAffinity = tc.value
			dep, err := app.GetDeployment()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetDeployment: %v", err)
			}
			aff := dep.Spec.Template.Spec.Affinity
			if tc.value == "" {
				if aff != nil {
					t.Errorf("expected nil affinity, got %+v", aff)
				}
				return
			}
			if aff == nil || aff.PodAntiAffinity == nil {
				t.Fatalf("expected pod anti-affinity, got %+v", aff)
			}
			if tc.preferred && len(aff.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution) == 0 {
				t.Errorf("expected preferred terms")
			}
			if tc.required && len(aff.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution) == 0 {
				t.Errorf("expected required terms")
			}
		})
	}
}

// Init containers must not run the main-container service/probe pipeline: the
// Kubernetes API rejects probes on non-sidecar init containers, and an init
// container port must never drive auto-service derivation.
func TestInitContainerSkipsProbeAndAutoService(t *testing.T) {
	app := deployApp(t)
	// Enable the auto-service code path: an ingress is present and the single
	// main container has no named port — only the init container exposes one.
	app.Ingress = []Ingress{{Host: "example.com"}}
	app.Deployment.InitContainers = map[string]apiv1.Container{
		"migrate": {
			Image: "example/migrate:v1",
			Ports: []apiv1.ContainerPort{{Name: "web", ContainerPort: 8080}},
		},
	}
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if len(dep.Spec.Template.Spec.InitContainers) != 1 {
		t.Fatalf("expected 1 init container, got %d", len(dep.Spec.Template.Spec.InitContainers))
	}
	if probe := dep.Spec.Template.Spec.InitContainers[0].LivenessProbe; probe != nil {
		t.Errorf("init container must not receive an auto LivenessProbe, got %+v", probe)
	}
	if len(app.Service) != 0 {
		t.Errorf("init container port must not create an auto-service, got %+v", app.Service)
	}
}

// A main container with a single named port still gets the auto LivenessProbe
// and drives auto-service derivation (the behavior init containers must skip).
func TestMainContainerGetsProbeAndAutoService(t *testing.T) {
	app := deployApp(t)
	app.Ingress = []Ingress{{Host: "example.com"}}
	app.Deployment.Containers = map[string]apiv1.Container{
		"app": {
			Image: "example/app:v1",
			Ports: []apiv1.ContainerPort{{Name: "web", ContainerPort: 8080}},
		},
	}
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	c := dep.Spec.Template.Spec.Containers[0]
	if c.LivenessProbe == nil || c.LivenessProbe.TCPSocket == nil {
		t.Errorf("main container with a single port must get a TCP LivenessProbe, got %+v", c.LivenessProbe)
	}
	if _, ok := app.Service["web"]; !ok {
		t.Errorf("main container named port must create an auto-service, got %+v", app.Service)
	}
}

func TestGetConfigMap(t *testing.T) {
	app := NewApp()
	app.Name = "example"

	cm, err := app.GetConfigMap()
	if err != nil {
		t.Fatalf("GetConfigMap: %v", err)
	}
	if cm != nil {
		t.Errorf("expected nil configmap when empty, got %+v", cm)
	}

	app.ConfigMap = map[string]string{"KEY": "value"}
	cm, err = app.GetConfigMap()
	if err != nil {
		t.Fatalf("GetConfigMap: %v", err)
	}
	if cm.Name != "example" {
		t.Errorf("name: got %q", cm.Name)
	}
	if cm.Data["KEY"] != "value" {
		t.Errorf("data: got %+v", cm.Data)
	}
}

func TestGetSecret(t *testing.T) {
	app := NewApp()
	app.Name = "example"

	s, err := app.GetSecret()
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if s != nil {
		t.Errorf("expected nil secret when empty, got %+v", s)
	}

	// Plaintext (non-encrypted) secrets are stored verbatim as bytes.
	app.Secrets = map[string]string{"pwd": "plain"}
	s, err = app.GetSecret()
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if string(s.Data["pwd"]) != "plain" {
		t.Errorf("data: got %q", string(s.Data["pwd"]))
	}
}

func TestGetSecretDecryptError(t *testing.T) {
	// An RSA-prefixed value without a private key configured must surface the
	// decryption error rather than emitting a broken Secret.
	app := NewApp()
	app.Name = "example"
	app.Secrets = map[string]string{"pwd": "RSA#deadbeef"}
	if _, err := app.GetSecret(); err == nil {
		t.Errorf("expected error decrypting RSA secret without key")
	}
}

func TestGetPersistentVolumeClaims(t *testing.T) {
	app := NewApp()
	app.Name = "example"
	app.Volumes = map[string]VolumeSpec{
		"data": {
			MountPath: "/data",
			Spec: apiv1.PersistentVolumeClaimSpec{
				AccessModes: []apiv1.PersistentVolumeAccessMode{apiv1.ReadWriteOnce},
				Resources: apiv1.VolumeResourceRequirements{
					Requests: apiv1.ResourceList{
						apiv1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		},
	}
	claims, err := app.GetPersistentVolumeClaims()
	if err != nil {
		t.Fatalf("GetPersistentVolumeClaims: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("expected 1 claim, got %d", len(claims))
	}
	if claims[0].Name != "example-data" {
		t.Errorf("claim name: got %q", claims[0].Name)
	}
	if len(claims[0].Spec.AccessModes) != 1 {
		t.Errorf("spec not copied: %+v", claims[0].Spec)
	}
}

func TestGetPersistentVolumeClaimsMissingMountPath(t *testing.T) {
	app := NewApp()
	app.Name = "example"
	app.Volumes = map[string]VolumeSpec{
		"data": {},
	}
	if _, err := app.GetPersistentVolumeClaims(); err == nil {
		t.Errorf("expected error when mount path is missing")
	}
}

// #48: an omitted accessModes produces a PVC the apiserver rejects; fail fast
// with a clear error at generation time instead.
func TestGetPersistentVolumeClaimsMissingAccessModes(t *testing.T) {
	app := NewApp()
	app.Name = "example"
	app.Volumes = map[string]VolumeSpec{
		"data": {MountPath: "/data"}, // no AccessModes
	}
	if _, err := app.GetPersistentVolumeClaims(); err == nil {
		t.Errorf("expected error when accessModes is empty")
	}
}

// #48: only ReadWriteMany/ReadOnlyMany let more than one pod mount a volume; a
// ReadWriteOnce(-only) volume cannot, so it must not be reported multi-attach.
func TestPVCAllowsMultiAttach(t *testing.T) {
	if pvcAllowsMultiAttach([]apiv1.PersistentVolumeAccessMode{apiv1.ReadWriteOnce}) {
		t.Errorf("ReadWriteOnce must not be multi-attach")
	}
	if !pvcAllowsMultiAttach([]apiv1.PersistentVolumeAccessMode{apiv1.ReadWriteMany}) {
		t.Errorf("ReadWriteMany must be multi-attach")
	}
	if !pvcAllowsMultiAttach([]apiv1.PersistentVolumeAccessMode{apiv1.ReadWriteOnce, apiv1.ReadOnlyMany}) {
		t.Errorf("ReadOnlyMany must be multi-attach")
	}
}

func TestGetNamespace(t *testing.T) {
	app := NewApp()

	if ns := app.GetNamespace(); ns != nil {
		t.Errorf("expected nil namespace when unset, got %+v", ns)
	}

	app.Namespace = "prod"
	app.Labels["app.kubernetes.io/managed-by"] = "app2kube"
	ns := app.GetNamespace()
	if ns == nil {
		t.Fatalf("expected namespace object")
	}
	if ns.Name != "prod" {
		t.Errorf("name: got %q", ns.Name)
	}
	if ns.Labels["app.kubernetes.io/managed-by"] != "app2kube" {
		t.Errorf("managed-by label not copied: %+v", ns.Labels)
	}
}

func TestGetObjectMeta(t *testing.T) {
	app := NewApp()
	app.Namespace = "prod"
	app.Labels = map[string]string{"foo": "bar"}
	meta := app.GetObjectMeta("thing")
	if meta.Name != "thing" || meta.Namespace != "prod" {
		t.Errorf("meta: %+v", meta)
	}
	if meta.Labels["foo"] != "bar" {
		t.Errorf("labels not propagated: %+v", meta.Labels)
	}
	// #67: annotations are left nil by default so resources that carry none do
	// not render a noisy `annotations: {}`; only the ingress generator adds them.
	if meta.Annotations != nil {
		t.Errorf("annotations must be nil by default, got %v", meta.Annotations)
	}
}
