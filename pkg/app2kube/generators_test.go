package app2kube

import (
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

// #46: an unset deployment strategy defaults to a zero-downtime RollingUpdate
// (maxUnavailable:0, maxSurge:1) and an explicit progressDeadlineSeconds, so a
// wedged rollout reports failure instead of hanging.
func TestGetDeploymentRolloutDefaults(t *testing.T) {
	app := deployApp(t)
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if dep.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType {
		t.Errorf("strategy type: got %q, want RollingUpdate", dep.Spec.Strategy.Type)
	}
	ru := dep.Spec.Strategy.RollingUpdate
	if ru == nil || ru.MaxUnavailable.IntValue() != 0 || ru.MaxSurge.IntValue() != 1 {
		t.Errorf("rolling update default: got %+v", ru)
	}
	if dep.Spec.ProgressDeadlineSeconds == nil || *dep.Spec.ProgressDeadlineSeconds != 15*60 {
		t.Errorf("progressDeadlineSeconds default: got %v, want 900 (15m)", dep.Spec.ProgressDeadlineSeconds)
	}
}

// #46: a user-provided strategy and progressDeadlineSeconds must be preserved.
func TestGetDeploymentRolloutUserOverride(t *testing.T) {
	app := deployApp(t)
	app.Deployment.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
	app.Deployment.ProgressDeadlineSeconds = ptr.To(int32(120))
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("user strategy not preserved: got %q", dep.Spec.Strategy.Type)
	}
	if dep.Spec.ProgressDeadlineSeconds == nil || *dep.Spec.ProgressDeadlineSeconds != 120 {
		t.Errorf("user progressDeadlineSeconds not preserved: got %v", dep.Spec.ProgressDeadlineSeconds)
	}
}

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

// #45: the pull policy is set explicitly (mirroring k8s' implicit rule) so a
// deploy is reproducible — :latest / no tag / unknown → Always, a fixed tag or
// digest → IfNotPresent. A registry host:port must not be mistaken for a tag.
func TestDefaultPullPolicy(t *testing.T) {
	cases := []struct {
		image string
		want  apiv1.PullPolicy
	}{
		{"repo:latest", apiv1.PullAlways},
		{"repo:v1", apiv1.PullIfNotPresent},
		{"repo", apiv1.PullAlways},
		{"repo@sha256:abc", apiv1.PullIfNotPresent},
		{"registry.io:5000/app:v1", apiv1.PullIfNotPresent},
		{"registry.io:5000/app", apiv1.PullAlways},
	}
	for _, c := range cases {
		if got := defaultPullPolicy(c.image); got != c.want {
			t.Errorf("defaultPullPolicy(%q): got %q, want %q", c.image, got, c.want)
		}
	}
}

// #45: GetDeployment fills an explicit ImagePullPolicy when none is set.
func TestGetDeploymentPullPolicyDefault(t *testing.T) {
	app := deployApp(t) // container image example/app:v1
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if got := dep.Spec.Template.Spec.Containers[0].ImagePullPolicy; got != apiv1.PullIfNotPresent {
		t.Errorf("pull policy for :v1: got %q, want IfNotPresent", got)
	}
}

// #47: a PodDisruptionBudget is emitted only when the Deployment runs more than
// one replica (a single-replica minAvailable:1 PDB would block every drain). It
// must carry the same stable selector as the Deployment.
func TestGetPodDisruptionBudget(t *testing.T) {
	app := deployApp(t)

	pdb, err := app.GetPodDisruptionBudget()
	if err != nil {
		t.Fatalf("GetPodDisruptionBudget: %v", err)
	}
	if pdb != nil {
		t.Errorf("single-replica deploy must emit no PDB, got %+v", pdb)
	}

	app.Deployment.ReplicaCount = ptr.To(int32(3))
	pdb, err = app.GetPodDisruptionBudget()
	if err != nil {
		t.Fatalf("GetPodDisruptionBudget: %v", err)
	}
	if pdb == nil {
		t.Fatal("multi-replica deploy must emit a PDB")
	}
	if pdb.Spec.MinAvailable == nil || pdb.Spec.MinAvailable.IntValue() != 1 {
		t.Errorf("minAvailable: got %v, want 1", pdb.Spec.MinAvailable)
	}
	if pdb.Spec.Selector == nil || pdb.Spec.Selector.MatchLabels[LabelName] != app.Labels[LabelName] {
		t.Errorf("PDB selector must match the deployment: %+v", pdb.Spec.Selector)
	}

	// It renders via the manifest registry under "all".
	m, err := app.GetManifest("yaml", OutputAll)
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if !strings.Contains(m, "PodDisruptionBudget") {
		t.Errorf("multi-replica manifest must include a PodDisruptionBudget")
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
// Regression (#11): app.Volumes are mounted only on app-image containers, so a
// cronjob whose only container is a third-party image must not carry a dangling
// PVC volume that nothing mounts; an app-image cronjob still gets it.
func TestGetCronJobsPVCVolumeOnlyWhenMounted(t *testing.T) {
	// App-image cron: the volume is mounted and present.
	app := cronApp(t)
	app.Volumes = map[string]VolumeSpec{"data": {MountPath: "/data"}}
	crons, err := app.GetCronJobs()
	if err != nil {
		t.Fatalf("GetCronJobs: %v", err)
	}
	var appHasData bool
	for _, v := range crons[0].Spec.JobTemplate.Spec.Template.Spec.Volumes {
		if v.Name == "data" {
			appHasData = true
		}
	}
	if !appHasData {
		t.Errorf("app-image cronjob must carry the mounted PVC volume 'data'")
	}

	// Third-party-image cron: nothing mounts the volume, so it must be omitted.
	tp := cronApp(t)
	tp.Common.Image.Repository = "example/app"
	tp.Common.Image.Tag = "v1"
	tp.Volumes = map[string]VolumeSpec{"data": {MountPath: "/data"}}
	tp.Cronjob = map[string]CronjobSpec{
		"backup": {Schedule: "* * * * *", Container: apiv1.Container{Image: "busybox:latest", Command: []string{"echo"}}},
	}
	crons, err = tp.GetCronJobs()
	if err != nil {
		t.Fatalf("GetCronJobs (third-party): %v", err)
	}
	for _, v := range crons[0].Spec.JobTemplate.Spec.Template.Spec.Volumes {
		if v.Name == "data" {
			t.Errorf("third-party cronjob must not carry the unmounted PVC volume 'data'")
		}
	}
}

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

// Regression: spec.selector is immutable, and existing deployments created by
// pre-v0.7 app2kube carry the full label set (GetColorLabels) in their selector.
// To upgrade those in place, the rendered selector must stay byte-identical to
// the pod template labels (the full set, incl. managed-by and user labels) — a
// narrower selector would make `kubectl apply` reject the update as immutable.
func TestDeploymentSelectorMatchesPodTemplateLabels(t *testing.T) {
	app := deployApp(t)
	app.Labels[LabelName] = "example"
	app.Labels["team"] = "payments" // arbitrary user label

	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	sel := dep.Spec.Selector.MatchLabels
	tmpl := dep.Spec.Template.Labels

	// Selector and pod template labels must be identical for backward compat.
	if len(sel) != len(tmpl) {
		t.Fatalf("selector and pod template labels differ: sel=%+v tmpl=%+v", sel, tmpl)
	}
	for k, v := range tmpl {
		if sel[k] != v {
			t.Errorf("selector must match pod template label %q: sel=%+v tmpl=%+v", k, sel, tmpl)
		}
	}
	// The full label set is present: name, instance, managed-by and user labels.
	if sel[LabelName] != "example" || sel[LabelInstance] != "production" {
		t.Errorf("selector must keep name+instance: %+v", sel)
	}
	if sel[LabelManagedBy] == "" {
		t.Errorf("selector must keep managed-by for backward compat: %+v", sel)
	}
	if sel["team"] != "payments" {
		t.Errorf("selector must keep user labels for backward compat: %+v", sel)
	}
}

// Regression: the common.resources baseline is applied to each app-image
// container via a deep copy, so containers (and app.Common.Resources) get
// independent Requests/Limits maps instead of aliasing one shared map.
func TestCommonResourcesNotAliasedAcrossContainers(t *testing.T) {
	app := deployApp(t)
	app.Deployment.Containers = map[string]apiv1.Container{
		"a": {Image: "example/app:v1"},
		"b": {Image: "example/app:v1"},
	}
	app.Common.Resources = &apiv1.ResourceRequirements{
		Requests: apiv1.ResourceList{apiv1.ResourceCPU: resource.MustParse("100m")},
	}

	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(containers))
	}

	// Mutating one container's Requests must not leak into the other container
	// or the shared app.Common.Resources.
	containers[0].Resources.Requests[apiv1.ResourceMemory] = resource.MustParse("64Mi")
	if _, ok := containers[1].Resources.Requests[apiv1.ResourceMemory]; ok {
		t.Errorf("containers share the same Requests map (aliasing)")
	}
	if _, ok := app.Common.Resources.Requests[apiv1.ResourceMemory]; ok {
		t.Errorf("container mutated the shared app.Common.Resources map (aliasing)")
	}
}

// Regression: podSecurityContext returns an independent deep copy of the
// user-supplied common.securityContext, so the Deployment and CronJob pod specs
// do not alias one struct.
func TestPodSecurityContextNotAliased(t *testing.T) {
	app := deployApp(t)
	app.Common.SecurityContext = &apiv1.PodSecurityContext{RunAsUser: ptr.To(int64(1000))}

	a := app.podSecurityContext()
	b := app.podSecurityContext()
	a.RunAsUser = ptr.To(int64(2000))

	if b.RunAsUser == nil || *b.RunAsUser != 1000 {
		t.Errorf("podSecurityContext must return independent copies, got b=%v", b.RunAsUser)
	}
	if *app.Common.SecurityContext.RunAsUser != 1000 {
		t.Errorf("podSecurityContext must not alias app.Common.SecurityContext")
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

// Regression (#6): the rendered container list must be ordered deterministically.
// Ranging app.Deployment.Containers (a map) without sorting yields a different
// order on each render, which kubectl sees as a pod-template change and rolls the
// Deployment on every apply.
func TestDeploymentContainerOrderDeterministic(t *testing.T) {
	app := deployApp(t)
	app.Deployment.Containers = map[string]apiv1.Container{
		"zeta":  {Image: "example/app:v1"},
		"alpha": {Image: "example/app:v1"},
		"mid":   {Image: "example/app:v1"},
	}
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	var got []string
	for _, c := range dep.Spec.Template.Spec.Containers {
		got = append(got, c.Name)
	}
	want := []string{"alpha", "mid", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("container order: got %v, want %v (sorted by name)", got, want)
	}
}

// Regression (#6): init containers must also render in a stable order.
func TestDeploymentInitContainerOrderDeterministic(t *testing.T) {
	app := deployApp(t)
	app.Deployment.InitContainers = map[string]apiv1.Container{
		"zinit": {Image: "example/app:v1", Command: []string{"a"}},
		"ainit": {Image: "example/app:v1", Command: []string{"a"}},
	}
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	var got []string
	for _, c := range dep.Spec.Template.Spec.InitContainers {
		got = append(got, c.Name)
	}
	want := []string{"ainit", "zinit"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("init container order: got %v, want %v (sorted by name)", got, want)
	}
}

// Regression (#6): both the pod-level PVC volumes and each container's
// volumeMounts must render in a stable order — either reordering changes the pod
// template and rolls the workload on every apply.
func TestDeploymentVolumeOrderDeterministic(t *testing.T) {
	rwo := apiv1.PersistentVolumeClaimSpec{
		AccessModes: []apiv1.PersistentVolumeAccessMode{apiv1.ReadWriteOnce},
	}
	app := deployApp(t)
	app.Volumes = map[string]VolumeSpec{
		"zdata": {MountPath: "/z", Spec: rwo},
		"adata": {MountPath: "/a", Spec: rwo},
		"mdata": {MountPath: "/m", Spec: rwo},
	}
	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	want := []string{"adata", "mdata", "zdata"}

	var vols []string
	for _, v := range dep.Spec.Template.Spec.Volumes {
		vols = append(vols, v.Name)
	}
	if !reflect.DeepEqual(vols, want) {
		t.Errorf("pod volume order: got %v, want %v (sorted)", vols, want)
	}

	var mounts []string
	for _, m := range dep.Spec.Template.Spec.Containers[0].VolumeMounts {
		mounts = append(mounts, m.Name)
	}
	if !reflect.DeepEqual(mounts, want) {
		t.Errorf("container volumeMounts order: got %v, want %v (sorted)", mounts, want)
	}
}

// Regression (#6): the cronjob pod template containers must render in a stable
// order too (mirrors the Deployment).
func TestCronJobContainerOrderDeterministic(t *testing.T) {
	app := deployApp(t)
	app.Cronjob = map[string]CronjobSpec{
		"job": {
			Schedule: "* * * * *",
			Containers: map[string]apiv1.Container{
				"zeta":  {Image: "example/app:v1"},
				"alpha": {Image: "example/app:v1"},
			},
		},
	}
	crons, err := app.GetCronJobs()
	if err != nil {
		t.Fatalf("GetCronJobs: %v", err)
	}
	if len(crons) != 1 {
		t.Fatalf("expected 1 cronjob, got %d", len(crons))
	}
	var got []string
	for _, c := range crons[0].Spec.JobTemplate.Spec.Template.Spec.Containers {
		got = append(got, c.Name)
	}
	want := []string{"alpha", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cron container order: got %v, want %v (sorted by name)", got, want)
	}
}
