package app2kube

import (
	"os"
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
