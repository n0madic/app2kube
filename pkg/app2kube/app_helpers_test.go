package app2kube

import (
	"strings"
	"testing"
)

func TestGetReleaseName(t *testing.T) {
	cases := []struct {
		name    string
		staging Staging
		branch  string
		want    string
	}{
		{"MyApp", Staging{}, "", "myapp"},
		{"MyApp", Staging{Active: true, Name: "stg"}, "", "myapp-stg"},
		{"MyApp", Staging{Active: true, Name: "stg"}, "feature", "myapp-feature"},
		// Anonymous staging (staging: true) keeps staging active but contributes
		// nothing to the release name: only the branch identifies the instance,
		// and without a branch the release name stays bare.
		{"MyApp", Staging{Active: true}, "feature", "myapp-feature"},
		{"MyApp", Staging{Active: true}, "", "myapp"},
	}
	for _, tc := range cases {
		app := NewApp()
		app.Name = tc.name
		app.Staging = tc.staging
		app.Branch = tc.branch
		if got := app.GetReleaseName(); got != tc.want {
			t.Errorf("GetReleaseName(%q,%+v,%q): got %q, want %q",
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
	if got := app.GetServiceName(""); got != "app" {
		t.Errorf("empty name: got %q, want app", got)
	}
	if got := app.GetServiceName("Web"); got != "app-web" {
		t.Errorf("named: got %q, want app-web", got)
	}
}

// #69: alias suppression under staging lives in one place (IngressAliases), so
// the ingress generator and the status printer share the rule.
func TestIngressAliases(t *testing.T) {
	app := NewApp()
	ing := Ingress{Host: "example.com", Aliases: []string{"www.example.com"}}

	app.Staging = Staging{}
	if got := app.IngressAliases(ing); len(got) != 1 || got[0] != "www.example.com" {
		t.Errorf("non-staging must return aliases, got %v", got)
	}

	app.Staging = Staging{Active: true, Name: "stg"}
	if got := app.IngressAliases(ing); got != nil {
		t.Errorf("staging must suppress aliases, got %v", got)
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
	_, err := app.LoadValues(nil, nil, nil, nil)
	if err == nil {
		t.Fatalf("expected error when app name is missing")
	}
	// #52: error strings start lowercase (ST1005), since they may be wrapped or
	// printed mid-sentence.
	if !strings.HasPrefix(err.Error(), "app name") {
		t.Errorf("error must start lowercase, got %q", err.Error())
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
	if app.Staging.Name != "stg" {
		t.Errorf("staging not lowercased: %q", app.Staging.Name)
	}
	// Staging forces replicaCount=1 and pull policy Always.
	if app.Deployment.ReplicaCount == nil || *app.Deployment.ReplicaCount != 1 {
		t.Errorf("staging replicaCount: got %v, want 1", app.Deployment.ReplicaCount)
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

// #9: branch and staging names must be DNS-sanitized like the app name —
// slashes (common in git branches like "feature/foo") and underscores become
// hyphens — so the derived release name, instance label and ingress host stay
// DNS-1123-valid instead of being rejected by the API server.
func TestLoadValuesStagingSanitizesBranchAndStaging(t *testing.T) {
	app := NewApp()
	_, err := app.LoadValues(nil, []string{
		"name=app",
		"staging=My_Env",
		"branch=feature/foo",
		"ingress[0].host=example.com",
	}, nil, nil)
	if err != nil {
		t.Fatalf("LoadValues: %v", err)
	}
	if app.Ingress[0].Host != "feature-foo.my-env.example.com" {
		t.Errorf("host: got %q, want feature-foo.my-env.example.com", app.Ingress[0].Host)
	}
	if got := app.Labels["app.kubernetes.io/instance"]; got != "my-env-feature-foo" {
		t.Errorf("instance label: got %q, want my-env-feature-foo", got)
	}
	if got := app.GetReleaseName(); got != "app-feature-foo" {
		t.Errorf("release name: got %q, want app-feature-foo", got)
	}
}

// Anonymous staging (staging: true) deploys a branch onto the root domain
// (branch.host) instead of the default branch.staging.host, while keeping
// staging machinery active. `--set staging=true` is parsed as a YAML boolean.
func TestLoadValuesStagingAnonymousRootDomain(t *testing.T) {
	app := NewApp()
	_, err := app.LoadValues(nil, []string{
		"name=app",
		"staging=true",
		"branch=feat",
		"ingress[0].host=example.com",
	}, nil, nil)
	if err != nil {
		t.Fatalf("LoadValues: %v", err)
	}
	if app.Ingress[0].Host != "feat.example.com" {
		t.Errorf("host: got %q, want feat.example.com", app.Ingress[0].Host)
	}
	// Anonymous staging contributes nothing to the instance label and release
	// name; only the branch identifies the instance.
	if got := app.Labels["app.kubernetes.io/instance"]; got != "feat" {
		t.Errorf("instance label: got %q, want feat", got)
	}
	if got := app.GetReleaseName(); got != "app-feat" {
		t.Errorf("release name: got %q, want app-feat", got)
	}
	// Anonymous staging still activates staging behavior (pull policy Always).
	if string(app.Common.Image.PullPolicy) != "Always" {
		t.Errorf("staging pull policy: got %q, want Always", app.Common.Image.PullPolicy)
	}
}

// With anonymous staging but no branch the host is left bare (no segment added).
func TestLoadValuesStagingAnonymousNoBranchBareHost(t *testing.T) {
	app := NewApp()
	_, err := app.LoadValues(nil, []string{
		"name=app",
		"staging=true",
		"ingress[0].host=example.com",
	}, nil, nil)
	if err != nil {
		t.Fatalf("LoadValues: %v", err)
	}
	if app.Ingress[0].Host != "example.com" {
		t.Errorf("host: got %q, want example.com", app.Ingress[0].Host)
	}
	if got := app.GetReleaseName(); got != "app" {
		t.Errorf("release name: got %q, want app", got)
	}
	// With no name and no branch to identify it, the instance label falls back to
	// "staging" rather than the production default.
	if got := app.Labels["app.kubernetes.io/instance"]; got != "staging" {
		t.Errorf("instance label: got %q, want staging", got)
	}
}

// The strings "true"/"false" are reserved: a quoted value or --set-string still
// selects anonymous staging rather than a staging environment named "true".
func TestLoadValuesStagingReservedStringTrue(t *testing.T) {
	app := NewApp()
	_, err := app.LoadValues(nil, []string{
		"name=app",
		"branch=feat",
		"ingress[0].host=example.com",
	}, []string{"staging=true"}, nil)
	if err != nil {
		t.Fatalf("LoadValues: %v", err)
	}
	if app.Ingress[0].Host != "feat.example.com" {
		t.Errorf("host: got %q, want feat.example.com", app.Ingress[0].Host)
	}
}

// staging: false (or the reserved "false" string) means no staging at all: the
// host is untouched and staging defaults are not applied.
func TestLoadValuesStagingFalseDisabled(t *testing.T) {
	app := NewApp()
	_, err := app.LoadValues(nil, []string{
		"name=app",
		"staging=false",
		"branch=feat",
		"ingress[0].host=example.com",
	}, nil, nil)
	if err != nil {
		t.Fatalf("LoadValues: %v", err)
	}
	if app.Ingress[0].Host != "example.com" {
		t.Errorf("host: got %q, want example.com", app.Ingress[0].Host)
	}
	if app.Staging.Active {
		t.Errorf("staging must be inactive for staging=false")
	}
}

// applyStaging is exercised directly (without parsing) to lock the instance
// label composition when both staging and branch are set.
func TestApplyStagingBranchInstanceLabel(t *testing.T) {
	app := NewApp()
	app.Staging = Staging{Active: true, Name: "STG"}
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
