package cmd

import "testing"

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
