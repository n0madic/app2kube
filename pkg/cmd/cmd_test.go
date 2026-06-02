package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// resetAppFlags clears the package-level flags that are still global (kube
// config and the all/blue-green toggles) so each test starts from a clean
// state. Value inputs now live on appOptions and are set per test.
func resetAppFlags() {
	flagAllApplications = false
	flagAllInstances = false
	blueGreenDeploy = false
	if kubeConfigFlags.Namespace != nil {
		*kubeConfigFlags.Namespace = ""
	}
}

func TestInitAppValuesRequired(t *testing.T) {
	resetAppFlags()
	defer resetAppFlags()
	o := &appOptions{}
	if _, err := o.initApp(); err == nil {
		t.Errorf("expected error when no values are provided")
	}
}

// #40: the values snapshot embeds merged plaintext config, so it must be written
// owner-only (0600) rather than group-readable.
func TestInitAppSnapshotMode(t *testing.T) {
	resetAppFlags()
	defer resetAppFlags()
	snap := filepath.Join(t.TempDir(), "snap.yaml")
	o := &appOptions{values: []string{"name=app"}, snapshot: snap}
	if _, err := o.initApp(); err != nil {
		t.Fatalf("initApp: %v", err)
	}
	fi, err := os.Stat(snap)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0600 {
		t.Errorf("snapshot mode: got %o, want 0600", got)
	}
}

func TestInitAppFromSet(t *testing.T) {
	resetAppFlags()
	defer resetAppFlags()
	o := &appOptions{values: []string{"name=app"}}

	app, err := o.initApp()
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
	o := &appOptions{values: []string{"name=app", "namespace=fromvalues"}}
	*kubeConfigFlags.Namespace = "fromflag"

	app, err := o.initApp()
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

	o := &appOptions{}
	app, err := o.initApp()
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

func TestScopedSelector(t *testing.T) {
	resetAppFlags()
	defer resetAppFlags()

	// Normal, app-scoped labels return the selector without error.
	got, err := scopedSelector(map[string]string{"app.kubernetes.io/name": "myapp"})
	if err != nil {
		t.Fatalf("scopedSelector(name): unexpected error: %v", err)
	}
	if got != "app.kubernetes.io/name=myapp" {
		t.Errorf("scoped selector: got %q", got)
	}

	// Labels without app.kubernetes.io/name are unscoped → refuse.
	if _, err := scopedSelector(map[string]string{"app.kubernetes.io/instance": "production"}); err == nil {
		t.Errorf("expected error for unscoped selector (no name label)")
	}

	// An empty label set yields an empty selector → refuse.
	if _, err := scopedSelector(map[string]string{}); err == nil {
		t.Errorf("expected error for empty selector")
	}

	// flagAllInstances dropping the only (instance) label yields "" → refuse.
	flagAllInstances = true
	if _, err := scopedSelector(map[string]string{"app.kubernetes.io/instance": "production"}); err == nil {
		t.Errorf("expected error when all-instances drops the only label")
	}
	flagAllInstances = false

	// The deliberate --all-applications mode is allowed (managed-by scope).
	flagAllApplications = true
	got, err = scopedSelector(map[string]string{"app.kubernetes.io/name": "myapp"})
	if err != nil {
		t.Fatalf("scopedSelector(all-apps): unexpected error: %v", err)
	}
	if got != "app.kubernetes.io/managed-by=app2kube" {
		t.Errorf("all-apps scoped selector: got %q", got)
	}
	flagAllApplications = false
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
