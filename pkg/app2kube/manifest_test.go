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
cronjob:
  backup:
    schedule: "* * * * *"
    container:
      image: example/app:v1
      command: [echo]
`)

	cases := []struct {
		name    string
		out     OutputResource
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
