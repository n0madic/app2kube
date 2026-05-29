package cmd

import (
	"testing"
)

// resetAppFlags clears the package-level value flags shared across commands so
// each test starts from a clean state.
func resetAppFlags() {
	valueFiles = nil
	values = nil
	stringValues = nil
	fileValues = nil
	flagAllApplications = false
	flagAllInstances = false
	blueGreenDeploy = false
	snapshot = ""
	flagVerbose = false
	if kubeConfigFlags.Namespace != nil {
		*kubeConfigFlags.Namespace = ""
	}
}

func TestInitAppValuesRequired(t *testing.T) {
	resetAppFlags()
	defer resetAppFlags()
	if _, err := initApp(); err == nil {
		t.Errorf("expected error when no values are provided")
	}
}

func TestInitAppFromSet(t *testing.T) {
	resetAppFlags()
	defer resetAppFlags()
	values = []string{"name=app"}

	app, err := initApp()
	if err != nil {
		t.Fatalf("initApp: %v", err)
	}
	if app.Name != "app" {
		t.Errorf("name: got %q", app.Name)
	}
	// Namespace defaults and the managed-by label is injected.
	if app.Namespace != "default" {
		t.Errorf("namespace default: got %q", app.Namespace)
	}
	if app.Labels["app.kubernetes.io/managed-by"] != "app2kube" {
		t.Errorf("managed-by label missing: %+v", app.Labels)
	}
}

func TestInitAppNamespaceOverride(t *testing.T) {
	resetAppFlags()
	defer resetAppFlags()
	values = []string{"name=app", "namespace=fromvalues"}
	*kubeConfigFlags.Namespace = "fromflag"

	app, err := initApp()
	if err != nil {
		t.Fatalf("initApp: %v", err)
	}
	// An explicit --namespace flag overrides the value file namespace.
	if app.Namespace != "fromflag" {
		t.Errorf("namespace: got %q, want fromflag", app.Namespace)
	}
}

func TestInitAppAllApplications(t *testing.T) {
	resetAppFlags()
	defer resetAppFlags()
	flagAllApplications = true
	*kubeConfigFlags.Namespace = "ns"

	app, err := initApp()
	if err != nil {
		t.Fatalf("initApp: %v", err)
	}
	if app.Name != "all" || app.Namespace != "ns" {
		t.Errorf("all-applications app: got name=%q ns=%q", app.Name, app.Namespace)
	}
}

func TestGetSelector(t *testing.T) {
	resetAppFlags()
	defer resetAppFlags()

	labels := map[string]string{"app.kubernetes.io/instance": "production"}
	if got := getSelector(labels); got != "app.kubernetes.io/instance=production" {
		t.Errorf("selector: got %q", got)
	}

	flagAllApplications = true
	if got := getSelector(labels); got != "app.kubernetes.io/managed-by=app2kube" {
		t.Errorf("all-apps selector: got %q", got)
	}
	flagAllApplications = false

	flagAllInstances = true
	if got := getSelector(labels); got != "" {
		t.Errorf("all-instances must drop the instance label, got %q", got)
	}
}

func TestCommandConstructors(t *testing.T) {
	// Smoke: command constructors must build without panicking and expose the
	// expected names.
	cmds := map[string]string{
		"manifest":   NewCmdManifest().Use,
		"config":     NewCmdConfig().Use,
		"apply":      NewCmdApply().Use,
		"delete":     NewCmdDelete().Use,
		"completion": NewCmdCompletion().Use,
		"track":      NewCmdTrack().Use,
		"status":     NewCmdStatus().Use,
		"build":      NewCmdBuild().Use,
		"blue-green": NewCmdBlueGreen().Use,
	}
	for want, got := range cmds {
		if got == "" {
			t.Errorf("command %q has empty Use", want)
		}
	}
}
