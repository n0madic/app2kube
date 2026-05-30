package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

var (
	ioStreams       = genericclioptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr}
	kubeConfigFlags = genericclioptions.NewConfigFlags(true)
	kubeFactory     cmdutil.Factory
)

var (
	flagAllApplications bool
	flagAllInstances    bool
)

func init() {
	kubeConfigFlags.AddFlags(rootCmd.PersistentFlags())

	rootCmd.PersistentFlags().MarkHidden("as")
	rootCmd.PersistentFlags().MarkHidden("as-group")
	rootCmd.PersistentFlags().MarkHidden("cache-dir")

	matchVersionKubeConfigFlags := cmdutil.NewMatchVersionFlags(kubeConfigFlags)
	kubeFactory = cmdutil.NewFactory(matchVersionKubeConfigFlags)

	if *kubeConfigFlags.Context == "" && os.Getenv("KUBECONTEXT") != "" {
		*kubeConfigFlags.Context = os.Getenv("KUBECONTEXT")
	}
}

func getSelector(labels map[string]string) string {
	var selectorList = make([]string, 0, len(labels))
	if flagAllApplications {
		selectorList = append(selectorList, "app.kubernetes.io/managed-by=app2kube")
	} else {
		for k, v := range labels {
			if flagAllInstances && k == "app.kubernetes.io/instance" {
				continue
			}
			selectorList = append(selectorList, k+"="+v)
		}
	}
	return strings.Join(selectorList, ",")
}

// scopedSelector builds the label selector for destructive operations (apply
// --prune, delete all). Unlike getSelector it refuses to return a selector that
// could match foreign or unrelated objects: it errors when the result is empty
// or, outside the deliberate --all-applications mode, when it is not scoped to a
// specific application (missing app.kubernetes.io/name). This prevents a
// catastrophic match-all prune/delete.
func scopedSelector(labels map[string]string) (string, error) {
	selector := getSelector(labels)
	if selector == "" {
		return "", fmt.Errorf("refusing to run a destructive operation with an empty label selector")
	}
	if flagAllApplications {
		// Deliberate cluster/namespace-wide app2kube scope requested by the user.
		return selector, nil
	}
	if _, ok := labels["app.kubernetes.io/name"]; !ok {
		return "", fmt.Errorf("refusing to run a destructive operation: selector %q is not scoped to an application (missing app.kubernetes.io/name)", selector)
	}
	return selector, nil
}

func deleteDeployment(name, namespace string) error {
	kcs, err := kubeFactory.KubernetesClientSet()
	if err != nil {
		return err
	}

	err = kcs.AppsV1().Deployments(namespace).Delete(context.TODO(), name, metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	return nil
}
