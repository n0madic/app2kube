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
	initAppFlags(manifestCmd)
	rootCmd.AddCommand(manifestCmd)
}

func manifest(cmd *cobra.Command, args []string) error {
	err := initApp()
	if err != nil {
		return err
	}

	secret, err := app.GetSecret()
	if err != nil {
		return err
	}
	yml, err := app2kube.PrintObj(secret, output)
	if err != nil {
		return err
	}
	fmt.Print(yml)

	configmap, err := app.GetConfigMap()
	if err != nil {
		return err
	}
	yml, err = app2kube.PrintObj(configmap, output)
	if err != nil {
		return err
	}
	fmt.Print(yml)

	claims, err := app.GetPersistentVolumeClaims()
	if err != nil {
		return err
	}
	for _, claim := range claims {
		yml, err := app2kube.PrintObj(claim, output)
		if err != nil {
			return err
		}
		fmt.Print(yml)
	}

	jobs, err := app.GetCronJobs()
	if err != nil {
		return err
	}
	for _, cron := range jobs {
		yml, err := app2kube.PrintObj(cron, output)
		if err != nil {
			return err
		}
		fmt.Print(yml)
	}

	deployment, err := app.GetDeployment()
	if err != nil {
		return err
	}
	yml, err = app2kube.PrintObj(deployment, output)
	if err != nil {
		return err
	}
	fmt.Print(yml)

	services, err := app.GetServices()
	if err != nil {
		return err
	}
	for _, service := range services {
		yml, err := app2kube.PrintObj(service, output)
		if err != nil {
			return err
		}
		fmt.Print(yml)
	}

	for _, ingressSecret := range app.GetIngressSecrets() {
		yml, err := app2kube.PrintObj(ingressSecret, output)
		if err != nil {
			return err
		}
		fmt.Print(yml)
	}

	ingress, err := app.GetIngress()
	if err != nil {
		return err
	}
	for _, ing := range ingress {
		yml, err := app2kube.PrintObj(ing, output)
		if err != nil {
			return err
		}
		fmt.Print(yml)
	}

	return nil
}
