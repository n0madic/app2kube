package app2kube

import (
	"strings"
	"testing"
)

func TestGetReleaseName(t *testing.T) {
	cases := []struct {
		name    string
		staging string
		branch  string
		want    string
	}{
		{"MyApp", "", "", "myapp"},
		{"MyApp", "stg", "", "myapp-stg"},
		{"MyApp", "stg", "feature", "myapp-feature"},
	}
	for _, tc := range cases {
		app := NewApp()
		app.Name = tc.name
		app.Staging = tc.staging
		app.Branch = tc.branch
		if got := app.GetReleaseName(); got != tc.want {
			t.Errorf("GetReleaseName(%q,%q,%q): got %q, want %q",
				tc.name, tc.staging, tc.branch, got, tc.want)
		}
	}
}

func TestGetDeploymentName(t *testing.T) {
	app := NewApp()
	app.Name = "app"
	if got := app.GetDeploymentName(); got != "app" {
		t.Errorf("got %q, want app", got)
	}
	app.Deployment.BlueGreenColor = "green"
	if got := app.GetDeploymentName(); got != "app-green" {
		t.Errorf("got %q, want app-green", got)
	}
}

func TestGetServiceName(t *testing.T) {
	app := NewApp()
	app.Name = "app"
	if got := app.getServiceName(""); got != "app" {
		t.Errorf("empty name: got %q, want app", got)
	}
	if got := app.getServiceName("Web"); got != "app-web" {
		t.Errorf("named: got %q, want app-web", got)
	}
}

func TestGetColorLabels(t *testing.T) {
	app := NewApp()
	app.Labels = map[string]string{"foo": "bar"}

	// Without blue/green color the labels are copied unchanged.
	labels := app.GetColorLabels()
	if labels["foo"] != "bar" {
		t.Errorf("expected copied labels, got %+v", labels)
	}
	if _, ok := labels["app.kubernetes.io/color"]; ok {
		t.Errorf("color label must not be present without blue/green")
	}
	// Must be a copy, not the same map.
	labels["new"] = "x"
	if _, ok := app.Labels["new"]; ok {
		t.Errorf("GetColorLabels must return a copy")
	}

	app.Deployment.BlueGreenColor = "blue"
	if got := app.GetColorLabels()["app.kubernetes.io/color"]; got != "blue" {
		t.Errorf("color label: got %q, want blue", got)
	}
}

func TestGetSelectorLabels(t *testing.T) {
	app := NewApp()
	app.Labels = map[string]string{
		LabelName:      "web",
		LabelInstance:  "production",
		LabelManagedBy: "app2kube",
		"team":         "payments",
	}

	sel := app.GetSelectorLabels()
	if sel[LabelName] != "web" || sel[LabelInstance] != "production" {
		t.Errorf("selector must carry name+instance: %+v", sel)
	}
	if _, ok := sel[LabelManagedBy]; ok {
		t.Errorf("selector must not carry managed-by: %+v", sel)
	}
	if _, ok := sel["team"]; ok {
		t.Errorf("selector must not carry user labels: %+v", sel)
	}
	if _, ok := sel[LabelColor]; ok {
		t.Errorf("selector must not carry color without blue/green: %+v", sel)
	}

	app.Deployment.BlueGreenColor = "green"
	if got := app.GetSelectorLabels()[LabelColor]; got != "green" {
		t.Errorf("selector color: got %q, want green", got)
	}
}

func TestTruncateName(t *testing.T) {
	long := strings.Repeat("a", 70)
	if got := truncateName(long); len(got) != MaxNameLength {
		t.Errorf("expected truncation to %d, got %d", MaxNameLength, len(got))
	}
	// Trailing non-alphanumeric runes are trimmed after truncation.
	if got := truncateName("name---"); got != "name" {
		t.Errorf("trailing dashes: got %q, want name", got)
	}
	if got := truncateName("ok"); got != "ok" {
		t.Errorf("short name changed: got %q", got)
	}
}

func TestLoadValuesNameRequired(t *testing.T) {
	app := NewApp()
	if _, err := app.LoadValues(nil, nil, nil, nil); err == nil {
		t.Errorf("expected error when app name is missing")
	}
}

func TestLoadValuesNameNormalized(t *testing.T) {
	app := NewApp()
	if _, err := app.LoadValues(nil, []string{"name=My_App"}, nil, nil); err != nil {
		t.Fatalf("LoadValues: %v", err)
	}
	// Underscores become dashes and the name is lowercased.
	if app.Name != "my-app" {
		t.Errorf("name: got %q, want my-app", app.Name)
	}
	if app.Labels["app.kubernetes.io/name"] != "my-app" {
		t.Errorf("name label: got %q", app.Labels["app.kubernetes.io/name"])
	}
}

func TestLoadValuesStagingDefaults(t *testing.T) {
	app := NewApp()
	_, err := app.LoadValues(nil, []string{"name=app", "staging=STG"}, nil, nil)
	if err != nil {
		t.Fatalf("LoadValues: %v", err)
	}
	if app.Staging != "stg" {
		t.Errorf("staging not lowercased: %q", app.Staging)
	}
	// Staging forces replicaCount=1 and pull policy Always.
	if app.Deployment.ReplicaCount != 1 {
		t.Errorf("staging replicaCount: got %d, want 1", app.Deployment.ReplicaCount)
	}
	if string(app.Common.Image.PullPolicy) != "Always" {
		t.Errorf("staging pull policy: got %q, want Always", app.Common.Image.PullPolicy)
	}
	if app.Labels["app.kubernetes.io/instance"] != "stg" {
		t.Errorf("instance label: got %q", app.Labels["app.kubernetes.io/instance"])
	}
}

func TestLoadValuesStagingWildcardError(t *testing.T) {
	app := NewApp()
	_, err := app.LoadValues(nil, []string{
		"name=app",
		"staging=stg",
		"ingress[0].host=*.example.com",
	}, nil, nil)
	if err == nil {
		t.Errorf("expected error: staging cannot be used with wildcard domain")
	}
}

func TestLoadValuesStagingPrefixesHost(t *testing.T) {
	app := NewApp()
	_, err := app.LoadValues(nil, []string{
		"name=app",
		"staging=stg",
		"branch=feat",
		"ingress[0].host=example.com",
	}, nil, nil)
	if err != nil {
		t.Fatalf("LoadValues: %v", err)
	}
	if app.Ingress[0].Host != "feat.stg.example.com" {
		t.Errorf("host: got %q, want feat.stg.example.com", app.Ingress[0].Host)
	}
}

// applyStaging is exercised directly (without parsing) to lock the instance
// label composition when both staging and branch are set.
func TestApplyStagingBranchInstanceLabel(t *testing.T) {
	app := NewApp()
	app.Staging = "STG"
	app.Branch = "Feat"
	if err := app.applyStaging(); err != nil {
		t.Fatalf("applyStaging: %v", err)
	}
	if app.Labels["app.kubernetes.io/instance"] != "stg-feat" {
		t.Errorf("instance label: got %q, want stg-feat", app.Labels["app.kubernetes.io/instance"])
	}
	// Staging clears blue/green color and revision history.
	if app.Deployment.BlueGreenColor != "" || app.Deployment.RevisionHistoryLimit != 0 {
		t.Errorf("staging must reset blue/green and revision history: %+v", app.Deployment)
	}
}

// Without staging, applyStaging only normalizes the blue/green color.
func TestApplyStagingNoStagingLowercasesColor(t *testing.T) {
	app := NewApp()
	app.Deployment.BlueGreenColor = "BLUE"
	if err := app.applyStaging(); err != nil {
		t.Fatalf("applyStaging: %v", err)
	}
	if app.Deployment.BlueGreenColor != "blue" {
		t.Errorf("color: got %q, want blue", app.Deployment.BlueGreenColor)
	}
}
