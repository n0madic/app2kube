package app2kube

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestValueFilesFlag(t *testing.T) {
	var v ValueFiles
	if err := v.Set("a.yaml,b.yaml"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if len(v) != 2 || v[0] != "a.yaml" || v[1] != "b.yaml" {
		t.Errorf("Set split: got %+v", v)
	}
	if v.Type() != "valueFiles" {
		t.Errorf("Type: got %q", v.Type())
	}
	if v.String() == "" {
		t.Errorf("String must not be empty")
	}
}

func TestMergeValues(t *testing.T) {
	dest := map[string]any{
		"a": "1",
		"nested": map[string]any{
			"x": "old",
			"y": "keep",
		},
	}
	src := map[string]any{
		"b": "2",
		"nested": map[string]any{
			"x": "new",
		},
	}
	out := mergeValues(dest, src)
	if out["a"] != "1" || out["b"] != "2" {
		t.Errorf("top-level merge: %+v", out)
	}
	nested := out["nested"].(map[string]any)
	if nested["x"] != "new" {
		t.Errorf("source must override: x=%v", nested["x"])
	}
	if nested["y"] != "keep" {
		t.Errorf("untouched key must remain: y=%v", nested["y"])
	}
}

func TestMergeValuesOverwriteNonMap(t *testing.T) {
	// When source value is not a map, it overwrites the destination map.
	dest := map[string]any{"k": map[string]any{"a": "1"}}
	src := map[string]any{"k": "scalar"}
	out := mergeValues(dest, src)
	if out["k"] != "scalar" {
		t.Errorf("non-map source must overwrite: got %v", out["k"])
	}
}

func TestMergeValuesNilDoesNotClobber(t *testing.T) {
	// #13: a bare/null key in a later file (e.g. `common:` with no value) must
	// not wipe an already-populated subtree from an earlier file.
	dest := map[string]any{
		"common": map[string]any{"image": "repo", "resources": "set"},
	}
	src := map[string]any{"common": nil}
	out := mergeValues(dest, src)
	nested, ok := out["common"].(map[string]any)
	if !ok {
		t.Fatalf("nil source clobbered populated subtree: common=%v", out["common"])
	}
	if nested["image"] != "repo" || nested["resources"] != "set" {
		t.Errorf("subtree contents lost: %+v", nested)
	}
}

func TestMergeValuesNilSetsNewKey(t *testing.T) {
	// A nil source value for a key absent from dest still sets it (no subtree to
	// protect), preserving the "set the key to that value" behavior.
	dest := map[string]any{"a": "1"}
	src := map[string]any{"b": nil}
	out := mergeValues(dest, src)
	if v, ok := out["b"]; !ok || v != nil {
		t.Errorf("new nil key not set: got %v (present=%v)", v, ok)
	}
}

func TestValsTemplating(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "v.yaml")
	// sprig template functions must be applied to file content.
	content := `name: {{ "myapp" | upper }}`
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := vals(ValueFiles{f}, nil, nil, nil)
	if err != nil {
		t.Fatalf("vals: %v", err)
	}
	if string(out) != "name: MYAPP\n" {
		t.Errorf("templating: got %q", string(out))
	}
}

func TestValsSetOverridesFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "v.yaml")
	if err := os.WriteFile(f, []byte("name: fromfile\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := vals(ValueFiles{f}, []string{"name=fromset"}, nil, nil)
	if err != nil {
		t.Fatalf("vals: %v", err)
	}
	if string(out) != "name: fromset\n" {
		t.Errorf("--set must override file: got %q", string(out))
	}
}

func TestValsSetFile(t *testing.T) {
	dir := t.TempDir()
	valFile := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(valFile, []byte("  topsecret  \n"), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := vals(nil, nil, nil, []string{"password=" + valFile})
	if err != nil {
		t.Fatalf("vals: %v", err)
	}
	// --set-file trims surrounding whitespace.
	if string(out) != "password: topsecret\n" {
		t.Errorf("set-file: got %q", string(out))
	}
}

// #36/#39: --set-file pointing at a missing file must return an error and no
// value — the reader does not derive a value on the read-error path, so a
// failed read can never surface as a silently-empty key.
func TestValsSetFileMissingError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.txt")
	out, err := vals(nil, nil, nil, []string{"password=" + missing})
	if err == nil {
		t.Errorf("expected error for --set-file pointing at a missing file")
	}
	if len(out) != 0 {
		t.Errorf("no value must be returned on a read error, got %q", string(out))
	}
}

// #36: --set-string keeps a numeric-looking value typed as a string (quoted),
// unlike --set which would render it as a bare integer.
func TestValsSetStringKeepsNumericAsString(t *testing.T) {
	out, err := vals(nil, nil, []string{"port=8080"}, nil)
	if err != nil {
		t.Fatalf("vals: %v", err)
	}
	if string(out) != "port: \"8080\"\n" {
		t.Errorf("--set-string must keep a numeric value as a string, got %q", string(out))
	}
}

// readFile must NOT perform any network fetch. An http:// argument is treated as
// a local filesystem path (which does not exist), so it errors without hitting
// the server, and the '?' suffix tolerates the missing "file".
func TestReadFileHTTPNotFetched(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = w.Write([]byte("name: remote\n"))
	}))
	defer srv.Close()

	if _, err := readFile(srv.URL); err == nil {
		t.Errorf("expected error: http URL must be treated as a local path, not fetched")
	}
	// With '?' the missing "file" is tolerated and yields empty content.
	b, err := readFile(srv.URL + "?")
	if err != nil {
		t.Errorf("'?' suffix must tolerate the missing path: %v", err)
	}
	if len(b) != 0 {
		t.Errorf("expected empty content, got %q", string(b))
	}
	if hit {
		t.Errorf("readFile must not make a network request")
	}
}

func TestReadFileMissingLocalAllowed(t *testing.T) {
	// Local missing file without '?' is an error.
	if _, err := readFile(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Errorf("expected error for missing local file")
	}
	// With '?' it is tolerated and returns empty content.
	b, err := readFile(filepath.Join(t.TempDir(), "nope.yaml?"))
	if err != nil {
		t.Errorf("'?' suffix must tolerate missing local file: %v", err)
	}
	if len(b) != 0 {
		t.Errorf("expected empty content, got %q", string(b))
	}
}

func TestTemplatingInvalid(t *testing.T) {
	if _, err := templating([]byte("{{ .Broken ")); err == nil {
		t.Errorf("expected parse error for malformed template")
	}
}

// TestTemplatingNoHTMLEscape locks the fix for values templating using
// text/template instead of html/template. HTML escaping would corrupt YAML by
// turning special characters in template output into entities
// (e.g. & -> &amp;, < -> &lt;), which is invalid for config values.
func TestTemplatingNoHTMLEscape(t *testing.T) {
	out, err := templating([]byte(`value: {{ "a&b<c>d" }}`))
	if err != nil {
		t.Fatalf("templating: %v", err)
	}
	if string(out) != `value: a&b<c>d` {
		t.Errorf("template output must not be HTML-escaped: got %q", string(out))
	}
}
