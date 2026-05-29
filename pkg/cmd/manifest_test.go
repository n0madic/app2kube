package cmd

import (
	"strings"
	"testing"

	"github.com/n0madic/app2kube/pkg/app2kube"
	apiv1 "k8s.io/api/core/v1"
)

func manifestTestApp(t *testing.T) *app2kube.App {
	t.Helper()
	app := app2kube.NewApp()
	app.Name = "example"
	app.Namespace = "prod"
	app.ConfigMap = map[string]string{"KEY": "value"}
	app.Deployment.Containers = map[string]apiv1.Container{
		"app": {Image: "example/app:v1"},
	}
	app.Service = map[string]app2kube.Service{"web": {Port: 80}}
	return app
}

func TestParseOutputTypes(t *testing.T) {
	cases := []struct {
		in   string
		want app2kube.OutputResource
	}{
		{"all", app2kube.OutputAll},
		{"ALL", app2kube.OutputAll},
		{"configmap", app2kube.OutputConfigMap},
		{"cronjob", app2kube.OutputCronJob},
		{"deployment", app2kube.OutputDeployment},
		{"ingress", app2kube.OutputIngress},
		{"pvc", app2kube.OutputPersistentVolumeClaim},
		{"secret", app2kube.OutputSecret},
		{"service", app2kube.OutputService},
	}
	for _, tc := range cases {
		got := parseOutputTypes([]string{tc.in})
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("parseOutputTypes(%q): got %v, want %v", tc.in, got, tc.want)
		}
	}

	// Unknown types are silently ignored.
	if got := parseOutputTypes([]string{"bogus"}); len(got) != 0 {
		t.Errorf("unknown type must be ignored, got %v", got)
	}

	// Multiple types are preserved in order.
	got := parseOutputTypes([]string{"deployment", "service"})
	if len(got) != 2 || got[0] != app2kube.OutputDeployment || got[1] != app2kube.OutputService {
		t.Errorf("multiple types: got %v", got)
	}
}

func TestBuildManifestDeploymentOnly(t *testing.T) {
	app := manifestTestApp(t)
	out, err := buildManifest(app, []string{"deployment"}, "yaml", false)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if !strings.Contains(out, "# Deployment: example") {
		t.Errorf("missing deployment:\n%s", out)
	}
	if strings.Contains(out, "# ConfigMap:") {
		t.Errorf("deployment-only must not include configmap:\n%s", out)
	}
}

func TestBuildManifestIncludeNamespace(t *testing.T) {
	app := manifestTestApp(t)
	out, err := buildManifest(app, []string{"deployment"}, "yaml", true)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if !strings.Contains(out, "# Namespace: prod") {
		t.Errorf("namespace must be included:\n%s", out)
	}
	// Namespace must be prepended before other resources.
	if strings.Index(out, "# Namespace:") > strings.Index(out, "# Deployment:") {
		t.Errorf("namespace must come first:\n%s", out)
	}
}

func TestBuildManifestDefaultNamespaceOmitted(t *testing.T) {
	app := manifestTestApp(t)
	app.Namespace = app2kube.NamespaceDefault
	out, err := buildManifest(app, []string{"deployment"}, "yaml", true)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	// The default namespace is cleared, so no Namespace resource is emitted.
	if strings.Contains(out, "# Namespace:") {
		t.Errorf("default namespace must not be emitted:\n%s", out)
	}
}
