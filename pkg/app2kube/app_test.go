package app2kube

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadValues(t *testing.T) {
	cases := []struct {
		name       string
		files      ValueFiles
		values     []string
		stringVals []string
		fileVals   []string
		wantImage  string
	}{
		{
			name:      "FileOnly",
			files:     ValueFiles{"testdata/base.yaml"},
			wantImage: "example/app:v1",
		},
		{
			name:      "MultiFile",
			files:     ValueFiles{"testdata/base.yaml", "testdata/override.yaml"},
			wantImage: "example/app:v2",
		},
		{
			name:      "SetValue",
			files:     ValueFiles{"testdata/base.yaml"},
			values:    []string{"deployment.containers.app.image=example/app:v3"},
			wantImage: "example/app:v3",
		},
		{
			name:      "SetFile",
			files:     ValueFiles{"testdata/base.yaml"},
			fileVals:  []string{"deployment.containers.app.image=testdata/value.txt"},
			wantImage: "example/app:v4",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := NewApp()
			_, err := app.LoadValues(tc.files, tc.values, tc.stringVals, tc.fileVals)
			if err != nil {
				t.Fatalf("LoadValues returned error: %v", err)
			}
			img := app.Deployment.Containers["app"].Image
			if img != tc.wantImage {
				t.Errorf("expected image %s, got %s", tc.wantImage, img)
			}
			if app.Name != "example" {
				t.Errorf("expected name example, got %s", app.Name)
			}
		})
	}
}

// Regression (#11/#43): an explicit `labels: null` or a bare `labels:` in a
// values file makes ghodss/yaml replace app.Labels with a nil map. LoadValues
// then wrote app.Labels["app.kubernetes.io/name"] into that nil map and
// panicked ("assignment to entry in nil map") on untrusted input. ensureLabels
// must re-seed a writable map carrying the default instance label.
func TestLoadValuesNilLabels(t *testing.T) {
	cases := map[string]string{
		"explicit null": "name: web\nlabels: null\n",
		"bare key":      "name: web\nlabels:\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "values.yaml")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatalf("write values file: %v", err)
			}
			app := NewApp()
			if _, err := app.LoadValues(ValueFiles{path}, nil, nil, nil); err != nil {
				t.Fatalf("LoadValues returned error: %v", err)
			}
			if app.Labels == nil {
				t.Fatal("app.Labels is nil after LoadValues")
			}
			if got := app.Labels["app.kubernetes.io/instance"]; got != "production" {
				t.Errorf("instance label: expected production, got %q", got)
			}
			if app.Name != "web" {
				t.Errorf("name: expected web, got %q", app.Name)
			}
		})
	}
}

// Regression (#23): the managed-by label must be applied by the library, not
// only by the CLI layer. A programmatic consumer building manifests through
// NewApp/LoadValues must get app.kubernetes.io/managed-by=app2kube so the
// prune/delete selector (which relies on managed-by) can select the resources.
func TestManagedByLabelSetByLibrary(t *testing.T) {
	// Seeded directly by NewApp.
	if got := NewApp().Labels[LabelManagedBy]; got != ManagedByValue {
		t.Errorf("NewApp managed-by label: got %q, want %q", got, ManagedByValue)
	}

	// Re-seeded by ensureLabels even when the user wipes labels with `labels:
	// null` (which unmarshals to a nil map).
	path := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(path, []byte("name: web\nlabels: null\n"), 0600); err != nil {
		t.Fatalf("write values file: %v", err)
	}
	app := NewApp()
	if _, err := app.LoadValues(ValueFiles{path}, nil, nil, nil); err != nil {
		t.Fatalf("LoadValues returned error: %v", err)
	}
	if got := app.Labels[LabelManagedBy]; got != ManagedByValue {
		t.Errorf("managed-by after labels:null: got %q, want %q", got, ManagedByValue)
	}
}

// #44: an omitted image.tag keeps the "latest" default, and an explicit empty
// tag must NOT clobber it to "" (which would yield a malformed "repo:" image
// reference). An explicit non-empty tag is preserved.
func TestLoadValuesImageTagDefault(t *testing.T) {
	cases := map[string]struct {
		yaml string
		want string
	}{
		"omitted":        {"name: web\n", "latest"},
		"explicit empty": {"name: web\ncommon:\n  image:\n    tag: \"\"\n", "latest"},
		"explicit value": {"name: web\ncommon:\n  image:\n    tag: v2\n", "v2"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "values.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0600); err != nil {
				t.Fatalf("write values file: %v", err)
			}
			app := NewApp()
			if _, err := app.LoadValues(ValueFiles{path}, nil, nil, nil); err != nil {
				t.Fatalf("LoadValues: %v", err)
			}
			if app.Common.Image.Tag != tc.want {
				t.Errorf("image tag: got %q, want %q", app.Common.Image.Tag, tc.want)
			}
		})
	}
}

func TestEncryptAndDecryptAES(t *testing.T) {
	t.Setenv(EnvPassword, "pass")

	app := NewApp()
	enc, err := app.EncryptSecret("secret")
	if err != nil {
		t.Fatalf("EncryptSecret error: %v", err)
	}
	if !strings.HasPrefix(enc, "AES#") {
		t.Fatalf("expected AES prefix, got %s", enc)
	}

	app.Secrets = map[string]string{"pwd": enc}
	dec, err := app.GetDecryptedSecrets()
	if err != nil {
		t.Fatalf("GetDecryptedSecrets error: %v", err)
	}
	if dec["pwd"] != "secret" {
		t.Errorf("expected decrypted secret 'secret', got %s", dec["pwd"])
	}
}

func TestEncryptAndDecryptRSA(t *testing.T) {
	pub, priv, err := GenerateRSAKeys(2048)
	if err != nil {
		t.Fatalf("GenerateRSAKeys error: %v", err)
	}
	t.Setenv(EnvEncryptKey, pub)
	t.Setenv(EnvDecryptKey, priv)

	app := NewApp()
	enc, err := app.EncryptSecret("secret")
	if err != nil {
		t.Fatalf("EncryptSecret error: %v", err)
	}
	if !strings.HasPrefix(enc, "RSA#") {
		t.Fatalf("expected RSA prefix, got %s", enc)
	}

	app.Secrets = map[string]string{"pwd": enc}
	dec, err := app.GetDecryptedSecrets()
	if err != nil {
		t.Fatalf("GetDecryptedSecrets error: %v", err)
	}
	if dec["pwd"] != "secret" {
		t.Errorf("expected decrypted secret 'secret', got %s", dec["pwd"])
	}
}
