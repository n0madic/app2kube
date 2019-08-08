package main

import (
	"os"
	"strings"

	"github.com/n0madic/app2kube"
	"github.com/rhysd/go-fakeio"
	"github.com/spf13/cobra"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/kubectl/pkg/cmd/apply"
	"k8s.io/kubectl/pkg/cmd/delete"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

var (
	kubeConfigFlags *genericclioptions.ConfigFlags
)

func init() {
	kubeConfigFlags = genericclioptions.NewConfigFlags(true)
	kubeConfigFlags.AddFlags(rootCmd.PersistentFlags())

	matchVersionKubeConfigFlags := cmdutil.NewMatchVersionFlags(kubeConfigFlags)
	f := cmdutil.NewFactory(matchVersionKubeConfigFlags)

	ioStreams := genericclioptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr}

	// apply command
	o := apply.NewApplyOptions(ioStreams)

	applyCmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply a configuration to a resource in kubernetes",
		Run: func(cmd *cobra.Command, args []string) {
			o.DeleteFlags.FileNameFlags.Filenames = &[]string{"-"}
			o.Overwrite = true
			o.PruneWhitelist = []string{"/v1/Namespace"}

			if o.Namespace != "" {
				o.EnforceNamespace = true
			}

			cmdutil.AddServerSideApplyFlags(cmd)
			cmdutil.AddValidateFlags(cmd)
			cmdutil.CheckErr(o.Complete(f, cmd))

			err := initApp()
			if err != nil {
				cmdutil.CheckErr(err)
			}
			if o.Prune {
				o.Selector = getSelector(app.Labels)
			}

			manifest, err := app.GetManifest([]app2kube.OutputResource{app2kube.OutputAll}, "json")
			cmdutil.CheckErr(err)

			if flagIncludeNamespace {
				namespace, err := app.GetManifest([]app2kube.OutputResource{app2kube.OutputNamespace}, "json")
				cmdutil.CheckErr(err)
				manifest = namespace + manifest
			}

			fake := fakeio.StdinBytes([]byte{})
			defer fake.Restore()
			go func() {
				fake.StdinBytes([]byte(manifest))
				fake.CloseStdin()
			}()

			cmdutil.CheckErr(o.Run())
		},
	}

	addAppFlags(applyCmd)
	o.PrintFlags.AddFlags(applyCmd)

	applyCmd.Flags().Bool("dry-run", false, "If true, only print the object that would be sent, without sending it. Warning: --dry-run cannot accurately output the result of merging the local manifest and the server-side data. Use --server-dry-run to get the merged result instead.")
	applyCmd.Flags().BoolVar(&o.ServerDryRun, "server-dry-run", o.ServerDryRun, "If true, request will be sent to server with dry-run flag, which means the modifications won't be persisted.")
	applyCmd.Flags().BoolVar(&o.Prune, "prune", o.Prune, "Automatically delete resource objects, including the uninitialized ones, that do not appear in the configs and are created by either apply.")

	rootCmd.AddCommand(applyCmd)

	// delete command
	deleteFlags := delete.NewDeleteCommandFlags("containing the resource to delete.")

	deleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete resources from kubernetes",
		Run: func(cmd *cobra.Command, args []string) {
			o := deleteFlags.ToOptions(nil, ioStreams)
			o.Filenames = []string{"-"}

			cmdutil.CheckErr(o.Complete(f, args, cmd))

			err := initApp()
			if err != nil {
				cmdutil.CheckErr(err)
			}

			manifest, err := app.GetManifest([]app2kube.OutputResource{app2kube.OutputAll}, "json")
			cmdutil.CheckErr(err)

			if flagIncludeNamespace {
				namespace, err := app.GetManifest([]app2kube.OutputResource{app2kube.OutputNamespace}, "json")
				cmdutil.CheckErr(err)
				manifest = namespace + manifest
			}

			fake := fakeio.StdinBytes([]byte{})
			defer fake.Restore()
			go func() {
				fake.StdinBytes([]byte(manifest))
				fake.CloseStdin()
			}()

			cmdutil.CheckErr(o.RunDelete(f))
		},
	}

	addAppFlags(deleteCmd)
	deleteCmd.Flags().BoolVar(deleteFlags.IgnoreNotFound, "ignore-not-found", *deleteFlags.IgnoreNotFound, "Treat \"resource not found\" as a successful delete.")
	deleteCmd.Flags().BoolVar(deleteFlags.Wait, "wait", *deleteFlags.Wait, "If true, wait for resources to be gone before returning. This waits for finalizers.")

	rootCmd.AddCommand(deleteCmd)
}

func getSelector(labels map[string]string) string {
	var selectorList = make([]string, 0, len(labels))
	for k, v := range labels {
		selectorList = append(selectorList, k+"="+v)
	}
	return strings.Join(selectorList, ",")
}
