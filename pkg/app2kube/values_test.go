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
	dest := map[string]interface{}{
		"a": "1",
		"nested": map[string]interface{}{
			"x": "old",
			"y": "keep",
		},
	}
	src := map[string]interface{}{
		"b": "2",
		"nested": map[string]interface{}{
			"x": "new",
		},
	}
	out := mergeValues(dest, src)
	if out["a"] != "1" || out["b"] != "2" {
		t.Errorf("top-level merge: %+v", out)
	}
	nested := out["nested"].(map[string]interface{})
	if nested["x"] != "new" {
		t.Errorf("source must override: x=%v", nested["x"])
	}
	if nested["y"] != "keep" {
		t.Errorf("untouched key must remain: y=%v", nested["y"])
	}
}

func TestMergeValuesOverwriteNonMap(t *testing.T) {
	// When source value is not a map, it overwrites the destination map.
	dest := map[string]interface{}{"k": map[string]interface{}{"a": "1"}}
	src := map[string]interface{}{"k": "scalar"}
	out := mergeValues(dest, src)
	if out["k"] != "scalar" {
		t.Errorf("non-map source must overwrite: got %v", out["k"])
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

func TestReadFileHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("name: remote\n"))
	}))
	defer srv.Close()

	b, err := readFile(srv.URL)
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if string(b) != "name: remote\n" {
		t.Errorf("got %q", string(b))
	}
}

func TestReadFileHTTPNotFoundError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := readFile(srv.URL); err == nil {
		t.Errorf("expected error for 404 without '?' suffix")
	}
	// With the '?' suffix a missing remote file is tolerated.
	if _, err := readFile(srv.URL + "?"); err != nil {
		t.Errorf("'?' suffix must tolerate missing file: %v", err)
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
