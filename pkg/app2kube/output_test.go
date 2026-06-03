package app2kube

import "testing"

// #55: the creationTimestamp-stripping filter must preserve the final line even
// when the input does not end in a newline — otherwise the last line of a
// serialization without a trailing newline is silently dropped (data loss in
// the manifest fed to kubectl).
func TestStripCreationTimestamp(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"no trailing newline": {
			in:   "a: 1\ncreationTimestamp: null\nb: 2",
			want: "a: 1\nb: 2",
		},
		"trailing newline preserved": {
			in:   "a: 1\ncreationTimestamp: null\nb: 2\n",
			want: "a: 1\nb: 2\n",
		},
		"timestamp on last unterminated line is dropped": {
			in:   "a: 1\ncreationTimestamp: null",
			want: "a: 1\n",
		},
		"empty input": {
			in:   "",
			want: "",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := string(stripCreationTimestamp([]byte(tc.in))); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
