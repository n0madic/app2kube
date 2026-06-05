package cmd

import (
	"context"
	"testing"

	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"
)

// #63: commands that take no positional arguments must reject unexpected ones
// instead of silently ignoring them (e.g. `manifest deployment` used to print
// the default "all").
func TestCommandsRejectUnexpectedArgs(t *testing.T) {
	noArgCmds := []*cobra.Command{NewCmdManifest(), NewCmdStatus(), NewCmdApply()}
	for _, parent := range []*cobra.Command{NewCmdConfig(), NewCmdTrack(), NewCmdBlueGreen()} {
		noArgCmds = append(noArgCmds, parent.Commands()...)
	}
	for _, c := range noArgCmds {
		if c.ValidateArgs([]string{"unexpected"}) == nil {
			t.Errorf("command %q must reject unexpected positional args", c.Name())
		}
		if c.ValidateArgs(nil) != nil {
			t.Errorf("command %q must accept no args", c.Name())
		}
	}
}

// #63: delete accepts only no args or exactly "all"; anything else (forwarded
// verbatim to kubectl before) must be rejected.
func TestDeleteArgsValidator(t *testing.T) {
	c := NewCmdDelete()
	if c.ValidateArgs(nil) != nil {
		t.Error("delete with no args must be valid")
	}
	if c.ValidateArgs([]string{"all"}) != nil {
		t.Error(`delete "all" must be valid`)
	}
	if c.ValidateArgs([]string{"foo"}) == nil {
		t.Error("delete with an unexpected positional must be rejected")
	}
	if c.ValidateArgs([]string{"all", "extra"}) == nil {
		t.Error("delete with two positionals must be rejected")
	}
}

// #59: namespace precedence is flag > file > default, and an explicitly-set
// --namespace wins even when empty (forcing the default), so it is
// distinguishable from an absent flag.
func TestResolveNamespace(t *testing.T) {
	if got := resolveNamespace(false, "", "fromfile"); got != "fromfile" {
		t.Errorf("file value must win when flag absent: got %q", got)
	}
	if got := resolveNamespace(true, "fromflag", "fromfile"); got != "fromflag" {
		t.Errorf("flag must win over file: got %q", got)
	}
	if got := resolveNamespace(true, "", "fromfile"); got != app2kube.NamespaceDefault {
		t.Errorf("explicit empty flag must force default: got %q", got)
	}
	if got := resolveNamespace(false, "", ""); got != app2kube.NamespaceDefault {
		t.Errorf("nothing set must default: got %q", got)
	}
}

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
	// Clear the persistent --namespace flag's Changed state so the next test
	// starts with the flag "absent" (namespace precedence keys off Changed, #59).
	if f := rootCmd.PersistentFlags().Lookup("namespace"); f != nil {
		f.Changed = false
	}
}

func TestInitAppValuesRequired(t *testing.T) {
	resetAppFlags()
	defer resetAppFlags()
	o := &appOptions{}
	if _, err := o.initApp(context.Background()); err == nil {
		t.Errorf("expected error when no values are provided")
	}
}

func TestInitAppFromSet(t *testing.T) {
	resetAppFlags()
	defer resetAppFlags()
	o := &appOptions{values: []string{"name=app"}}

	app, err := o.initApp(context.Background())
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
	// Set through the flag so its Changed state is recorded (how #59 detects an
	// explicit flag), not by assigning the bound variable directly.
	if err := rootCmd.PersistentFlags().Set("namespace", "fromflag"); err != nil {
		t.Fatalf("set --namespace: %v", err)
	}

	app, err := o.initApp(context.Background())
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
	app, err := o.initApp(context.Background())
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
