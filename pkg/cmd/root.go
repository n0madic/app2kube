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

	rootCmd.AddCommand(NewCmdApply())
	rootCmd.AddCommand(NewCmdBlueGreen())
	rootCmd.AddCommand(NewCmdBuild())
	rootCmd.AddCommand(NewCmdCompletion())
	rootCmd.AddCommand(NewCmdConfig())
	rootCmd.AddCommand(NewCmdDelete())
	rootCmd.AddCommand(NewCmdEncrypt())
	rootCmd.AddCommand(NewCmdManifest())
	rootCmd.AddCommand(NewCmdStatus())
	rootCmd.AddCommand(NewCmdTrack())

	return rootCmd.Execute()
}
