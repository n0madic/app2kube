package cmd

import (
	"errors"
	"strings"
	"testing"
)

// #10 regression: Docker Hub credentials from `docker login` are stored under
// the legacy index key; a lookup keyed on the "docker.io" domain must be
// translated to that key, while every other registry is keyed by its domain.
func TestRegistryAuthKey(t *testing.T) {
	cases := map[string]string{
		"docker.io":           "https://index.docker.io/v1/",
		"registry.gitlab.com": "registry.gitlab.com",
		"ghcr.io":             "ghcr.io",
		"registry.io:5000":    "registry.io:5000",
	}
	for domain, want := range cases {
		if got := registryAuthKey(domain); got != want {
			t.Errorf("registryAuthKey(%q): got %q, want %q", domain, got, want)
		}
	}
}

// #41: --password-stdin must reject flag combinations that misuse stdin and
// require a username to be meaningful.
func TestValidatePasswordStdin(t *testing.T) {
	// Not using --password-stdin: anything goes.
	if err := validatePasswordStdin(false, true, ""); err != nil {
		t.Errorf("no --password-stdin must never error: %v", err)
	}
	// --password-stdin + --file - both read stdin → reject.
	if err := validatePasswordStdin(true, true, "user"); err == nil {
		t.Errorf("expected error: --password-stdin with Dockerfile from stdin")
	}
	// --password-stdin without a username → reject.
	if err := validatePasswordStdin(true, false, ""); err == nil {
		t.Errorf("expected error: --password-stdin without username")
	}
	// Valid combination.
	if err := validatePasswordStdin(true, false, "user"); err != nil {
		t.Errorf("valid --password-stdin must pass: %v", err)
	}
}

// #41: the stdin secret read must be bounded and trim the trailing newline a
// pipe/heredoc adds (otherwise the newline breaks auth).
func TestReadStdinSecret(t *testing.T) {
	got, err := readStdinSecret(strings.NewReader("hunter2\n"))
	if err != nil {
		t.Fatalf("readStdinSecret: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("trailing newline not trimmed: got %q", got)
	}
	// CRLF is also trimmed.
	if got, _ := readStdinSecret(strings.NewReader("pw\r\n")); got != "pw" {
		t.Errorf("CRLF not trimmed: got %q", got)
	}
	// The read is bounded: a payload larger than the cap is truncated, not OOM.
	huge := strings.Repeat("x", maxStdinSecretBytes+100)
	got, err = readStdinSecret(strings.NewReader(huge))
	if err != nil {
		t.Fatalf("readStdinSecret(huge): %v", err)
	}
	if int64(len(got)) != maxStdinSecretBytes {
		t.Errorf("read not bounded: got %d bytes, want %d", len(got), maxStdinSecretBytes)
	}
}

// #56: pushing multiple tags must continue past a failing tag and aggregate the
// errors, so a partial-push state is reported rather than masked by the first
// error.
func TestPushTags(t *testing.T) {
	var pushed []string
	err := pushTags([]string{"a", "b", "c"}, func(tag string) error {
		pushed = append(pushed, tag)
		if tag == "b" {
			return errors.New("boom")
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected an aggregated error when a tag fails")
	}
	// All tags were attempted despite b failing.
	if strings.Join(pushed, ",") != "a,b,c" {
		t.Errorf("all tags must be attempted, got %v", pushed)
	}
	if !strings.Contains(err.Error(), "b") {
		t.Errorf("aggregated error must name the failing tag: %v", err)
	}

	// All-success → nil.
	if err := pushTags([]string{"a", "b"}, func(string) error { return nil }); err != nil {
		t.Errorf("all-success must return nil, got %v", err)
	}
}
