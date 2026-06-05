package app2kube

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/ghodss/yaml"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func mustUnmarshalApp(t *testing.T, y string) *App {
	t.Helper()
	app := NewApp()
	if err := yaml.Unmarshal([]byte(y), app); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return app
}

// Regression: a cronjob with multiple named containers (the `containers:` map,
// mirroring deployment) was silently dropped because the field shared the
// `container` yaml tag with the single-container field.
func TestCronjobMultipleContainers(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: example
cronjob:
  backup:
    schedule: "* * * * *"
    containers:
      one:
        image: example/one:v1
      two:
        image: example/two:v1
`)
	crons, err := app.GetCronJobs()
	if err != nil {
		t.Fatalf("GetCronJobs: %v", err)
	}
	if len(crons) != 1 {
		t.Fatalf("expected 1 cronjob, got %d", len(crons))
	}
	containers := crons[0].Spec.JobTemplate.Spec.Template.Spec.Containers
	if len(containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(containers))
	}
}

// Regression: a cronjob `container` block specified without a command used to be
// silently dropped (the include gate keyed on Command being non-empty), so a
// user who set only image/args ended up with a CronJob missing that container.
// It must now fail loudly instead.
func TestCronjobContainerWithoutCommandErrors(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: example
cronjob:
  backup:
    schedule: "* * * * *"
    container:
      image: example/app:v1
`)
	if _, err := app.GetCronJobs(); err == nil {
		t.Fatal("expected an error for a cronjob 'container' without a command")
	}
}

// Regression: a cronjob with neither `container` nor `containers` produced a
// CronJob with zero containers (an invalid manifest) instead of an error.
func TestCronjobWithoutContainersErrors(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: example
cronjob:
  backup:
    schedule: "* * * * *"
`)
	if _, err := app.GetCronJobs(); err == nil {
		t.Fatal("expected an error for a cronjob with no containers")
	}
}

// The single `container` and the `containers` map coexist in one cronjob pod:
// the single container is emitted first (named "<cronjob>-job"), then the named
// containers in sorted key order. Mirrors values_cronjob.yaml, which uses both.
func TestCronjobContainerAndContainersMerged(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: example
cronjob:
  joba:
    schedule: "* * * * *"
    container:
      image: example/app:v1
      command: [run]
    containers:
      cli2:
        image: example/cli:v1
        command: [two]
      cli1:
        image: example/cli:v1
        command: [one]
`)
	crons, err := app.GetCronJobs()
	if err != nil {
		t.Fatalf("GetCronJobs: %v", err)
	}
	containers := crons[0].Spec.JobTemplate.Spec.Template.Spec.Containers
	if len(containers) != 3 {
		t.Fatalf("expected 3 containers (single + 2 named), got %d", len(containers))
	}
	// Single container first (default name), then named containers sorted by key.
	want := []string{"joba-job", "cli1", "cli2"}
	for i, w := range want {
		if containers[i].Name != w {
			t.Errorf("container[%d]: got %q, want %q", i, containers[i].Name, w)
		}
	}
}

// Regression: zero values for cronjob limits must be honored (e.g. backoffLimit:0
// to disable retries) instead of being treated as "unset" and overridden.
func TestCronjobZeroLimitsHonored(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: example
cronjob:
  backup:
    schedule: "* * * * *"
    backoffLimit: 0
    failedJobsHistoryLimit: 0
    successfulJobsHistoryLimit: 0
    activeDeadlineSeconds: 0
    container:
      image: example/app:v1
      command: [echo]
`)
	crons, err := app.GetCronJobs()
	if err != nil {
		t.Fatalf("GetCronJobs: %v", err)
	}
	spec := crons[0].Spec
	if got := *spec.JobTemplate.Spec.BackoffLimit; got != 0 {
		t.Errorf("BackoffLimit: expected 0, got %d", got)
	}
	if got := *spec.FailedJobsHistoryLimit; got != 0 {
		t.Errorf("FailedJobsHistoryLimit: expected 0, got %d", got)
	}
	if got := *spec.SuccessfulJobsHistoryLimit; got != 0 {
		t.Errorf("SuccessfulJobsHistoryLimit: expected 0, got %d", got)
	}
	if got := *spec.JobTemplate.Spec.ActiveDeadlineSeconds; got != 0 {
		t.Errorf("ActiveDeadlineSeconds: expected 0, got %d", got)
	}
}

// Defaults must still apply when limits are omitted entirely.
func TestCronjobDefaultLimits(t *testing.T) {
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
	spec := crons[0].Spec
	if got := *spec.JobTemplate.Spec.BackoffLimit; got != 6 {
		t.Errorf("BackoffLimit default: expected 6, got %d", got)
	}
	if got := *spec.FailedJobsHistoryLimit; got != 2 {
		t.Errorf("FailedJobsHistoryLimit default: expected 2, got %d", got)
	}
	if got := *spec.JobTemplate.Spec.ActiveDeadlineSeconds; got != 86400 {
		t.Errorf("ActiveDeadlineSeconds default: expected 86400, got %d", got)
	}
}

// Regression: malformed AES ciphertext must return an error, not panic.
func TestDecryptAESMalformed(t *testing.T) {
	cases := []string{
		base64.StdEncoding.EncodeToString([]byte("short")),  // shorter than IV
		base64.StdEncoding.EncodeToString(make([]byte, 16)), // only IV, no data
		base64.StdEncoding.EncodeToString(make([]byte, 20)), // not block-aligned
		base64.StdEncoding.EncodeToString(make([]byte, 32)), // zero block -> invalid padding
		"not-base64-!!!", // invalid base64
	}
	for _, c := range cases {
		if _, err := DecryptAES("password", c); err == nil {
			t.Errorf("expected error for malformed ciphertext %q", c)
		}
	}
}

// AES round-trip must still succeed after adding padding validation.
func TestDecryptAESRoundTrip(t *testing.T) {
	enc, err := EncryptAES("password", "topsecret")
	if err != nil {
		t.Fatalf("EncryptAES: %v", err)
	}
	dec, err := DecryptAES("password", enc)
	if err != nil {
		t.Fatalf("DecryptAES: %v", err)
	}
	if dec != "topsecret" {
		t.Errorf("expected 'topsecret', got %q", dec)
	}
}

// Regression: an image whose repository contains a registry port (or a digest)
// must be recognized as the app's own image so env/secrets are injected.
func TestProcessContainerRegistryPort(t *testing.T) {
	app := NewApp()
	app.Common.Image.Repository = "registry.io:5000/app"
	app.Common.Image.Tag = "v2"
	app.Env = map[string]string{"FOO": "bar"}

	cases := []struct {
		name      string
		image     string
		wantImage string
		wantEnv   bool
	}{
		{"registry port + tag", "registry.io:5000/app:v1", "registry.io:5000/app:v2", true},
		{"registry port + digest", "registry.io:5000/app@sha256:" + zeros(64), "registry.io:5000/app:v2", true},
		{"bare repository", "registry.io:5000/app", "registry.io:5000/app:v2", true},
		{"third party image", "other.io/lib:1.0", "other.io/lib:1.0", false},
		{"prefix collision is not a match", "registry.io:5000/app-extra:v1", "registry.io:5000/app-extra:v1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &apiv1.Container{Name: "app", Image: tc.image}
			if err := app.processContainer(c, false); err != nil {
				t.Fatalf("processContainer: %v", err)
			}
			if c.Image != tc.wantImage {
				t.Errorf("image: expected %q, got %q", tc.wantImage, c.Image)
			}
			hasEnv := len(c.Env) > 0
			if hasEnv != tc.wantEnv {
				t.Errorf("env injected=%v, want %v", hasEnv, tc.wantEnv)
			}
		})
	}
}

// Regression: a named (string) probe port must not be overwritten by the
// numeric container port; an unset numeric port must still be filled.
func TestProbeNamedPortPreserved(t *testing.T) {
	app := NewApp()

	named := &apiv1.Container{
		Name:  "app",
		Image: "example/app:v1",
		Ports: []apiv1.ContainerPort{{ContainerPort: 8080}},
		LivenessProbe: &apiv1.Probe{ProbeHandler: apiv1.ProbeHandler{
			HTTPGet: &apiv1.HTTPGetAction{Port: intstr.FromString("http")},
		}},
	}
	if err := app.processContainer(named, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if got := named.LivenessProbe.HTTPGet.Port; got.StrVal != "http" {
		t.Errorf("named probe port overwritten: %+v", got)
	}

	unset := &apiv1.Container{
		Name:  "app",
		Image: "example/app:v1",
		Ports: []apiv1.ContainerPort{{ContainerPort: 8080}},
		LivenessProbe: &apiv1.Probe{ProbeHandler: apiv1.ProbeHandler{
			HTTPGet: &apiv1.HTTPGetAction{},
		}},
	}
	if err := app.processContainer(unset, false); err != nil {
		t.Fatalf("processContainer: %v", err)
	}
	if got := unset.LivenessProbe.HTTPGet.Port.IntVal; got != 8080 {
		t.Errorf("unset probe port not filled: got %d, want 8080", got)
	}
}

// Regression: NodePort must only be pinned when ExternalPort is a valid node
// port; otherwise it is left for the apiserver to auto-assign.
func TestServiceNodePortRange(t *testing.T) {
	newApp := func() *App {
		app := NewApp()
		app.Name = "example"
		app.Deployment.Containers = map[string]apiv1.Container{"app": {}}
		return app
	}

	app := newApp()
	app.Service = map[string]Service{"web": {Port: 80, Type: apiv1.ServiceTypeNodePort}}
	svcs, err := app.GetServices()
	if err != nil {
		t.Fatalf("GetServices: %v", err)
	}
	if got := svcs[0].Spec.Ports[0].NodePort; got != 0 {
		t.Errorf("NodePort for port 80: expected unset (0), got %d", got)
	}

	app = newApp()
	app.Service = map[string]Service{"web": {Port: 80, ExternalPort: 30080, Type: apiv1.ServiceTypeNodePort}}
	svcs, err = app.GetServices()
	if err != nil {
		t.Fatalf("GetServices: %v", err)
	}
	if got := svcs[0].Spec.Ports[0].NodePort; got != 30080 {
		t.Errorf("NodePort for valid 30080: expected 30080, got %d", got)
	}
}

// #49: the node-port range gate is a pure predicate so the Service generator and
// its warning share one definition of "valid node port".
func TestNodePortInRange(t *testing.T) {
	for _, p := range []int32{30000, 31000, 32767} {
		if !nodePortInRange(p) {
			t.Errorf("%d must be in the node-port range", p)
		}
	}
	for _, p := range []int32{0, 80, 29999, 32768} {
		if nodePortInRange(p) {
			t.Errorf("%d must be outside the node-port range", p)
		}
	}
}

// Regression (#16): a service defined with only internalPort renders a valid
// Service (external mirrors internal), but GetIngress used to read only
// ExternalPort/Port and never InternalPort, so it failed with "you must specify
// a servicePort". The ingress backend port must fall back to InternalPort,
// mirroring the Service generator.
func TestIngressBackendPortFromInternalPort(t *testing.T) {
	app := ingressTestApp()
	app.Service = map[string]Service{"web": {InternalPort: 8080}}
	app.Ingress = []Ingress{{Host: "example.com"}}

	ings, err := app.GetIngress()
	if err != nil {
		t.Fatalf("GetIngress: %v", err)
	}
	if len(ings) != 1 {
		t.Fatalf("expected 1 ingress, got %d", len(ings))
	}
	got := ings[0].Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port.Number
	if got != 8080 {
		t.Errorf("ingress backend port: got %d, want 8080", got)
	}
}

// Regression (#15): when the same alias appears on multiple entries of one host,
// the alias must be a single IngressRule accumulating all paths — not one
// duplicate rule per entry each carrying a single path.
func TestIngressAliasDedupAndPathAccumulation(t *testing.T) {
	app := ingressTestApp()
	app.Ingress = []Ingress{
		{Host: "example.com", Path: "/a", Aliases: []string{"www.example.com"}},
		{Host: "example.com", Path: "/b", Aliases: []string{"www.example.com"}},
	}

	ings, err := app.GetIngress()
	if err != nil {
		t.Fatalf("GetIngress: %v", err)
	}
	ing := ings[0]

	aliasRules, aliasPaths := 0, 0
	for _, r := range ing.Spec.Rules {
		if r.Host == "www.example.com" {
			aliasRules++
			aliasPaths = len(r.HTTP.Paths)
		}
	}
	if aliasRules != 1 {
		t.Errorf("alias must be a single rule, got %d", aliasRules)
	}
	if aliasPaths != 2 {
		t.Errorf("alias rule must accumulate both paths, got %d", aliasPaths)
	}
}

func ingressTestApp() *App {
	app := NewApp()
	app.Name = "example"
	app.Deployment.Containers = map[string]apiv1.Container{"app": {}}
	app.Service = map[string]Service{"web": {Port: 80}}
	return app
}

// Regression: a TLS Secret is emitted for both a letsencrypt ingress (an empty
// kubernetes.io/tls placeholder, later populated by cert-manager) and an ingress
// carrying inline certificate material. The placeholder must exist so it stays
// in the apply set and `apply --prune` does not delete a cert-manager-populated
// Secret created by an earlier release (see
// TestIngressLetsencryptSecretEmittedForPrune).
func TestIngressSecretsTLSMaterialOnly(t *testing.T) {
	app := ingressTestApp()
	app.Ingress = []Ingress{
		{Host: "le.example.com", IngressCommon: IngressCommon{Letsencrypt: true}},
		{Host: "cert.example.com", TLSCrt: "CRT", TLSKey: "KEY"},
	}
	secrets, err := app.GetIngressSecrets()
	if err != nil {
		t.Fatalf("GetIngressSecrets: %v", err)
	}
	if len(secrets) != 2 {
		t.Fatalf("expected 2 TLS secrets (letsencrypt placeholder + inline cert), got %d", len(secrets))
	}
	byName := map[string]*apiv1.Secret{}
	for _, s := range secrets {
		byName[s.Name] = s
	}
	le := byName["tls-le.example.com"]
	if le == nil {
		t.Fatalf("missing letsencrypt placeholder secret, got %v", byName)
	}
	if le.Type != apiv1.SecretTypeTLS {
		t.Errorf("letsencrypt secret type: got %q, want kubernetes.io/tls", le.Type)
	}
	if len(le.Data["tls.crt"]) != 0 || len(le.Data["tls.key"]) != 0 {
		t.Errorf("letsencrypt placeholder must carry empty cert material, got %v", le.Data)
	}
	cert := byName["tls-cert.example.com"]
	if cert == nil || string(cert.Data["tls.crt"]) != "CRT" || string(cert.Data["tls.key"]) != "KEY" {
		t.Errorf("unexpected inline TLS secret data: %v", byName)
	}
}

// With letsencrypt, app2kube wires the Ingress for cert-manager via an explicit
// cert-manager.io/v1 Certificate (NOT the legacy kubernetes.io/tls-acme
// annotation): the Ingress carries a TLS block referencing the secret name, a
// Certificate of the same name requests the cert from the default ClusterIssuer,
// and an empty placeholder Secret of that name keeps prune from orphaning the
// cert cert-manager later writes into it.
func TestIngressLetsencryptTriggersCertManager(t *testing.T) {
	app := ingressTestApp()
	app.Ingress = []Ingress{{Host: "le.example.com", IngressCommon: IngressCommon{Letsencrypt: true}}}

	ings, err := app.GetIngress()
	if err != nil {
		t.Fatalf("GetIngress: %v", err)
	}
	if len(ings) != 1 {
		t.Fatalf("expected 1 ingress, got %d", len(ings))
	}
	if _, ok := ings[0].Annotations["kubernetes.io/tls-acme"]; ok {
		t.Errorf("letsencrypt ingress must NOT carry the legacy kubernetes.io/tls-acme annotation, got %v", ings[0].Annotations)
	}
	if len(ings[0].Spec.TLS) != 1 || ings[0].Spec.TLS[0].SecretName != "tls-le.example.com" {
		t.Errorf("letsencrypt ingress must reference the TLS secret cert-manager fills, got %+v", ings[0].Spec.TLS)
	}

	certs, err := app.GetCertificates()
	if err != nil {
		t.Fatalf("GetCertificates: %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("letsencrypt must emit one Certificate, got %d", len(certs))
	}
	cert := certs[0]
	if cert.APIVersion != "cert-manager.io/v1" || cert.Kind != "Certificate" {
		t.Errorf("Certificate TypeMeta: got %s/%s, want cert-manager.io/v1/Certificate", cert.APIVersion, cert.Kind)
	}
	if cert.Name != "tls-le.example.com" || cert.Spec.SecretName != "tls-le.example.com" {
		t.Errorf("Certificate name/secretName must match the placeholder secret tls-le.example.com, got name=%q secretName=%q", cert.Name, cert.Spec.SecretName)
	}
	if len(cert.Spec.DNSNames) != 1 || cert.Spec.DNSNames[0] != "le.example.com" {
		t.Errorf("Certificate dnsNames: got %v, want [le.example.com]", cert.Spec.DNSNames)
	}
	wantIssuer := IssuerReference{Name: "letsencrypt-prod", Kind: "ClusterIssuer", Group: "cert-manager.io"}
	if cert.Spec.IssuerRef != wantIssuer {
		t.Errorf("Certificate issuerRef: got %+v, want %+v", cert.Spec.IssuerRef, wantIssuer)
	}

	secrets, err := app.GetIngressSecrets()
	if err != nil {
		t.Fatalf("GetIngressSecrets: %v", err)
	}
	if len(secrets) != 1 || secrets[0].Name != "tls-le.example.com" {
		t.Fatalf("letsencrypt must emit one placeholder secret named tls-le.example.com, got %+v", secrets)
	}
	if secrets[0].Type != apiv1.SecretTypeTLS {
		t.Errorf("placeholder secret type: got %q, want kubernetes.io/tls", secrets[0].Type)
	}
}

// Regression (#1, backward-compat): an earlier app2kube release emitted the
// letsencrypt `tls-<host>` Secret under the app's recommended labels; cert-manager
// then populated it. The Secret therefore matches the `apply --prune` label
// selector, so if the current release stops emitting it, prune deletes the live
// certificate on the first upgrade. The placeholder must be emitted and must
// carry the app labels so it is recognised as part of the app's manifest set.
func TestIngressLetsencryptSecretEmittedForPrune(t *testing.T) {
	app := ingressTestApp()
	app.ensureLabels()
	app.Labels[LabelName] = truncateName(app.Name)
	app.Ingress = []Ingress{{Host: "le.example.com", IngressCommon: IngressCommon{Letsencrypt: true}}}

	secrets, err := app.GetIngressSecrets()
	if err != nil {
		t.Fatalf("GetIngressSecrets: %v", err)
	}
	if len(secrets) != 1 {
		t.Fatalf("letsencrypt must emit a prune-protecting placeholder secret, got %d", len(secrets))
	}
	for _, k := range []string{LabelManagedBy, LabelInstance, LabelName} {
		if _, ok := secrets[0].Labels[k]; !ok {
			t.Errorf("placeholder secret missing prune-selector label %q: %v", k, secrets[0].Labels)
		}
	}
}

// Object names combining the release name with a user-controlled key must stay
// within the apiserver's per-kind limit: a Service name is a DNS-1123 label
// (63); PVC/ConfigMap/Secret/Deployment/Ingress names are DNS-1123 subdomains
// (253) and must NOT be shortened to 63 (which would diverge from the name
// earlier releases emitted and orphan the live object on apply); a CronJob name
// is capped at 52 (the controller appends a suffix to the Job it spawns).
func TestObjectNamesWithinLimits(t *testing.T) {
	longKey := strings.Repeat("b", 40)

	app := NewApp()
	app.Name = strings.Repeat("a", 50)

	// Service: DNS-1123 label limit (63).
	if got := app.GetServiceName(longKey); len(got) > MaxNameLength {
		t.Errorf("service name over label limit: len=%d (%q)", len(got), got)
	}

	// PVC: subdomain limit (253) — the full name is kept, not shortened to 63.
	wantClaim := strings.ToLower(app.GetReleaseName() + "-" + longKey)
	if got := app.GetVolumeClaimName(longKey); got != wantClaim {
		t.Errorf("claim name must be kept in full: got %q, want %q", got, wantClaim)
	}
	if got := app.GetVolumeClaimName(longKey); len(got) <= MaxNameLength || len(got) > MaxSubdomainNameLength {
		t.Errorf("claim name must use the subdomain limit, not 63: len=%d (%q)", len(got), got)
	}

	// The PVC object name and the pod volume reference must use the same helper,
	// so they stay byte-identical.
	app.Volumes = map[string]VolumeSpec{
		longKey: {MountPath: "/data", Spec: apiv1.PersistentVolumeClaimSpec{
			AccessModes: []apiv1.PersistentVolumeAccessMode{apiv1.ReadWriteOnce},
		}},
	}
	claims, err := app.GetPersistentVolumeClaims()
	if err != nil {
		t.Fatalf("GetPersistentVolumeClaims: %v", err)
	}
	if claims[0].Name != app.GetVolumeClaimName(longKey) {
		t.Errorf("PVC object name %q must equal GetVolumeClaimName %q", claims[0].Name, app.GetVolumeClaimName(longKey))
	}

	// CronJob: capped at 52.
	app.Common.Image.Repository = "example/app"
	app.Cronjob = map[string]CronjobSpec{
		longKey: {Schedule: "* * * * *", Container: apiv1.Container{Image: "example/app:v1", Command: []string{"true"}}},
	}
	crons, err := app.GetCronJobs()
	if err != nil {
		t.Fatalf("GetCronJobs: %v", err)
	}
	if len(crons[0].Name) > MaxCronJobNameLength {
		t.Errorf("cronjob name over %d-char limit: len=%d (%q)", MaxCronJobNameLength, len(crons[0].Name), crons[0].Name)
	}

	// Ingress object name uses the 253-char DNS subdomain limit, so a long name
	// is kept in full (not shortened to 63) — byte-identical to earlier releases.
	app.Deployment.Containers = map[string]apiv1.Container{"app": {}}
	app.Service = map[string]Service{"web": {Port: 80}}
	longHost := strings.Repeat("h", 60) + ".example.com"
	app.Ingress = []Ingress{{Host: longHost}}
	ings, err := app.GetIngress()
	if err != nil {
		t.Fatalf("GetIngress: %v", err)
	}
	wantIngName := strings.ToLower(app.Name + "-" + longHost)
	if ings[0].Name != wantIngName {
		t.Errorf("ingress name must be kept in full (not truncated to 63): got %q, want %q", ings[0].Name, wantIngName)
	}
	if len(ings[0].Name) > MaxSubdomainNameLength {
		t.Errorf("ingress name over subdomain limit %d: len=%d (%q)", MaxSubdomainNameLength, len(ings[0].Name), ings[0].Name)
	}
}

// Regression: a long app name must yield apiserver-valid, mutually-consistent
// names for EVERY object. GetReleaseName backs the ConfigMap/Secret names (and
// their envFrom refs) and GetDeploymentName backs the Deployment/PDB names;
// these are DNS-1123 subdomains, so they are kept up to 253 chars (NOT shortened
// to the 63-char label limit, which would orphan the live object) and stay
// byte-identical to each other instead of splitting the app across mismatched
// names.
func TestLongNameAllObjectNamesValid(t *testing.T) {
	app := NewApp()
	app.Name = strings.Repeat("a", 70)
	app.Namespace = "default"
	app.ensureLabels()
	app.Labels[LabelName] = truncateName(app.Name)
	app.Deployment.Containers = map[string]apiv1.Container{"app": {Image: "x/y:1"}}
	app.Deployment.ReplicaCount = ptr.To(int32(2))
	app.Deployment.BlueGreenColor = "green"
	app.ConfigMap = map[string]string{"K": "v"}
	app.Secrets = map[string]string{"s": "v"}

	// The release/deployment names are subdomains: kept in full (here 70+ chars,
	// not shortened to the 63-char label limit) and within the 253-char limit.
	if n := app.GetReleaseName(); len(n) <= MaxNameLength || len(n) > MaxSubdomainNameLength {
		t.Errorf("release name: len=%d (%q), want kept full within %d", len(n), n, MaxSubdomainNameLength)
	}
	if n := app.GetDeploymentName(); len(n) <= MaxNameLength || len(n) > MaxSubdomainNameLength {
		t.Errorf("deployment name: len=%d (%q), want kept full within %d", len(n), n, MaxSubdomainNameLength)
	}

	dep, err := app.GetDeployment()
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if len(dep.Name) > MaxSubdomainNameLength || dep.Name != app.GetDeploymentName() {
		t.Errorf("deployment object name %q (len %d)", dep.Name, len(dep.Name))
	}

	pdb, err := app.GetPodDisruptionBudget()
	if err != nil {
		t.Fatalf("GetPodDisruptionBudget: %v", err)
	}
	if pdb == nil || pdb.Name != dep.Name {
		t.Errorf("PDB name must match the Deployment name; got %+v", pdb)
	}

	cm, err := app.GetConfigMap()
	if err != nil {
		t.Fatalf("GetConfigMap: %v", err)
	}
	if cm.Name != app.GetReleaseName() || len(cm.Name) > MaxSubdomainNameLength {
		t.Errorf("configmap object name %q (len %d)", cm.Name, len(cm.Name))
	}

	sec, err := app.GetSecret()
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if sec.Name != app.GetReleaseName() || len(sec.Name) > MaxSubdomainNameLength {
		t.Errorf("secret object name %q (len %d)", sec.Name, len(sec.Name))
	}
}

// Regression: when the same host is described by multiple ingress entries, paths
// must accumulate on that host's rule only, and aliases must attach to the TLS
// entry created in their own iteration (not a hardcoded TLS[0]).
func TestIngressPathsAndAliasTLS(t *testing.T) {
	app := ingressTestApp()
	app.Ingress = []Ingress{
		{Host: "example.com", Path: "/a", TLSCrt: "C", TLSKey: "K"},
		{Host: "example.com", Path: "/b", TLSCrt: "C", TLSKey: "K", Aliases: []string{"www.example.com"}},
	}
	ings, err := app.GetIngress()
	if err != nil {
		t.Fatalf("GetIngress: %v", err)
	}
	if len(ings) != 1 {
		t.Fatalf("expected 1 ingress, got %d", len(ings))
	}
	ing := ings[0]

	var hostPaths int
	for _, r := range ing.Spec.Rules {
		if r.Host == "example.com" {
			hostPaths = len(r.HTTP.Paths)
		}
	}
	if hostPaths != 2 {
		t.Errorf("expected 2 paths on example.com rule, got %d", hostPaths)
	}

	aliasFound := false
	for _, tls := range ing.Spec.TLS {
		for _, h := range tls.Hosts {
			if h == "www.example.com" {
				aliasFound = true
			}
		}
	}
	if !aliasFound {
		t.Errorf("alias www.example.com not attached to any TLS entry: %+v", ing.Spec.TLS)
	}
}

// Regression (#13/#14): the TLS Secret name must be byte-identical between the
// Ingress TLS reference and the emitted Secret object, and every host-derived
// name (Ingress object + TLS Secret) must be lowercased so it stays DNS-1123
// valid even when the host carries uppercase letters.
func TestIngressTLSNamesLowercasedAndConsistent(t *testing.T) {
	app := ingressTestApp()
	app.Ingress = []Ingress{
		{Host: "Cert.Example.COM", TLSCrt: "CRT", TLSKey: "KEY"},
	}

	ings, err := app.GetIngress()
	if err != nil {
		t.Fatalf("GetIngress: %v", err)
	}
	if len(ings) != 1 {
		t.Fatalf("expected 1 ingress, got %d", len(ings))
	}
	if len(ings[0].Spec.TLS) != 1 {
		t.Fatalf("expected 1 TLS block, got %d", len(ings[0].Spec.TLS))
	}
	refName := ings[0].Spec.TLS[0].SecretName

	secrets, err := app.GetIngressSecrets()
	if err != nil {
		t.Fatalf("GetIngressSecrets: %v", err)
	}
	if len(secrets) != 1 {
		t.Fatalf("expected 1 TLS secret, got %d", len(secrets))
	}
	secretName := secrets[0].Name

	if refName != secretName {
		t.Errorf("TLS reference %q does not match emitted Secret name %q", refName, secretName)
	}
	if refName != strings.ToLower(refName) {
		t.Errorf("TLS reference name not lowercased: %q", refName)
	}
	if secretName != strings.ToLower(secretName) {
		t.Errorf("Secret object name not lowercased: %q", secretName)
	}
	if ingName := ings[0].Name; ingName != strings.ToLower(ingName) {
		t.Errorf("ingress object name not lowercased: %q", ingName)
	}
}

// Regression (#10): when the same host repeats across ingress entries each
// carrying TLS material, only one IngressTLS block and one TLS Secret may be
// emitted; duplicates with an identical name/kind/namespace break kubectl apply.
func TestIngressDeduplicatesRepeatedHostTLS(t *testing.T) {
	app := ingressTestApp()
	app.Ingress = []Ingress{
		{Host: "example.com", Path: "/a", TLSCrt: "C", TLSKey: "K"},
		{Host: "example.com", Path: "/b", TLSCrt: "C", TLSKey: "K"},
	}

	ings, err := app.GetIngress()
	if err != nil {
		t.Fatalf("GetIngress: %v", err)
	}
	if len(ings) != 1 {
		t.Fatalf("expected 1 ingress, got %d", len(ings))
	}
	if got := len(ings[0].Spec.TLS); got != 1 {
		t.Errorf("expected 1 TLS block for repeated host, got %d", got)
	}
	secrets, err := app.GetIngressSecrets()
	if err != nil {
		t.Fatalf("GetIngressSecrets: %v", err)
	}
	if got := len(secrets); got != 1 {
		t.Errorf("expected 1 TLS secret for repeated host, got %d", got)
	}
}

// Regression (#8): two ingress entries sharing an explicit tlsSecretName but
// carrying different certificate material is a fatal misconfiguration — only one
// Secret can own a given name, so the second cert would be silently dropped and
// the wrong certificate served. GetIngressSecrets must reject it.
func TestIngressSecretsConflictingCertSameName(t *testing.T) {
	app := ingressTestApp()
	app.Ingress = []Ingress{
		{Host: "a.example.com", TLSSecretName: "shared", TLSCrt: "CRT_A", TLSKey: "KEY_A"},
		{Host: "b.example.com", TLSSecretName: "shared", TLSCrt: "CRT_B", TLSKey: "KEY_B"},
	}
	if _, err := app.GetIngressSecrets(); err == nil {
		t.Error("expected an error for conflicting TLS certificates under one secret name")
	}
}

// #58: two entries for the same host that resolve to the same ingress object but
// request different ingressClasses cannot be represented (IngressClassName is
// ingress-wide) — error instead of silently letting the last entry win. The same
// class on both entries must still merge cleanly.
func TestIngressConflictingClassError(t *testing.T) {
	app := ingressTestApp()
	app.Ingress = []Ingress{
		{Host: "example.com", IngressCommon: IngressCommon{Class: "alpha"}},
		{Host: "example.com", Path: "/api", IngressCommon: IngressCommon{Class: "beta"}},
	}
	if _, err := app.GetIngress(); err == nil {
		t.Errorf("expected an error for conflicting ingressClass on the same host")
	}

	app.Ingress = []Ingress{
		{Host: "example.com", IngressCommon: IngressCommon{Class: "alpha"}},
		{Host: "example.com", Path: "/api", IngressCommon: IngressCommon{Class: "alpha"}},
	}
	ings, err := app.GetIngress()
	if err != nil {
		t.Fatalf("same class on both entries must not conflict: %v", err)
	}
	if len(ings) != 1 || len(ings[0].Spec.Rules[0].HTTP.Paths) != 2 {
		t.Errorf("same-host same-class entries must merge into one rule with both paths, got %+v", ings)
	}
}

// Regression: two distinct cronjob keys whose "<release>-<name>" both exceed the
// 52-char CronJob limit and share the same first 52 characters truncate to the
// SAME object name. Emitting two CronJobs with an identical metadata.name would
// silently drop one on `kubectl apply` (only the last-applied survives), so the
// collision must surface as an error instead.
func TestGetCronJobsTruncationNameCollisionErrors(t *testing.T) {
	app := deployApp(t)
	// A 45-char release name + "-report" (first 7 chars of both suffixes) fills
	// exactly the 52-char cap, so both crons truncate to the identical name.
	app.Name = strings.Repeat("a", 45)
	app.Cronjob = map[string]CronjobSpec{
		"report-generation-daily":  {Schedule: "* * * * *", Container: apiv1.Container{Image: "example/app:v1", Command: []string{"true"}}},
		"report-generation-weekly": {Schedule: "* * * * *", Container: apiv1.Container{Image: "example/app:v1", Command: []string{"true"}}},
	}

	if _, err := app.GetCronJobs(); err == nil {
		t.Fatalf("expected an error for colliding truncated cronjob names, got nil")
	}
}

// Distinct cronjob keys that truncate to different names must still generate
// independently — the collision guard must not reject every long-named cron.
func TestGetCronJobsDistinctTruncatedNamesOK(t *testing.T) {
	app := deployApp(t)
	app.Common.Image.Repository = "example/app"
	app.Name = strings.Repeat("a", 45)
	app.Cronjob = map[string]CronjobSpec{
		"alpha": {Schedule: "* * * * *", Container: apiv1.Container{Image: "example/app:v1", Command: []string{"true"}}},
		"omega": {Schedule: "* * * * *", Container: apiv1.Container{Image: "example/app:v1", Command: []string{"true"}}},
	}

	crons, err := app.GetCronJobs()
	if err != nil {
		t.Fatalf("GetCronJobs: %v", err)
	}
	if len(crons) != 2 {
		t.Fatalf("expected 2 cronjobs, got %d", len(crons))
	}
	if crons[0].Name == crons[1].Name {
		t.Errorf("distinct crons must keep distinct names, both %q", crons[0].Name)
	}
}

func zeros(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '0'
	}
	return string(b)
}
