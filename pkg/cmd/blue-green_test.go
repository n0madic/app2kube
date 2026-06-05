package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestNextBlueGreenColor(t *testing.T) {
	cases := []struct {
		current string
		want    string
	}{
		{"blue", "green"},
		{"green", "blue"},
		{"", "blue"},        // no current deployment starts at blue
		{"unknown", "blue"}, // any non-blue color toggles to blue
	}
	for _, tc := range cases {
		if got := nextBlueGreenColor(tc.current); got != tc.want {
			t.Errorf("nextBlueGreenColor(%q): got %q, want %q", tc.current, got, tc.want)
		}
	}
}

// Regression: `blue-green rollback` used a hardcoded 1-minute track timeout and
// ignored the operator's wishes. It must expose a configurable --timeout (in
// minutes, default the shared track timeout) so a slow previous-color rollout
// is not cut off prematurely.
func TestBlueGreenRollbackTimeoutFlag(t *testing.T) {
	var rollback *cobra.Command
	for _, c := range NewCmdBlueGreen().Commands() {
		if c.Name() == "rollback" {
			rollback = c
		}
	}
	if rollback == nil {
		t.Fatal("rollback subcommand missing")
	}
	f := rollback.Flags().Lookup("timeout")
	if f == nil {
		t.Fatal("blue-green rollback must expose a --timeout flag")
	}
	if f.DefValue != "15" {
		t.Errorf("rollback --timeout default: got %s, want 15", f.DefValue)
	}
}

func TestColorize(t *testing.T) {
	// "blue" input renders in blue; the returned string still contains the text.
	blue := colorize("blue")
	if !strings.Contains(blue, "blue") {
		t.Errorf("colorize lost text: %q", blue)
	}
	// With extra args the trailing args become the displayed text.
	msg := colorize("blue", "hello", "world")
	if !strings.Contains(msg, "hello world") {
		t.Errorf("colorize message: %q", msg)
	}
	// Non-blue colors render green but keep the text.
	green := colorize("green", "ok")
	if !strings.Contains(green, "ok") {
		t.Errorf("colorize green: %q", green)
	}
}

func newColorService(name, color string) *apiv1.Service {
	svc := &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "prod",
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "app2kube"},
		},
	}
	if color != "" {
		svc.Spec.Selector = map[string]string{"app.kubernetes.io/color": color}
	}
	return svc
}

func TestColorFromServices(t *testing.T) {
	kcs := fake.NewSimpleClientset(newColorService("web", "green"))
	got, err := colorFromServices(context.Background(), kcs, "prod", "")
	if err != nil {
		t.Fatalf("colorFromServices: %v", err)
	}
	if got != "green" {
		t.Errorf("color: got %q, want green", got)
	}
}

func TestColorFromServicesNoService(t *testing.T) {
	kcs := fake.NewSimpleClientset()
	if _, err := colorFromServices(context.Background(), kcs, "prod", ""); err == nil {
		t.Errorf("expected 'service not found' error")
	}
}

func TestColorFromServicesNoColorSelector(t *testing.T) {
	kcs := fake.NewSimpleClientset(newColorService("web", ""))
	if _, err := colorFromServices(context.Background(), kcs, "prod", ""); err == nil {
		t.Errorf("expected 'color not found' error when selector lacks color")
	}
}

func TestColorFromServicesSelectorFilter(t *testing.T) {
	// A selector that matches no service must yield a not-found error.
	kcs := fake.NewSimpleClientset(newColorService("web", "blue"))
	if _, err := colorFromServices(context.Background(), kcs, "prod", "app.kubernetes.io/instance=other"); err == nil {
		t.Errorf("expected no match for non-matching selector")
	}
}

// A live service with color=blue must rotate the target to green.
func TestTargetColorFromServicesBlueToGreen(t *testing.T) {
	kcs := fake.NewSimpleClientset(newColorService("web", "blue"))
	got, err := targetColorFromServices(context.Background(), kcs, "prod", "")
	if err != nil {
		t.Fatalf("targetColorFromServices: %v", err)
	}
	if got != "green" {
		t.Errorf("target color: got %q, want green", got)
	}
}

// With no service yet (first deploy) the rotation starts at blue, with no error.
func TestTargetColorFromServicesNoService(t *testing.T) {
	kcs := fake.NewSimpleClientset()
	got, err := targetColorFromServices(context.Background(), kcs, "prod", "")
	if err != nil {
		t.Fatalf("targetColorFromServices: %v", err)
	}
	if got != "blue" {
		t.Errorf("no service must start at blue, got %q", got)
	}
}

// A real API/connectivity error must propagate so the deploy aborts instead of
// silently defaulting to blue.
func TestTargetColorFromServicesAPIError(t *testing.T) {
	kcs := fake.NewSimpleClientset()
	kcs.PrependReactor("list", "services", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("connection refused")
	})
	if _, err := targetColorFromServices(context.Background(), kcs, "prod", ""); err == nil {
		t.Errorf("expected a real API error to propagate (cluster unreachable must abort)")
	}
}
