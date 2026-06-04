package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/logrusorgru/aurora"
	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

var blueGreenDeploy bool

// errNoBlueGreenColor signals that no current blue/green color could be resolved
// because no matching service exists yet (or it carries no color selector). This
// is a benign condition — the rotation simply starts at blue — and must be
// distinguished from a real API/connectivity error, which must abort the deploy.
var errNoBlueGreenColor = errors.New("no blue/green color found")

func addBlueGreenFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&blueGreenDeploy, "blue-green", false, "Enable blue-green deployment")
}

// NewCmdBlueGreen return track command
func NewCmdBlueGreen() *cobra.Command {
	blueGreenCmd := &cobra.Command{
		Use:   "blue-green",
		Short: "Commands for blue-green deployment",
	}

	// addBGSub wires a blue-green subcommand with its own appOptions so no state
	// is shared between commands. blueGreen tells initApp whether to resolve the
	// target color: rollback/prune need it, but `color` only reads the current
	// color and must not trigger a target-color lookup (the previous global
	// PersistentPreRun forced it on every subcommand, including color).
	addBGSub := func(use, short string, blueGreen bool, run func(ctx context.Context, app *app2kube.App) error) {
		c := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs}
		opts := addAppFlags(c)
		_ = c.Flags().MarkHidden("include-namespace")
		_ = c.Flags().MarkHidden("snapshot")
		c.RunE = func(cmd *cobra.Command, args []string) error {
			blueGreenDeploy = blueGreen
			app, err := opts.initApp(cmd.Context())
			if err != nil {
				return err
			}
			cmd.SilenceUsage = true
			return run(cmd.Context(), app)
		}
		blueGreenCmd.AddCommand(c)
	}

	addBGSub("color", "Get current Deployment color", false, func(ctx context.Context, app *app2kube.App) error {
		currentColor, err := getCurrentBlueGreenColor(ctx, app.Namespace, app.Labels)
		if err != nil {
			return err
		}
		fmt.Println(colorize(currentColor))
		return nil
	})

	addBGSub("rollback", "Rollback Deployment to previous color", true, func(ctx context.Context, app *app2kube.App) error {
		fmt.Printf("Check Deployment %s with previous color:\n",
			colorize(app.Deployment.BlueGreenColor, app.GetDeploymentName()))
		// Use a short, local rollback timeout instead of mutating the global
		// trackTimeout (which previously leaked a permanent 1-minute timeout into
		// any later track in the process).
		err := trackReady(ctx, app.GetDeploymentName(), app.Namespace, 1, time.Now())
		if err != nil {
			return err
		}

		kcs, err := kubeFactory.KubernetesClientSet()
		if err != nil {
			return err
		}

		services, err := kcs.CoreV1().Services(app.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: getSelector(app.Labels),
		})
		if err != nil {
			return err
		}

		if len(services.Items) > 0 {
			for _, service := range services.Items {
				fmt.Printf("Patch service %s in [%s] color\n", service.Name, colorize(app.Deployment.BlueGreenColor))
				payloadBytes := []byte(`[{
					"op": "replace",
					"path": "/spec/selector/` + strings.ReplaceAll(app2kube.LabelColor, "/", "~1") + `",
					"value": "` + app.Deployment.BlueGreenColor + `"
				}]`)
				options := metav1.PatchOptions{}
				_, err = kcs.CoreV1().Services(app.Namespace).Patch(ctx, service.Name, types.JSONPatchType, payloadBytes, options)
				if err != nil {
					return err
				}
				fmt.Println(colorize(app.Deployment.BlueGreenColor, "Rollback is successful"))
			}
		} else {
			return fmt.Errorf("no services found")
		}

		return nil
	})

	addBGSub("prune", "Prune Deployment with previous color", true, func(ctx context.Context, app *app2kube.App) error {
		if err := deleteDeployment(ctx, app.GetDeploymentName(), app.Namespace); err != nil {
			return err
		}
		// Remove the matching per-color PodDisruptionBudget too (multi-replica
		// deploys only); otherwise it is orphaned by the prune.
		kcs, err := kubeFactory.KubernetesClientSet()
		if err != nil {
			return err
		}
		if err := prunePodDisruptionBudget(ctx, kcs, app.GetDeploymentName(), app.Namespace); err != nil {
			return err
		}
		fmt.Printf("Deployment %s pruned\n", colorize(app.Deployment.BlueGreenColor, app.GetDeploymentName()))
		return nil
	})

	return blueGreenCmd
}

func colorize(s ...string) string {
	str := s[0]
	if len(s) > 1 {
		str = strings.Join(s[1:], " ")
	}
	if s[0] == "blue" {
		return aurora.BrightBlue(str).String()
	}
	return aurora.Green(str).String()
}

// nextBlueGreenColor returns the color to deploy next given the current one:
// it alternates to "green" only when the current color is "blue", otherwise
// "blue". An unknown/empty current color therefore starts at "blue".
func nextBlueGreenColor(currentColor string) string {
	if currentColor == "blue" {
		return "green"
	}
	return "blue"
}

// getTargetBlueGreenColor return the color for target deployment
func getTargetBlueGreenColor(ctx context.Context, namespace string, labels map[string]string) (string, error) {
	kcs, err := kubeFactory.KubernetesClientSet()
	if err != nil {
		return "", err
	}
	return targetColorFromServices(ctx, kcs, namespace, getSelector(labels))
}

// targetColorFromServices computes the blue/green color to deploy next from the
// live services. A genuine API/connectivity error is propagated so the deploy
// aborts on an unreachable cluster; the benign "no service yet" / "no color"
// cases are treated as no current color and start the rotation at blue.
func targetColorFromServices(ctx context.Context, kcs kubernetes.Interface, namespace, selector string) (string, error) {
	currentColor, err := colorFromServices(ctx, kcs, namespace, selector)
	if err != nil {
		if errors.Is(err, errNoBlueGreenColor) {
			return nextBlueGreenColor(""), nil
		}
		return "", err
	}
	return nextBlueGreenColor(currentColor), nil
}

// colorFromServices reads the current blue/green color from the first service
// matching the selector. It takes a kubernetes.Interface so it can be exercised
// with a fake client, without a live cluster. A real API error is returned
// as-is; the absence of a matching service or color selector is reported via the
// errNoBlueGreenColor sentinel so callers can tell the two apart.
func colorFromServices(ctx context.Context, kcs kubernetes.Interface, namespace, selector string) (string, error) {
	svc, err := kcs.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return "", fmt.Errorf("listing services: %w", err)
	}
	if len(svc.Items) == 0 {
		return "", errNoBlueGreenColor
	}

	if currentColor, ok := svc.Items[0].Spec.Selector[app2kube.LabelColor]; ok {
		return currentColor, nil
	}
	return "", errNoBlueGreenColor
}

// getCurrentBlueGreenColor return the color for current deployment
func getCurrentBlueGreenColor(ctx context.Context, namespace string, labels map[string]string) (string, error) {
	kcs, err := kubeFactory.KubernetesClientSet()
	if err != nil {
		return "", err
	}
	return colorFromServices(ctx, kcs, namespace, getSelector(labels))
}
