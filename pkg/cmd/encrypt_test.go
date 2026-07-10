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

	err := runEncrypt("", false, app2kube.ValueFiles{filepath.Join(t.TempDir(), "does-not-exist.yaml")})
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

	if err := runEncrypt("", false, app2kube.ValueFiles{file}); err != nil {
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

	if err := runEncrypt("", false, app2kube.ValueFiles{file}); err != nil {
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

	if err := runEncrypt("", false, app2kube.ValueFiles{file}); err != nil {
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

	if err := runEncrypt("", false, app2kube.ValueFiles{file}); err != nil {
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

	if err := runEncrypt("", false, app2kube.ValueFiles{file}); err != nil {
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

	if err := runEncrypt("", false, app2kube.ValueFiles{file}); err != nil {
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

// Regression: the secrets file must be replaced atomically. An in-place
// os.WriteFile truncates before writing, so an interruption between the two
// (SIGINT, full disk, panic) left the file empty or half-written, destroying
// already-encrypted secrets. writeSecretsFile writes a sibling temp file and
// renames it over the target, and tightens the mode to 0600 even when the
// pre-existing file was world-readable.
func TestWriteSecretsFileAtomicAndOwnerOnly(t *testing.T) {
	t.Run("replaces content and tightens a loose mode", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "secrets.yaml")
		if err := os.WriteFile(path, []byte("old: value\n"), 0644); err != nil {
			t.Fatalf("seed: %v", err)
		}

		const want = "new: CRYPT#abc\n"
		if err := writeSecretsFile(path, want); err != nil {
			t.Fatalf("writeSecretsFile: %v", err)
		}

		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if string(got) != want {
			t.Errorf("content = %q, want %q", got, want)
		}

		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("mode = %04o, want 0600 (a secrets file must not stay group/world-readable)", perm)
		}
	})

	t.Run("leaves no temp file behind", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "secrets.yaml")
		if err := writeSecretsFile(path, "a: b\n"); err != nil {
			t.Fatalf("writeSecretsFile: %v", err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("readdir: %v", err)
		}
		if len(entries) != 1 || entries[0].Name() != "secrets.yaml" {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("directory = %v, want only secrets.yaml (temp file leaked)", names)
		}
	})

	t.Run("a failed write does not destroy the original", func(t *testing.T) {
		// An unwritable directory makes the temp-file creation fail. The original
		// must survive untouched — the whole point of not truncating in place.
		if os.Geteuid() == 0 {
			t.Skip("root bypasses directory permissions")
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "secrets.yaml")
		const orig = "keep: me\n"
		if err := os.WriteFile(path, []byte(orig), 0600); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if err := os.Chmod(dir, 0500); err != nil {
			t.Fatalf("chmod dir: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

		if err := writeSecretsFile(path, "clobbered\n"); err == nil {
			t.Fatal("expected an error when the directory is unwritable")
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if string(got) != orig {
			t.Errorf("original content was destroyed: got %q, want %q", got, orig)
		}
	})
}

// Regression: `config encrypt --string -` with empty stdin resolves the secret to
// "", which used to be indistinguishable from "--string was not passed". The
// command then fell into the files branch and reported "need to specify yaml
// files" — diagnosing the wrong problem. An empty secret has no encryption (
// EncryptSecret maps "" to ""), so it must be rejected on its own terms.
func TestRunEncryptEmptyStringIsDiagnosedCorrectly(t *testing.T) {
	t.Run("explicit empty --string reports the empty value", func(t *testing.T) {
		err := runEncrypt("", true, nil)
		if err == nil {
			t.Fatal("expected an error for an empty --string")
		}
		if !strings.Contains(err.Error(), "empty value") {
			t.Errorf("error = %q, want it to mention the empty value, not missing files", err)
		}
	})

	t.Run("no --string and no files still reports missing files", func(t *testing.T) {
		err := runEncrypt("", false, nil)
		if err == nil || !strings.Contains(err.Error(), "need to specify yaml files") {
			t.Errorf("error = %v, want the missing-files error", err)
		}
	})
}

// resolveEncryptString maps the "-" sentinel to stdin so the secret never lands
// in shell history; an empty stdin legitimately yields an empty string, which the
// caller (runEncrypt) is responsible for rejecting.
func TestResolveEncryptStringEmptyStdin(t *testing.T) {
	got, err := resolveEncryptString("-", strings.NewReader(""))
	if err != nil {
		t.Fatalf("resolveEncryptString: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
