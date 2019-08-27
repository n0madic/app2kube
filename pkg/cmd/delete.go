package cmd

import (
	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/rhysd/go-fakeio"
	"github.com/spf13/cobra"

	"k8s.io/kubectl/pkg/cmd/delete"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

// NewCmdDelete return delete command
func NewCmdDelete() *cobra.Command {
	deleteFlags := delete.NewDeleteCommandFlags("containing the resource to delete.")

	deleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete resources from kubernetes",
		Run: func(cmd *cobra.Command, args []string) {
			o := deleteFlags.ToOptions(nil, ioStreams)

			app, err := initApp()
			if err != nil {
				cmdutil.CheckErr(err)
			}

			if flagIncludeNamespace && app.Namespace != "" {
				args = []string{"namespace", app.Namespace}
			} else if len(args) == 1 && args[0] == "all" {
				args = []string{"all,ingress,configmap,secret,pvc"}
				o.LabelSelector = getSelector(app.Labels)
			} else if len(args) == 0 {
				o.Filenames = []string{"-"}
				manifest, err := app.GetManifest("json", app2kube.OutputAll)
				cmdutil.CheckErr(err)

				fake := fakeio.StdinBytes([]byte{})
				defer fake.Restore()
				go func() {
					fake.StdinBytes([]byte(manifest))
					fake.CloseStdin()
				}()
			}

			cmdutil.CheckErr(o.Complete(kubeFactory, args, cmd))
			cmdutil.CheckErr(o.RunDelete(kubeFactory))
		},
	}

	addAppFlags(deleteCmd)
	deleteCmd.Flags().BoolVar(deleteFlags.IgnoreNotFound, "ignore-not-found", *deleteFlags.IgnoreNotFound, "Treat \"resource not found\" as a successful delete.")
	deleteCmd.Flags().BoolVar(deleteFlags.Wait, "wait", *deleteFlags.Wait, "If true, wait for resources to be gone before returning. This waits for finalizers.")

	return deleteCmd
}
