package cmd

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/kubectl/pkg/cmd/delete"
	cmdtesting "k8s.io/kubectl/pkg/cmd/testing"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

// fakeDeleteCmd is a minimal cobra command carrying only the flag
// DeleteOptions.Complete inspects (--dry-run), matching how kubectl's own
// delete tests drive Complete.
func fakeDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{Run: func(*cobra.Command, []string) {}}
	cmdutil.AddDryRunFlag(cmd)
	return cmd
}

// Regression: `app2kube delete` with no positional args renders the manifest and
// feeds it to kubectl through an in-memory Stream, but kubectl's
// DeleteOptions.Complete still runs first (to wire up the mapper, dynamic client
// and dry-run strategy). Complete builds a resource.Result from the
// FilenameOptions/args/selectors and rejects an empty set with the very error the
// user hit in GitLab CI: "You must provide one or more resources by argument or
// filename." The delete-by-manifest path must therefore seed o.Filenames=["-"]
// so Complete succeeds; the stdin-backed Result it builds is overwritten by the
// Stream result before it is ever read. This guards both halves of that contract.
func TestDeleteByManifestCompleteRequiresStdinFilename(t *testing.T) {
	cmdtesting.InitTestErrorHandler(t)

	newOptions := func() (*delete.DeleteOptions, cmdutil.Factory) {
		tf := cmdtesting.NewTestFactory().WithNamespace("test")
		t.Cleanup(tf.Cleanup)
		tf.ClientConfigVal = cmdtesting.DefaultClientConfig()

		streams, _, _, _ := genericiooptions.NewTestIOStreams()
		o, err := delete.NewDeleteCommandFlags("containing the resource to delete.").ToOptions(nil, streams)
		if err != nil {
			t.Fatalf("ToOptions: %v", err)
		}
		return o, tf
	}

	// Bug reproduction: the byManifest invocation is empty args + empty
	// FilenameOptions, and Complete aborts with the CI-visible error.
	t.Run("empty inputs are rejected", func(t *testing.T) {
		o, tf := newOptions()
		err := o.Complete(tf, []string{}, fakeDeleteCmd())
		if err == nil || !strings.Contains(err.Error(), "You must provide one or more resources") {
			t.Fatalf("want missing-resource error, got: %v", err)
		}
	})

	// Fix verification: seeding the "-" filename (as the delete-by-manifest path
	// does) makes Complete accept the otherwise-empty invocation.
	t.Run("stdin filename is accepted", func(t *testing.T) {
		o, tf := newOptions()
		o.Filenames = []string{"-"}
		if err := o.Complete(tf, []string{}, fakeDeleteCmd()); err != nil {
			t.Fatalf("Complete with stdin filename: %v", err)
		}
	})
}

// Delete-by-manifest must feed the rendered manifest to kubectl through an
// in-memory reader (resource.Builder.Stream) instead of hijacking the process's
// global os.Stdin. This parses a multi-document manifest into the resource.Infos
// kubectl delete would build, and asserts os.Stdin is left untouched.
func TestStreamDeleteResultParsesManifestWithoutStdin(t *testing.T) {
	orig := os.Stdin
	manifest := strings.Join([]string{
		`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"cm-a","namespace":"app"},"data":{"k":"v"}}`,
		`{"apiVersion":"v1","kind":"Service","metadata":{"name":"svc-b","namespace":"app"}}`,
	}, "\n")

	r := streamDeleteResult(resource.NewLocalBuilder(), "app", false, manifest)
	if err := r.Err(); err != nil {
		t.Fatalf("streamDeleteResult: %v", err)
	}
	infos, err := r.Infos()
	if err != nil {
		t.Fatalf("infos: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("got %d infos, want 2", len(infos))
	}

	names := []string{infos[0].Name, infos[1].Name}
	for _, want := range []string{"cm-a", "svc-b"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing object %q in %v", want, names)
		}
	}
	for _, info := range infos {
		if info.Namespace != "app" {
			t.Errorf("object %q namespace = %q, want app", info.Name, info.Namespace)
		}
	}
	if os.Stdin != orig {
		t.Errorf("os.Stdin was modified; delete must not touch global stdin")
	}
}

// validateDeleteFlags must reject the contradictory "all" + --include-namespace
// combination (which would silently nuke the whole namespace instead of doing
// the scoped, label-selected delete the operator asked for) while leaving every
// other invocation valid.
func TestValidateDeleteFlags(t *testing.T) {
	cases := []struct {
		name             string
		includeNamespace bool
		args             []string
		wantErr          bool
	}{
		{"all alone", false, []string{"all"}, false},
		{"no args", false, nil, false},
		{"include-namespace, no args", true, nil, false},
		{"include-namespace + all is rejected", true, []string{"all"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDeleteFlags(tc.includeNamespace, tc.args)
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
