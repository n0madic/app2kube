package main

import (
	"fmt"

	"github.com/n0madic/app2kube"
	"github.com/spf13/cobra"
)

var manifestCmd = &cobra.Command{
	Use:   "manifest",
	Short: "Generate kubernetes manifests for an application",
	RunE:  manifest,
}

func init() {
	manifestCmd.Flags().StringVarP(&output, "output", "o", "yaml", "Output format")
	rootCmd.AddCommand(manifestCmd)
}

func manifest(cmd *cobra.Command, args []string) error {
	for _, claim := range app.GetPersistentVolumeClaims() {
		fmt.Print(app2kube.PrintObj(claim, output))
	}

	fmt.Print(app2kube.PrintObj(app.GetSecret(), output))

	for _, cron := range app.GetCronJobs() {
		fmt.Print(app2kube.PrintObj(cron, output))
	}

	fmt.Print(app2kube.PrintObj(app.GetDeployment(), output))

	for _, service := range app.GetServices() {
		fmt.Print(app2kube.PrintObj(service, output))
	}

	for _, ingressSecret := range app.GetIngressSecrets() {
		fmt.Print(app2kube.PrintObj(ingressSecret, output))
	}

	for _, ingress := range app.GetIngress(defaultIngress) {
		fmt.Print(app2kube.PrintObj(ingress, output))
	}

	return nil
}
