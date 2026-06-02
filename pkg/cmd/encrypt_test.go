package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n0madic/app2kube/pkg/app2kube"
)

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
