package cmd

import (
	"fmt"
	"strings"

	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"

	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/kubectl/pkg/cmd/delete"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

// streamDeleteResult builds the resource.Result kubectl delete operates on from
// an in-memory reader (Stream) instead of os.Stdin, for the delete-by-manifest
// path (no resource args, no selectors). It mirrors the relevant part of the
// builder chain in delete.DeleteOptions.Complete() (k8s.io/kubectl v0.29.0);
// keep it in sync with that method on a kubectl bump.
func streamDeleteResult(b *resource.Builder, namespace string, enforceNamespace bool, manifest string) *resource.Result {
	b = b.
		Unstructured().
		ContinueOnError().
		NamespaceParam(namespace).DefaultNamespace().
		Stream(strings.NewReader(manifest), "app2kube").
		RequireObject(false).
		Flatten()
	if enforceNamespace {
		b = b.RequireNamespace()
	}
	return b.Do()
}

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

// validateDeleteFlags rejects contradictory delete invocations: "all" (a scoped,
// label-selected delete of this app's resources) cannot be combined with
// --include-namespace, which deletes the whole namespace and cascades to every
// resource in it — including unrelated ones sharing the namespace. Letting
// --include-namespace silently win over an explicit "all" was a surprising,
// destructive footgun.
func validateDeleteFlags(includeNamespace bool, args []string) error {
	if includeNamespace && len(args) == 1 && args[0] == "all" {
		return fmt.Errorf(`--include-namespace cannot be combined with "all" (deleting the namespace already removes every resource in it)`)
	}
	return nil
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
			cmdutil.CheckErr(err)

			app, err := opts.initApp(cmd.Context())
			cmdutil.CheckErr(err)

			o.DryRunStrategy, err = cmdutil.GetDryRunStrategy(cmd)
			cmdutil.CheckErr(err)

			cmdutil.CheckErr(validateDeleteFlags(opts.includeNamespace, args))

			var deleteManifest string
			byManifest := false
			if opts.includeNamespace && app.Namespace != "" {
				args = []string{"namespace", app.Namespace}
			} else if len(args) == 1 && args[0] == "all" {
				// The kubectl resource list is derived per-app from the generator
				// registry (output.go) so it cannot drift — notably it names
				// poddisruptionbudgets (excluded by kubectl's "all" category) and
				// includes the cert-manager Certificate only when this app uses
				// letsencrypt, avoiding a reference to a CRD the cluster may lack.
				args = []string{app.DeleteResourceTypes()}
				o.LabelSelector, err = scopedSelector(app.Labels)
				cmdutil.CheckErr(err)
			} else if len(args) == 0 {
				deleteManifest, err = app.GetManifest("json", app2kube.OutputAll)
				cmdutil.CheckErr(err)
				byManifest = true
			}

			cmdutil.CheckErr(o.Complete(kubeFactory, args, cmd))

			// For delete-by-manifest, Complete built an (empty) Result from the
			// FilenameOptions; replace it with one fed from an in-memory reader so
			// RunDelete never reads os.Stdin. Complete still wired up the mapper,
			// dynamic client and dry-run strategy that DeleteResult needs.
			if byManifest {
				cmdNamespace, enforceNamespace, err := kubeFactory.ToRawKubeConfigLoader().Namespace()
				cmdutil.CheckErr(err)
				r := streamDeleteResult(kubeFactory.NewBuilder(), cmdNamespace, enforceNamespace, deleteManifest)
				cmdutil.CheckErr(r.Err())
				o.Result = r
			}

			cmdutil.CheckErr(o.RunDelete(kubeFactory))
		},
	}

	opts = addAppFlags(deleteCmd)
	addBlueGreenFlag(deleteCmd, opts)

	deleteCmd.Flags().BoolVar(&flagAllInstances, "all-instances", false, "Delete all instances of application with the cmd 'delete all'")
	deleteCmd.Flags().BoolVar(deleteFlags.IgnoreNotFound, "ignore-not-found", *deleteFlags.IgnoreNotFound, "Treat \"resource not found\" as a successful delete.")
	deleteCmd.Flags().BoolVar(deleteFlags.Wait, "wait", *deleteFlags.Wait, "If true, wait for resources to be gone before returning. This waits for finalizers.")
	deleteCmd.Flags().String("dry-run", "none", "Must be \"none\", \"server\", or \"client\". If client strategy, only print the object that would be sent, without sending it. If server strategy, submit server-side request without persisting the resource.")

	return deleteCmd
}
