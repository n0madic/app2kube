package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

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
	rootCmd.AddCommand(NewCmdManifest())
	rootCmd.AddCommand(NewCmdStatus())
	rootCmd.AddCommand(NewCmdTrack())

	// Install a cancellable context so Ctrl-C (SIGINT) / SIGTERM cleanly cancels
	// in-flight kubedog watches, docker builds and Kubernetes API calls instead
	// of killing the process and leaking watch goroutines/informers. Commands
	// reach it via cmd.Context().
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return rootCmd.ExecuteContext(ctx)
}
