package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "DEV"

var rootCmd = &cobra.Command{
	Use:     "app2kube",
	Short:   fmt.Sprintf("Kubernetes application deployment (app2kube %s)", version),
	Version: version,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
