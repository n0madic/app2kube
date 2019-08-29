package cmd

import (
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var blueGreenDeploy bool

func addBlueGreenFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&blueGreenDeploy, "blue-green", false, "Enable blue-green deployment")
}

// getBlueGreenColor return the color for deployment
func getBlueGreenColor(namespace string, labels map[string]string) (string, error) {
	color := "blue"

	kcs, err := kubeFactory.KubernetesClientSet()
	if err != nil {
		return "", err
	}

	svc, err := kcs.CoreV1().Services(namespace).List(metav1.ListOptions{
		LabelSelector: getSelector(labels),
	})
	if err == nil && len(svc.Items) > 0 {
		if currentColor, ok := svc.Items[0].Spec.Selector["app.kubernetes.io/color"]; ok {
			if currentColor == "blue" {
				color = "green"
			}
		}
	}

	return color, nil
}
