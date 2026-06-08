package cmd

import (
	"os"
	"slices"
	"strings"
	"testing"

	"k8s.io/cli-runtime/pkg/resource"
)

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
