package cmd

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n0madic/app2kube/pkg/app2kube"
)

// #64: `config encrypt --string -` reads the plaintext from stdin instead of
// argv (which leaks into shell history / ps); a literal value is returned as-is.
func TestResolveEncryptString(t *testing.T) {
	if got, err := resolveEncryptString("secret", strings.NewReader("ignored")); err != nil || got != "secret" {
		t.Errorf("literal must pass through: got %q, err %v", got, err)
	}
	got, err := resolveEncryptString("-", strings.NewReader("fromstdin\n"))
	if err != nil {
		t.Fatalf("resolveEncryptString stdin: %v", err)
	}
	if got != "fromstdin" {
		t.Errorf("'-' must read stdin (newline trimmed): got %q", got)
	}
}

// #53: the file-open failure is wrapped with %w, so callers/tests can match the
// underlying error with errors.Is (here fs.ErrNotExist) instead of only seeing
// a flattened string.
func TestRunEncryptFileOpenErrorIsWrapped(t *testing.T) {
	t.Setenv(app2kube.EnvPassword, "pass")

	err := runEncrypt("", app2kube.ValueFiles{filepath.Join(t.TempDir(), "does-not-exist.yaml")})
	if err == nil {
		t.Fatal("expected an error for a missing value file")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error not wrapped with %%w (errors.Is fs.ErrNotExist failed): %v", err)
	}
}

// Regression: a blank line inside the secrets: block must not end the section;
// secrets after it must still be encrypted instead of left in plaintext.
func TestEncryptBlankLineInSecrets(t *testing.T) {
	t.Setenv(app2kube.EnvPassword, "pass")

	dir := t.TempDir()
	file := filepath.Join(dir, "secrets.yaml")
	content := "name: example\nsecrets:\n  a: one\n\n  b: two\nother: keep\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if err := runEncrypt("", app2kube.ValueFiles{file}); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	out, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)

	if !strings.Contains(s, "a: AES#") {
		t.Errorf("secret 'a' not encrypted:\n%s", s)
	}
	if !strings.Contains(s, "b: AES#") {
		t.Errorf("secret 'b' after blank line not encrypted (plaintext leak):\n%s", s)
	}
	if !strings.Contains(s, "other: keep") {
		t.Errorf("non-secret content not preserved:\n%s", s)
	}
}
