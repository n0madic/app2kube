package cmd

import (
	"fmt"
	"strings"

	"github.com/logrusorgru/aurora"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

	blueGreenCmd.AddCommand(&cobra.Command{
		Use:   "color",
		Short: "Get current Deployment color",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := initApp()
			if err != nil {
				return err
			}

			cmd.SilenceUsage = true

			currentColor, err := getCurrentBlueGreenColor(app.Namespace, app.Labels)
			if err != nil {
				return err
			}
			fmt.Println(colorize(currentColor))

			return nil
		},
	})

	blueGreenCmd.AddCommand(&cobra.Command{
		Use:   "rollback",
		Short: "Rollback Deployment to previous color",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := initApp()
			if err != nil {
				return err
			}

			cmd.SilenceUsage = true

			fmt.Printf("Check Deployment %s with previous color:\n",
				colorize(app.Deployment.BlueGreenColor, app.GetDeploymentName()))
			trackTimeout = 1
			err = trackReady(app.GetDeploymentName(), app.Namespace)
			if err != nil {
				return err
			}

			kcs, err := kubeFactory.KubernetesClientSet()
			if err != nil {
				return err
			}

			services, err := kcs.CoreV1().Services(app.Namespace).List(metav1.ListOptions{
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
					_, err = kcs.CoreV1().Services(app.Namespace).Patch(service.Name, types.JSONPatchType, payloadBytes)
					if err != nil {
						return err
					}
					fmt.Println(colorize(app.Deployment.BlueGreenColor, "Rollback is successful"))
				}
			} else {
				return fmt.Errorf("no services found")
			}

			return nil
		},
	})

	blueGreenCmd.AddCommand(&cobra.Command{
		Use:   "prune",
		Short: "Prune Deployment with previous color",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := initApp()
			if err != nil {
				return err
			}

			cmd.SilenceUsage = true

			kcs, err := kubeFactory.KubernetesClientSet()
			if err != nil {
				return err
			}

			err = kcs.AppsV1().Deployments(app.Namespace).Delete(app.GetDeploymentName(), &metav1.DeleteOptions{})
			if err != nil {
				return err
			}
			fmt.Printf("Deployment %s pruned\n", colorize(app.Deployment.BlueGreenColor, app.GetDeploymentName()))

			return nil
		},
	})

	for _, cmd := range blueGreenCmd.Commands() {
		addAppFlags(cmd)
		cmd.Flags().MarkHidden("include-namespace")
		cmd.Flags().MarkHidden("snapshot")
	}

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

// getTargetBlueGreenColor return the color for target deployment
func getTargetBlueGreenColor(namespace string, labels map[string]string) (string, error) {
	color := "blue"
	currentColor, _ := getCurrentBlueGreenColor(namespace, labels)
	if currentColor == "blue" {
		color = "green"
	}
	return color, nil
}

// getCurrentBlueGreenColor return the color for current deployment
func getCurrentBlueGreenColor(namespace string, labels map[string]string) (string, error) {
	kcs, err := kubeFactory.KubernetesClientSet()
	if err != nil {
		return "", err
	}

	svc, err := kcs.CoreV1().Services(namespace).List(metav1.ListOptions{
		LabelSelector: getSelector(labels),
	})
	if err != nil || len(svc.Items) == 0 {
		return "", fmt.Errorf("service not found")
	}

	if currentColor, ok := svc.Items[0].Spec.Selector["app.kubernetes.io/color"]; ok {
		return currentColor, nil
	}
	return "", fmt.Errorf("color not found")
}
