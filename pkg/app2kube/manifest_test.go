package app2kube

import (
	"strings"
	"testing"

	apiv1 "k8s.io/api/core/v1"
)

func TestGetManifestSelectiveTypes(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: example
namespace: prod
configmap:
  KEY: value
secrets:
  pwd: plain
deployment:
  containers:
    app:
      image: example/app:v1
service:
  web:
    port: 80
ingress:
  - host: example.com
volumes:
  data:
    mountPath: /data
    spec:
      accessModes:
        - ReadWriteOnce
cronjob:
  backup:
    schedule: "* * * * *"
    container:
      image: example/app:v1
      command: [echo]
`)

	cases := []struct {
		name     string
		out      OutputResource
		mustHave []string
		mustNot  []string
	}{
		{"ConfigMap", OutputConfigMap, []string{"# ConfigMap: example"}, []string{"# Deployment:", "# Service:"}},
		{"Deployment", OutputDeployment, []string{"# Deployment: example"}, []string{"# ConfigMap:", "# Service:"}},
		{"Service", OutputService, []string{"# Service: example-web"}, []string{"# Deployment:"}},
		{"Ingress", OutputIngress, []string{"# Ingress: example-example.com"}, []string{"# Deployment:"}},
		{"Secret", OutputSecret, []string{"# Secret: example"}, []string{"# Deployment:"}},
		{"CronJob", OutputCronJob, []string{"# CronJob: example-backup"}, []string{"# Deployment:"}},
		{"PVC", OutputPersistentVolumeClaim, []string{"# PersistentVolumeClaim: example-data"}, []string{"# Deployment:"}},
		{"Namespace", OutputNamespace, []string{"# Namespace: prod"}, []string{"# Deployment:"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := app.GetManifest("yaml", tc.out)
			if err != nil {
				t.Fatalf("GetManifest: %v", err)
			}
			for _, s := range tc.mustHave {
				if !strings.Contains(m, s) {
					t.Errorf("manifest missing %q:\n%s", s, m)
				}
			}
			for _, s := range tc.mustNot {
				if strings.Contains(m, s) {
					t.Errorf("manifest unexpectedly contains %q:\n%s", s, m)
				}
			}
		})
	}
}

func TestGetManifestAll(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: example
configmap:
  KEY: value
deployment:
  containers:
    app:
      image: example/app:v1
service:
  web:
    port: 80
`)
	m, err := app.GetManifest("yaml", OutputAll)
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	for _, want := range []string{"# ConfigMap: example", "# Deployment: example", "# Service: example-web"} {
		if !strings.Contains(m, want) {
			t.Errorf("OutputAll missing %q:\n%s", want, m)
		}
	}
	// OutputAll must NOT include the namespace.
	if strings.Contains(m, "# Namespace:") {
		t.Errorf("OutputAll must not include namespace")
	}
}

func TestGetManifestAllAutoServiceIgnoresCronJobPorts(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: review
common:
  image:
    repository: example/app
    tag: v1
deployment:
  containers:
    web:
      ports:
        - name: http
          containerPort: 8080
cronjob:
  backup:
    schedule: "* * * * *"
    container:
      command: [backup]
      ports:
        - name: metrics
          containerPort: 9090
ingress:
  - host: example.com
`)

	m, err := app.GetManifest("yaml", OutputAll)
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if !strings.Contains(m, "# Service: review-http") {
		t.Fatalf("deployment port must drive the implicit service, got:\n%s", m)
	}
	if strings.Contains(m, "# Service: review-metrics") {
		t.Fatalf("cronjob port must not create the implicit ingress service:\n%s", m)
	}
	if !strings.Contains(m, "name: review-http") || !strings.Contains(m, "number: 8080") {
		t.Errorf("ingress must point at the deployment service/port, got:\n%s", m)
	}
}

// The implicit Service (Ingress, no explicit Service, single app container) is
// derived up front by GetManifest, not as a side effect of the Deployment
// render. So a Service- or Ingress-only render — and the blue/green phase that
// emits traffic resources without re-rendering the Deployment — must derive it
// the same way a full render does. A regression would reintroduce the coupling
// where rendering Service/Ingress without the Deployment in the same call
// silently dropped the implicit Service.
func TestGetManifestImplicitServiceWithoutDeploymentRender(t *testing.T) {
	const values = `
name: review
common:
  image:
    repository: example/app
    tag: v1
deployment:
  containers:
    web:
      ports:
        - name: http
          containerPort: 8080
ingress:
  - host: example.com
`

	cases := []struct {
		name   string
		render func(app *App) (string, error)
	}{
		{"service only", func(app *App) (string, error) {
			return app.GetManifest("yaml", OutputService)
		}},
		{"ingress only", func(app *App) (string, error) {
			return app.GetManifest("yaml", OutputIngress)
		}},
		{"all-other only (blue/green phase 2 without phase 1)", func(app *App) (string, error) {
			return app.GetManifest("yaml", OutputAllOther)
		}},
		{"blue/green two-phase: deployment then all-other", func(app *App) (string, error) {
			if _, err := app.GetManifest("yaml", OutputAllForDeployment); err != nil {
				return "", err
			}
			return app.GetManifest("yaml", OutputAllOther)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := mustUnmarshalApp(t, values)
			m, err := tc.render(app)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !strings.Contains(m, "review-http") {
				t.Errorf("implicit service must be derived, got:\n%s", m)
			}
		})
	}
}

// #38: lock the two-phase blue/green apply contract. Phase 1
// (OutputAllForDeployment) must carry the Deployment and the config it depends
// on (Secret/ConfigMap/PVC) but NOT the traffic resources (Service/Ingress),
// which phase 2 (OutputAllOther) applies only after the new color is ready.
// A future edit moving the Service into phase 1 would switch traffic before
// readiness — this test fails loudly if the membership changes.
func TestGetManifestPhaseSplit(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: example
configmap:
  KEY: value
secrets:
  pwd: plain
deployment:
  containers:
    app:
      image: example/app:v1
service:
  web:
    port: 80
ingress:
  - host: example.com
volumes:
  data:
    mountPath: /data
    spec:
      accessModes:
        - ReadWriteOnce
cronjob:
  backup:
    schedule: "* * * * *"
    container:
      image: example/app:v1
      command: [echo]
`)

	phase1, err := app.GetManifest("yaml", OutputAllForDeployment)
	if err != nil {
		t.Fatalf("GetManifest phase 1: %v", err)
	}
	for _, s := range []string{"# Deployment: example", "# Secret: example", "# ConfigMap: example", "# PersistentVolumeClaim: example-data"} {
		if !strings.Contains(phase1, s) {
			t.Errorf("phase 1 (OutputAllForDeployment) missing %q:\n%s", s, phase1)
		}
	}
	for _, s := range []string{"# Service:", "# Ingress:", "# CronJob:"} {
		if strings.Contains(phase1, s) {
			t.Errorf("phase 1 must NOT contain %q (traffic/other belongs to phase 2):\n%s", s, phase1)
		}
	}

	phase2, err := app.GetManifest("yaml", OutputAllOther)
	if err != nil {
		t.Fatalf("GetManifest phase 2: %v", err)
	}
	for _, s := range []string{"# Service: example-web", "# Ingress: example-example.com", "# CronJob: example-backup"} {
		if !strings.Contains(phase2, s) {
			t.Errorf("phase 2 (OutputAllOther) missing %q:\n%s", s, phase2)
		}
	}
	for _, s := range []string{"# Deployment:", "# ConfigMap:", "# PersistentVolumeClaim:", "# Secret: example"} {
		if strings.Contains(phase2, s) {
			t.Errorf("phase 2 must NOT contain %q (it belongs to phase 1):\n%s", s, phase2)
		}
	}
}

func TestGetManifestJSONFormat(t *testing.T) {
	app := mustUnmarshalApp(t, `
name: example
configmap:
  KEY: value
`)
	m, err := app.GetManifest("json", OutputConfigMap)
	if err != nil {
		t.Fatalf("GetManifest json: %v", err)
	}
	if !strings.Contains(m, "\"kind\": \"ConfigMap\"") {
		t.Errorf("expected JSON output, got:\n%s", m)
	}
}

func TestPrintObjNil(t *testing.T) {
	// A typed nil pointer must yield an empty string, not panic.
	var cm *apiv1.ConfigMap
	out, err := PrintObj(cm, "yaml")
	if err != nil {
		t.Fatalf("PrintObj: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty output for nil object, got %q", out)
	}
}

func TestPrintObjStripsCreationTimestamp(t *testing.T) {
	cm := &apiv1.ConfigMap{}
	cm.Name = "thing"
	out, err := PrintObj(cm, "yaml")
	if err != nil {
		t.Fatalf("PrintObj: %v", err)
	}
	if strings.Contains(out, "creationTimestamp") {
		t.Errorf("creationTimestamp must be stripped:\n%s", out)
	}
	if !strings.HasPrefix(out, "---\n# ConfigMap: thing\n") {
		t.Errorf("unexpected header:\n%s", out)
	}
}

func TestParseOutputType(t *testing.T) {
	cases := map[string]OutputResource{
		"all":         OutputAll,
		"certificate": OutputCertificate,
		"configmap":   OutputConfigMap,
		"cronjob":     OutputCronJob,
		"deployment":  OutputDeployment,
		"ingress":     OutputIngress,
		"pvc":         OutputPersistentVolumeClaim,
		"secret":      OutputSecret,
		"service":     OutputService,
		"SECRET":      OutputSecret, // case-insensitive
	}
	for name, want := range cases {
		got, ok := ParseOutputType(name)
		if !ok || got != want {
			t.Errorf("ParseOutputType(%q) = (%v, %v), want (%v, true)", name, got, ok, want)
		}
	}
	if _, ok := ParseOutputType("bogus"); ok {
		t.Errorf("ParseOutputType(bogus) must report unknown")
	}
}
