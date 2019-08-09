package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{Use: "app2kube"}

// Execute cmd
func Execute(version string) error {
	rootCmd.Version = version
	rootCmd.Short = fmt.Sprintf("Kubernetes application deployment (app2kube %s)", rootCmd.Version)
	return rootCmd.Execute()
}
