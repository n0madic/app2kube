package cmd

import (
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
	for k, v := range labels {
		selectorList = append(selectorList, k+"="+v)
	}
	return strings.Join(selectorList, ",")
}

func deleteDeployment(name, namespace string) error {
	kcs, err := kubeFactory.KubernetesClientSet()
	if err != nil {
		return err
	}

	err = kcs.AppsV1().Deployments(namespace).Delete(name, &metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	return nil
}
