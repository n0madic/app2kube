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

func TestEncryptAndDecryptAES(t *testing.T) {
	os.Setenv("APP2KUBE_PASSWORD", "pass")
	defer os.Unsetenv("APP2KUBE_PASSWORD")

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
	os.Setenv("APP2KUBE_ENCRYPT_KEY", pub)
	os.Setenv("APP2KUBE_DECRYPT_KEY", priv)
	defer os.Unsetenv("APP2KUBE_ENCRYPT_KEY")
	defer os.Unsetenv("APP2KUBE_DECRYPT_KEY")

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
