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

// decryptSecret reads back an encrypted secrets file, extracts the AES blob for
// key and decrypts it, so tests can assert on the plaintext that was actually
// encrypted rather than only on the "AES#" prefix.
func decryptSecret(t *testing.T, file, key string) string {
	t.Helper()
	out, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != key {
			continue
		}
		blob := strings.TrimPrefix(strings.TrimSpace(parts[1]), "AES#")
		plain, err := app2kube.DecryptAES("pass", blob)
		if err != nil {
			t.Fatalf("decrypt %s: %v", key, err)
		}
		return plain
	}
	t.Fatalf("key %q not found in:\n%s", key, string(out))
	return ""
}

// Regression: a single-quoted YAML value must be decoded per YAML rules before
// encryption, so the stored secret is the real value ("it's") and not the literal
// token including the quotes ("'it”s'") that the old strconv.Unquote left intact
// (Go quoting != YAML quoting).
func TestEncryptDecodesSingleQuotedValue(t *testing.T) {
	t.Setenv(app2kube.EnvPassword, "pass")

	dir := t.TempDir()
	file := filepath.Join(dir, "secrets.yaml")
	content := "name: example\nsecrets:\n  q: 'it''s'\nother: keep\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if err := runEncrypt("", app2kube.ValueFiles{file}); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if got := decryptSecret(t, file, "q"); got != "it's" {
		t.Errorf("single-quoted value decoded wrong: got %q, want %q", got, "it's")
	}
}

// Regression: an inline comment after a secret value must not be folded into the
// ciphertext; only the scalar ("secret") is encrypted, as YAML scalar parsing
// strips the trailing comment.
func TestEncryptStripsInlineComment(t *testing.T) {
	t.Setenv(app2kube.EnvPassword, "pass")

	dir := t.TempDir()
	file := filepath.Join(dir, "secrets.yaml")
	content := "name: example\nsecrets:\n  token: secret # note\nother: keep\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if err := runEncrypt("", app2kube.ValueFiles{file}); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if got := decryptSecret(t, file, "token"); got != "secret" {
		t.Errorf("inline comment folded into ciphertext: got %q, want %q", got, "secret")
	}
}

// #5: decodeYAMLScalar surfaces a stripped inline comment so the caller can warn
// when a '#' that may be part of an unquoted secret was dropped; a quoted value
// keeps the '#' and reports no comment.
func TestDecodeYAMLScalarComment(t *testing.T) {
	cases := []struct {
		raw         string
		wantVal     string
		wantComment string
		wantOK      bool
	}{
		{" hunter2 #1", "hunter2", "#1", true},
		{" secret # note", "secret", "# note", true},
		{" plain", "plain", "", true},
		{" 'kept #x'", "kept #x", "", true},
		{" no-space#1", "no-space#1", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			val, comment, ok := decodeYAMLScalar(tc.raw)
			if ok != tc.wantOK || val != tc.wantVal || comment != tc.wantComment {
				t.Errorf("decodeYAMLScalar(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.raw, val, comment, ok, tc.wantVal, tc.wantComment, tc.wantOK)
			}
		})
	}
}

// #5: a quoted secret containing a '#' (which would otherwise be read as an
// inline comment and silently truncated) is preserved end-to-end.
func TestEncryptPreservesQuotedHash(t *testing.T) {
	t.Setenv(app2kube.EnvPassword, "pass")

	dir := t.TempDir()
	file := filepath.Join(dir, "secrets.yaml")
	content := "name: example\nsecrets:\n  token: 'hunter2 #1'\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if err := runEncrypt("", app2kube.ValueFiles{file}); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if got := decryptSecret(t, file, "token"); got != "hunter2 #1" {
		t.Errorf("quoted hash truncated: got %q, want %q", got, "hunter2 #1")
	}
}

// A Go template in value position ({{ ... }}) is not a plain YAML scalar and must
// be preserved verbatim: the normal pipeline renders value files through
// text/template BEFORE YAML parsing, so encrypting the directive would store the
// literal template text as the secret and defeat templating. An ordinary secret
// after it must still be encrypted.
func TestEncryptPreservesValueTemplate(t *testing.T) {
	t.Setenv(app2kube.EnvPassword, "pass")

	dir := t.TempDir()
	file := filepath.Join(dir, "secrets.yaml")
	content := "name: example\nsecrets:\n  api: {{ env \"API\" }}\n  token: plain\nother: keep\n"
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

	if !strings.Contains(s, "  api: {{ env \"API\" }}\n") {
		t.Errorf("value template not preserved verbatim:\n%s", s)
	}
	if !strings.Contains(s, "  token: AES#") {
		t.Errorf("ordinary secret after template not encrypted:\n%s", s)
	}
}

func TestEncryptSkipsEntireBlockScalarSecret(t *testing.T) {
	t.Setenv(app2kube.EnvPassword, "pass")

	dir := t.TempDir()
	file := filepath.Join(dir, "secrets.yaml")
	content := "name: example\nsecrets:\n  cert: |\n    subject: example\n    body: keep-this\n  token: plain\nother: keep\n"
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

	if !strings.Contains(s, "  cert: |\n    subject: example\n    body: keep-this\n") {
		t.Errorf("block scalar secret body must be preserved verbatim:\n%s", s)
	}
	if !strings.Contains(s, "  token: AES#") {
		t.Errorf("ordinary secret after block scalar must still be encrypted:\n%s", s)
	}
	if strings.Contains(s, "subject: AES#") || strings.Contains(s, "body: AES#") {
		t.Errorf("block scalar body lines must not be encrypted as separate secrets:\n%s", s)
	}
	if !strings.Contains(s, "other: keep") {
		t.Errorf("non-secret content not preserved:\n%s", s)
	}
}
