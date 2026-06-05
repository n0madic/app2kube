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

// parseBuildPlatforms must mirror docker/cli's classic build path: an empty
// value yields no constraint, a valid value yields exactly one parsed platform,
// and an unparsable value is reported up front (before ImageBuild is called).
func TestParseBuildPlatforms(t *testing.T) {
	// Empty → no constraint, no error.
	got, err := parseBuildPlatforms("")
	if err != nil {
		t.Fatalf("empty platform must not error: %v", err)
	}
	if got != nil {
		t.Errorf("empty platform must yield nil slice, got %v", got)
	}

	// Valid → exactly one element with the expected OS/Architecture.
	got, err = parseBuildPlatforms("linux/amd64")
	if err != nil {
		t.Fatalf("valid platform must not error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("valid platform must yield one element, got %d", len(got))
	}
	if got[0].OS != "linux" || got[0].Architecture != "amd64" {
		t.Errorf("got OS=%q Arch=%q, want linux/amd64", got[0].OS, got[0].Architecture)
	}

	// Garbage → error, no panic.
	if _, err := parseBuildPlatforms("!!!"); err == nil {
		t.Errorf("expected error for an unparsable platform")
	}
}

// TestBuildCommandFlags pins the classic-parity flag surface: the new builder
// flags must exist, and --file must default to empty (so the helper resolves
// PATH/Dockerfile, matching docker/cli). --file carries no -f shorthand: that
// letter is already bound to --values by addAppFlags, and the build command must
// not panic on a shorthand collision.
func TestBuildCommandFlags(t *testing.T) {
	cmd := NewCmdBuild()
	fs := cmd.Flags()

	for _, name := range []string{"no-cache", "target", "platform", "label"} {
		if fs.Lookup(name) == nil {
			t.Errorf("missing flag --%s", name)
		}
	}

	file := fs.Lookup("file")
	if file == nil {
		t.Fatal("missing flag --file")
	}
	if file.DefValue != "" {
		t.Errorf("--file default: got %q, want empty", file.DefValue)
	}
	if file.Shorthand != "" {
		t.Errorf("--file must not take a shorthand (-f is --values): got %q", file.Shorthand)
	}

	// -f must remain bound to --values, not be hijacked by --file.
	if values := fs.ShorthandLookup("f"); values == nil || values.Name != "values" {
		t.Errorf("-f shorthand must resolve to --values, got %v", values)
	}
}

// Regression: app2kube pins the legacy builder (BuilderV1), which cannot build
// a foreign platform on the host's native arch (the containerd image store
// rejects the platform-mismatched intermediate images). Inheriting
// $DOCKER_DEFAULT_PLATFORM as the --platform default therefore silently forces
// an unbuildable cross-arch build (e.g. linux/amd64 on an arm64 Mac). The
// default must be empty so the build is native unless --platform is passed
// explicitly.
func TestBuildPlatformDefaultIgnoresEnv(t *testing.T) {
	t.Setenv("DOCKER_DEFAULT_PLATFORM", "linux/amd64")
	cmd := NewCmdBuild()
	if got := cmd.Flags().Lookup("platform").DefValue; got != "" {
		t.Errorf("--platform default must not inherit $DOCKER_DEFAULT_PLATFORM, got %q", got)
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
