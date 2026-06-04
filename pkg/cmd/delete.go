package cmd

import (
	"fmt"

	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"

	"k8s.io/kubectl/pkg/cmd/delete"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

// deleteAllResourceTypes is the comma-separated kubectl resource list `delete
// all` removes by app label selector. kubectl's "all" category does not cover
// the namespaced extras app2kube emits, so each is named explicitly; it must
// stay in sync with the resource generators (output.go) — notably
// poddisruptionbudgets, which "all" excludes and which would otherwise survive
// teardown and keep blocking node drains.
const deleteAllResourceTypes = "all,ingress,configmap,secret,pvc,poddisruptionbudgets"

// deleteArgs accepts no positional arguments or exactly "all"; anything else
// (which delete used to forward verbatim to kubectl with no app-aware selector)
// is rejected (#63).
func deleteArgs(cmd *cobra.Command, args []string) error {
	switch len(args) {
	case 0:
		return nil
	case 1:
		if args[0] == "all" {
			return nil
		}
		return fmt.Errorf("invalid argument %q (the only accepted positional argument is \"all\")", args[0])
	default:
		return fmt.Errorf("accepts at most one argument (\"all\"), received %d", len(args))
	}
}

// NewCmdDelete return delete command
func NewCmdDelete() *cobra.Command {
	deleteFlags := delete.NewDeleteCommandFlags("containing the resource to delete.")
	var opts *appOptions

	deleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete resources from kubernetes",
		Args:  deleteArgs,
		Run: func(cmd *cobra.Command, args []string) {
			o, err := deleteFlags.ToOptions(nil, ioStreams)
			if err != nil {
				cmdutil.CheckErr(err)
			}

			app, err := opts.initApp(cmd.Context())
			if err != nil {
				cmdutil.CheckErr(err)
			}

			o.DryRunStrategy, err = cmdutil.GetDryRunStrategy(cmd)
			cmdutil.CheckErr(err)

			var waitStdin func() error
			if opts.includeNamespace && app.Namespace != "" {
				args = []string{"namespace", app.Namespace}
			} else if len(args) == 1 && args[0] == "all" {
				args = []string{deleteAllResourceTypes}
				o.LabelSelector, err = scopedSelector(app.Labels)
				cmdutil.CheckErr(err)
			} else if len(args) == 0 {
				o.Filenames = []string{"-"}
				manifest, err := app.GetManifest("json", app2kube.OutputAll)
				cmdutil.CheckErr(err)

				waitStdin, err = withStdin([]byte(manifest))
				cmdutil.CheckErr(err)
			}

			cmdutil.CheckErr(o.Complete(kubeFactory, args, cmd))
			runErr := o.RunDelete(kubeFactory)
			if waitStdin != nil {
				if feedErr := waitStdin(); feedErr != nil && runErr == nil {
					runErr = feedErr
				}
			}
			cmdutil.CheckErr(runErr)
		},
	}

	opts = addAppFlags(deleteCmd)
	addBlueGreenFlag(deleteCmd)

	deleteCmd.Flags().BoolVar(&flagAllInstances, "all-instances", false, "Delete all instances of application with the cmd 'delete all'")
	deleteCmd.Flags().BoolVar(deleteFlags.IgnoreNotFound, "ignore-not-found", *deleteFlags.IgnoreNotFound, "Treat \"resource not found\" as a successful delete.")
	deleteCmd.Flags().BoolVar(deleteFlags.Wait, "wait", *deleteFlags.Wait, "If true, wait for resources to be gone before returning. This waits for finalizers.")
	deleteCmd.Flags().String("dry-run", "none", "Must be \"none\", \"server\", or \"client\". If client strategy, only print the object that would be sent, without sending it. If server strategy, submit server-side request without persisting the resource.")

	return deleteCmd
}
