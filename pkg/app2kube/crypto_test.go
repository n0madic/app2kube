package app2kube

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"math/big"
	"strings"
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

// Regression: decrypting a secret with the wrong AES password must produce an
// actionable error that names the secret and points at $APP2KUBE_PASSWORD,
// instead of the cryptic underlying "invalid padding".
func TestGetDecryptedSecretsWrongPassword(t *testing.T) {
	enc, err := EncryptAES("correct-password", "topsecret")
	if err != nil {
		t.Fatalf("EncryptAES: %v", err)
	}

	app := NewApp()
	app.aesPassword = "wrong-password"
	app.Secrets = map[string]string{"db": aesPrefix + enc}

	_, err = app.GetDecryptedSecrets()
	if err == nil {
		t.Fatalf("expected error when decrypting with the wrong password")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"db"`) {
		t.Errorf("error must name the failing secret, got: %v", msg)
	}
	if !strings.Contains(msg, "APP2KUBE_PASSWORD") {
		t.Errorf("error must hint at $APP2KUBE_PASSWORD, got: %v", msg)
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

// #36: decrypting an RSA secret with a valid-but-wrong private key must produce
// an actionable error naming the secret and $APP2KUBE_DECRYPT_KEY, not a panic
// or a cryptic low-level error.
func TestGetDecryptedSecretsWrongRSAKey(t *testing.T) {
	pub, _, err := GenerateRSAKeys(2048)
	if err != nil {
		t.Fatalf("GenerateRSAKeys: %v", err)
	}
	_, wrongPriv, err := GenerateRSAKeys(2048)
	if err != nil {
		t.Fatalf("GenerateRSAKeys: %v", err)
	}
	blob, err := EncryptRSA(pub, "topsecret")
	if err != nil {
		t.Fatalf("EncryptRSA: %v", err)
	}

	app := NewApp()
	app.rsaPrivateKey = wrongPriv
	app.Secrets = map[string]string{"token": rsaPrefix + blob}

	_, err = app.GetDecryptedSecrets()
	if err == nil {
		t.Fatalf("expected error decrypting with the wrong RSA private key")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"token"`) {
		t.Errorf("error must name the failing secret, got: %v", msg)
	}
	if !strings.Contains(msg, "APP2KUBE_DECRYPT_KEY") {
		t.Errorf("error must hint at $APP2KUBE_DECRYPT_KEY, got: %v", msg)
	}
}

// #36: the deprecated CRYPT# prefix (6 chars) must still be stripped correctly
// and decrypt — an off-by-one in the prefix length would corrupt legacy data.
func TestGetDecryptedSecretsCryptPrefix(t *testing.T) {
	blob, err := EncryptAES("pass", "legacy-value")
	if err != nil {
		t.Fatalf("EncryptAES: %v", err)
	}

	app := NewApp()
	app.aesPassword = "pass"
	app.Secrets = map[string]string{"pwd": cryptPrefix + blob}

	dec, err := app.GetDecryptedSecrets()
	if err != nil {
		t.Fatalf("GetDecryptedSecrets (CRYPT#): %v", err)
	}
	if dec["pwd"] != "legacy-value" {
		t.Errorf("CRYPT# secret must decrypt to original, got %q", dec["pwd"])
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

// New-format (AES-GCM) round-trip across several plaintext lengths, including a
// length whose resulting blob is a multiple of the AES block size (plaintext
// len ≡ 4 mod 16 → 12-byte nonce + len + 16-byte tag ≡ 0 mod 16). This guards
// against the GCM/CBC dispatch in DecryptAES mishandling block-aligned blobs.
func TestEncryptDecryptAESRoundTrip(t *testing.T) {
	lengths := []int{1, 4, 15, 16, 17, 20, 36, 100}
	for _, n := range lengths {
		plaintext := strings.Repeat("a", n)
		enc, err := EncryptAES("round-trip-password", plaintext)
		if err != nil {
			t.Fatalf("EncryptAES(len=%d): %v", n, err)
		}
		dec, err := DecryptAES("round-trip-password", enc)
		if err != nil {
			t.Fatalf("DecryptAES(len=%d): %v", n, err)
		}
		if dec != plaintext {
			t.Errorf("round-trip(len=%d): got %q, want %q", n, dec, plaintext)
		}
	}
}

// Backward-compatibility lock: a pre-existing AES-256-CBC blob (generated by the
// legacy algorithm before the AES-GCM migration) must still decrypt via the CBC
// fallback. The constant below was produced once with the old EncryptAES.
func TestDecryptAESLegacyCBC(t *testing.T) {
	const legacyBlob = "OT0hDAnu+ALx0ynQdtqOrD8Ho9GqRDnbj14DcYzedx2hirEdsMyh81nF7wc78W/M"
	dec, err := DecryptAES("legacy-password", legacyBlob)
	if err != nil {
		t.Fatalf("DecryptAES(legacy CBC): %v", err)
	}
	if dec != "legacy-secret-value" {
		t.Errorf("legacy CBC: got %q, want %q", dec, "legacy-secret-value")
	}
}

// Tampering with an AES-GCM blob (flipping a ciphertext byte) must be detected
// and rejected — this is the malleability fix the CBC scheme lacked.
func TestDecryptAESTamperDetected(t *testing.T) {
	enc, err := EncryptAES("tamper-password", "authenticated-data")
	if err != nil {
		t.Fatalf("EncryptAES: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	// Flip a bit in the ciphertext region (just past the 12-byte nonce).
	raw[12] ^= 0x01
	tampered := base64.StdEncoding.EncodeToString(raw)
	if _, err := DecryptAES("tamper-password", tampered); err == nil {
		t.Fatalf("expected tampered GCM ciphertext to be rejected")
	}
}

// All decrypt failures must collapse to the single generic errDecrypt sentinel,
// so the error message cannot be used as a padding/stage oracle.
func TestDecryptAESGenericError(t *testing.T) {
	enc, err := EncryptAES("oracle-password", "some-secret")
	if err != nil {
		t.Fatalf("EncryptAES: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	tampered := make([]byte, len(raw))
	copy(tampered, raw)
	tampered[12] ^= 0x01

	cases := map[string]string{
		"invalid base64":   "not-valid-base64-!!!",
		"too short":        base64.StdEncoding.EncodeToString([]byte("short")),
		"tampered GCM":     base64.StdEncoding.EncodeToString(tampered),
		"garbage of zeros": base64.StdEncoding.EncodeToString(make([]byte, 48)),
	}
	for name, blob := range cases {
		_, err := DecryptAES("oracle-password", blob)
		if err == nil {
			t.Errorf("%s: expected error", name)
			continue
		}
		if !errors.Is(err, errDecrypt) {
			t.Errorf("%s: got %v, want errDecrypt", name, err)
		}
	}
}

// A valid PKIX public key that is not RSA (e.g. ECDSA) must return an error
// rather than panicking on the type assertion.
func TestEncryptRSANonRSAKey(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	key := base64.StdEncoding.EncodeToString(der)
	if _, err := EncryptRSA(key, "x"); err == nil {
		t.Errorf("expected error for non-RSA PKIX key, got nil")
	}
}

// A 512-bit RSA key is too small for OAEP-SHA256 (step = size - 2*hash - 2 <= 0)
// and must be rejected with an error instead of hanging/panicking in the chunk
// loop. The key is constructed synthetically because crypto/rsa refuses to
// generate keys this small.
func TestEncryptRSAKeyTooSmall(t *testing.T) {
	n := new(big.Int).Lsh(big.NewInt(1), 511) // 512-bit modulus
	n.SetBit(n, 0, 1)                         // make it odd
	pub := &rsa.PublicKey{N: n, E: 65537}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	key := base64.StdEncoding.EncodeToString(der)
	if _, err := EncryptRSA(key, "x"); err == nil {
		t.Errorf("expected error for too-small RSA key, got nil")
	}
}

// Multi-chunk RSA round-trip across lengths spanning the 190-byte OAEP-SHA256
// chunk boundary, locking the encrypt/decrypt chunk math.
func TestEncryptDecryptRSARoundTripLengths(t *testing.T) {
	pub, priv, err := GenerateRSAKeys(2048)
	if err != nil {
		t.Fatalf("GenerateRSAKeys: %v", err)
	}
	for _, n := range []int{0, 1, 189, 190, 191, 380, 500} {
		plaintext := strings.Repeat("x", n)
		enc, err := EncryptRSA(pub, plaintext)
		if err != nil {
			t.Fatalf("EncryptRSA(len=%d): %v", n, err)
		}
		dec, err := DecryptRSA(priv, enc)
		if err != nil {
			t.Fatalf("DecryptRSA(len=%d): %v", n, err)
		}
		if dec != plaintext {
			t.Errorf("RSA round-trip(len=%d): got %d bytes, want %d", n, len(dec), n)
		}
	}
}
