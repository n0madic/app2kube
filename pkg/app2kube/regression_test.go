package app2kube

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/ghodss/yaml"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
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

// Regression: a TLS Secret must only be emitted when certificate material is
// actually provided. With letsencrypt the Secret is managed by cert-manager and
// emitting an empty kubernetes.io/tls Secret would be invalid.
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
	if len(secrets) != 1 {
		t.Fatalf("expected 1 TLS secret (only the one with crt/key), got %d", len(secrets))
	}
	if string(secrets[0].Data["tls.crt"]) != "CRT" || string(secrets[0].Data["tls.key"]) != "KEY" {
		t.Errorf("unexpected TLS secret data: %v", secrets[0].Data)
	}
}

// With letsencrypt, app2kube no longer emits the TLS Secret itself — instead it
// must wire the Ingress so cert-manager's ingress-shim creates and populates it:
// the kubernetes.io/tls-acme annotation plus a TLS block referencing the secret
// name. This locks the contract that "cert-manager creates the secret if missing".
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
	if ings[0].Annotations["kubernetes.io/tls-acme"] != "true" {
		t.Errorf("letsencrypt ingress must carry kubernetes.io/tls-acme=true for cert-manager, got %v", ings[0].Annotations)
	}
	if len(ings[0].Spec.TLS) != 1 || ings[0].Spec.TLS[0].SecretName != "tls-le.example.com" {
		t.Errorf("letsencrypt ingress must reference the TLS secret cert-manager fills, got %+v", ings[0].Spec.TLS)
	}

	// app2kube must NOT emit the secret itself — cert-manager owns it.
	secrets, err := app.GetIngressSecrets()
	if err != nil {
		t.Fatalf("GetIngressSecrets: %v", err)
	}
	if len(secrets) != 0 {
		t.Errorf("letsencrypt must not emit a TLS secret (cert-manager creates it), got %d", len(secrets))
	}
}

// Object names combining the release name with a user-controlled key
// (service/volume/cron/ingress) must be truncated to the 63-char DNS-1123 limit
// so a long app name plus a long key is not rejected by the apiserver on apply.
func TestObjectNamesTruncatedTo63(t *testing.T) {
	longKey := strings.Repeat("b", 40)

	app := NewApp()
	app.Name = strings.Repeat("a", 50)

	if got := app.GetServiceName(longKey); len(got) > MaxNameLength {
		t.Errorf("service name not truncated: len=%d (%q)", len(got), got)
	}
	if got := app.GetVolumeClaimName(longKey); len(got) > MaxNameLength {
		t.Errorf("claim name not truncated: len=%d (%q)", len(got), got)
	}

	// The PVC object name and the pod volume reference must use the same helper,
	// so they stay byte-identical even after truncation.
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

	// CronJob object name truncated.
	app.Common.Image.Repository = "example/app"
	app.Cronjob = map[string]CronjobSpec{
		longKey: {Schedule: "* * * * *", Container: apiv1.Container{Image: "example/app:v1", Command: []string{"true"}}},
	}
	crons, err := app.GetCronJobs()
	if err != nil {
		t.Fatalf("GetCronJobs: %v", err)
	}
	if len(crons[0].Name) > MaxNameLength {
		t.Errorf("cronjob name not truncated: len=%d (%q)", len(crons[0].Name), crons[0].Name)
	}

	// Ingress object name truncated.
	app.Deployment.Containers = map[string]apiv1.Container{"app": {}}
	app.Service = map[string]Service{"web": {Port: 80}}
	app.Ingress = []Ingress{{Host: strings.Repeat("h", 60) + ".example.com"}}
	ings, err := app.GetIngress()
	if err != nil {
		t.Fatalf("GetIngress: %v", err)
	}
	if len(ings[0].Name) > MaxNameLength {
		t.Errorf("ingress name not truncated: len=%d (%q)", len(ings[0].Name), ings[0].Name)
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

func zeros(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '0'
	}
	return string(b)
}
