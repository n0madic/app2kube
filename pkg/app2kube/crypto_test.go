package app2kube

import (
	"encoding/base64"
	"testing"
)

func TestIsEncrypted(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"AES#abc", true},
		{"CRYPT#abc", true}, // deprecated prefix still recognized
		{"RSA#abc", true},
		{"plain", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsEncrypted(tc.value); got != tc.want {
			t.Errorf("IsEncrypted(%q): got %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestEncryptSecretNoKeys(t *testing.T) {
	app := NewApp()
	app.aesPassword = ""
	app.rsaPublicKey = ""
	if _, err := app.EncryptSecret("x"); err == nil {
		t.Errorf("expected error when no AES password or RSA key configured")
	}
}

func TestEncryptSecretRSAPriority(t *testing.T) {
	pub, _, err := GenerateRSAKeys(2048)
	if err != nil {
		t.Fatalf("GenerateRSAKeys: %v", err)
	}
	app := NewApp()
	app.aesPassword = "pass"
	app.rsaPublicKey = pub
	enc, err := app.EncryptSecret("secret")
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	// RSA takes priority when both are set.
	if got := enc[:4]; got != "RSA#" {
		t.Errorf("expected RSA# prefix, got %q", got)
	}
}

func TestGetDecryptedSecretsNoAESPassword(t *testing.T) {
	app := NewApp()
	app.aesPassword = ""
	app.Secrets = map[string]string{"k": "AES#whatever"}
	if _, err := app.GetDecryptedSecrets(); err == nil {
		t.Errorf("expected error: AES password not specified")
	}
}

func TestGetDecryptedSecretsNoRSAKey(t *testing.T) {
	app := NewApp()
	app.rsaPrivateKey = ""
	app.Secrets = map[string]string{"k": "RSA#whatever"}
	if _, err := app.GetDecryptedSecrets(); err == nil {
		t.Errorf("expected error: RSA private key not specified")
	}
}

func TestGetDecryptedSecretsPlaintextPassthrough(t *testing.T) {
	app := NewApp()
	app.Secrets = map[string]string{"k": "plainvalue"}
	dec, err := app.GetDecryptedSecrets()
	if err != nil {
		t.Fatalf("GetDecryptedSecrets: %v", err)
	}
	if dec["k"] != "plainvalue" {
		t.Errorf("plaintext should pass through unchanged, got %q", dec["k"])
	}
}

func TestDecryptRSAMalformed(t *testing.T) {
	_, priv, err := GenerateRSAKeys(2048)
	if err != nil {
		t.Fatalf("GenerateRSAKeys: %v", err)
	}
	cases := []string{
		"not-base64-!!!",
		base64.StdEncoding.EncodeToString([]byte("garbage")),
	}
	for _, c := range cases {
		if _, err := DecryptRSA(priv, c); err == nil {
			t.Errorf("expected error for malformed RSA ciphertext %q", c)
		}
	}
	// A malformed private key must also error rather than panic.
	if _, err := DecryptRSA("not-a-key", "AAAA"); err == nil {
		t.Errorf("expected error for malformed private key")
	}
}

func TestEncryptRSAMalformedKey(t *testing.T) {
	if _, err := EncryptRSA("not-base64-!!!", "x"); err == nil {
		t.Errorf("expected error for malformed public key")
	}
}

func TestEncryptDecryptAESEmpty(t *testing.T) {
	// Empty plaintext round-trips to empty without error.
	enc, err := EncryptAES("pass", "")
	if err != nil {
		t.Fatalf("EncryptAES: %v", err)
	}
	if enc != "" {
		t.Errorf("expected empty ciphertext for empty plaintext, got %q", enc)
	}
	dec, err := DecryptAES("pass", "")
	if err != nil {
		t.Fatalf("DecryptAES: %v", err)
	}
	if dec != "" {
		t.Errorf("expected empty plaintext, got %q", dec)
	}
}

func TestGenerateRSAKeysDistinct(t *testing.T) {
	pub, priv, err := GenerateRSAKeys(2048)
	if err != nil {
		t.Fatalf("GenerateRSAKeys: %v", err)
	}
	if pub == "" || priv == "" {
		t.Errorf("keys must not be empty")
	}
	if pub == priv {
		t.Errorf("public and private keys must differ")
	}
	// Generated keys must be usable for a round-trip.
	enc, err := EncryptRSA(pub, "hello")
	if err != nil {
		t.Fatalf("EncryptRSA: %v", err)
	}
	dec, err := DecryptRSA(priv, enc)
	if err != nil {
		t.Fatalf("DecryptRSA: %v", err)
	}
	if dec != "hello" {
		t.Errorf("round-trip: got %q, want hello", dec)
	}
}
