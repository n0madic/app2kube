package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/logrusorgru/aurora"
	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

var blueGreenDeploy bool

func addBlueGreenFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&blueGreenDeploy, "blue-green", false, "Enable blue-green deployment")
}

// NewCmdBlueGreen return track command
func NewCmdBlueGreen() *cobra.Command {
	blueGreenCmd := &cobra.Command{
		Use:   "blue-green",
		Short: "Commands for blue-green deployment",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			blueGreenDeploy = true
		},
	}

	// addBGSub wires a blue-green subcommand with its own appOptions so no
	// state is shared between commands.
	addBGSub := func(use, short string, run func(app *app2kube.App) error) {
		c := &cobra.Command{Use: use, Short: short}
		opts := addAppFlags(c)
		c.Flags().MarkHidden("include-namespace")
		c.Flags().MarkHidden("snapshot")
		c.RunE = func(cmd *cobra.Command, args []string) error {
			app, err := opts.initApp()
			if err != nil {
				return err
			}
			cmd.SilenceUsage = true
			return run(app)
		}
		blueGreenCmd.AddCommand(c)
	}

	addBGSub("color", "Get current Deployment color", func(app *app2kube.App) error {
		currentColor, err := getCurrentBlueGreenColor(app.Namespace, app.Labels)
		if err != nil {
			return err
		}
		fmt.Println(colorize(currentColor))
		return nil
	})

	addBGSub("rollback", "Rollback Deployment to previous color", func(app *app2kube.App) error {
		fmt.Printf("Check Deployment %s with previous color:\n",
			colorize(app.Deployment.BlueGreenColor, app.GetDeploymentName()))
		trackTimeout = 1
		err := trackReady(app.GetDeploymentName(), app.Namespace)
		if err != nil {
			return err
		}

		kcs, err := kubeFactory.KubernetesClientSet()
		if err != nil {
			return err
		}

		services, err := kcs.CoreV1().Services(app.Namespace).List(context.TODO(), metav1.ListOptions{
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
					"path": "/spec/selector/app.kubernetes.io~1color",
					"value": "` + app.Deployment.BlueGreenColor + `"
				}]`)
				options := metav1.PatchOptions{}
				_, err = kcs.CoreV1().Services(app.Namespace).Patch(context.TODO(), service.Name, types.JSONPatchType, payloadBytes, options)
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

	addBGSub("prune", "Prune Deployment with previous color", func(app *app2kube.App) error {
		if err := deleteDeployment(app.GetDeploymentName(), app.Namespace); err != nil {
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
func getTargetBlueGreenColor(namespace string, labels map[string]string) (string, error) {
	currentColor, _ := getCurrentBlueGreenColor(namespace, labels)
	return nextBlueGreenColor(currentColor), nil
}

// colorFromServices reads the current blue/green color from the first service
// matching the selector. It takes a kubernetes.Interface so it can be exercised
// with a fake client, without a live cluster.
func colorFromServices(kcs kubernetes.Interface, namespace, selector string) (string, error) {
	svc, err := kcs.CoreV1().Services(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil || len(svc.Items) == 0 {
		return "", fmt.Errorf("service not found")
	}

	if currentColor, ok := svc.Items[0].Spec.Selector["app.kubernetes.io/color"]; ok {
		return currentColor, nil
	}
	return "", fmt.Errorf("color not found")
}

// getCurrentBlueGreenColor return the color for current deployment
func getCurrentBlueGreenColor(namespace string, labels map[string]string) (string, error) {
	kcs, err := kubeFactory.KubernetesClientSet()
	if err != nil {
		return "", err
	}
	return colorFromServices(kcs, namespace, getSelector(labels))
}
