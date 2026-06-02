package app2kube

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// errDecrypt is the single, generic error returned for every AES decryption
// failure. Collapsing all failure modes (bad base64, wrong length, failed GCM
// authentication, bad CBC padding) into one indistinguishable error removes the
// padding/stage oracle the previous distinct messages exposed.
var errDecrypt = errors.New("decryption failed")

const (
	// aesPrefix for values encrypted with AES-256-GCM (legacy AES-256-CBC blobs
	// under the same prefix are still decryptable via the CBC fallback)
	aesPrefix = "AES#"
	// Deprecated: cryptPrefix for legacy values encrypted with AES-256-CBC
	cryptPrefix = "CRYPT#"
	// rsaPrefix for encrypted values by RSA
	rsaPrefix = "RSA#"
)

// deriveKey turns a password into a 32-byte AES-256 key by copying it into a
// zero-filled buffer. This derivation is intentionally kept (no KDF/salt) so
// that legacy AES#/CRYPT# blobs encrypted before the AES-GCM migration still
// decrypt under the same key. It is shared by the GCM path and the CBC fallback.
func deriveKey(password string) []byte {
	key := make([]byte, 32)
	copy(key, []byte(password))
	return key
}

// EncryptAES encrypts plaintext with AES-256-GCM (authenticated encryption) and
// returns a base64-encoded string of nonce || ciphertext || tag. The "AES#"
// prefix is added by EncryptSecret.
func EncryptAES(password string, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	block, err := aes.NewCipher(deriveKey(password))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	// Seal appends the ciphertext+tag to nonce, yielding nonce || ct || tag.
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptAES decrypts a base64-encoded blob produced by EncryptAES. It first
// tries the current AES-256-GCM format; if that does not apply or fails it falls
// back to the legacy AES-256-CBC format for backward compatibility. Every
// failure mode returns the single generic errDecrypt to avoid a padding/stage
// oracle.
//
// Backward-compat caveat: the CBC fallback still ACCEPTS unauthenticated blobs,
// so pre-existing AES#/CRYPT# secrets remain malleable until they are
// re-encrypted. Full closure requires dropping the CBC fallback in a future
// major version.
func DecryptAES(password string, crypt64 string) (string, error) {
	if crypt64 == "" {
		return "", nil
	}

	key := deriveKey(password)

	raw, err := base64.StdEncoding.DecodeString(crypt64)
	if err != nil {
		return "", errDecrypt
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", errDecrypt
	}

	// Current format: AES-256-GCM (nonce || ciphertext || tag).
	if gcm, err := cipher.NewGCM(block); err == nil {
		if len(raw) >= gcm.NonceSize()+gcm.Overhead() {
			nonce := raw[:gcm.NonceSize()]
			if plaintext, err := gcm.Open(nil, nonce, raw[gcm.NonceSize():], nil); err == nil {
				return string(plaintext), nil
			}
		}
	}

	// Legacy format: AES-256-CBC. Only attempt when the blob matches the old
	// invariant (IV plus at least one block, block-aligned). This guard also
	// makes wrong-password GCM blobs whose length is not block-aligned skip CBC
	// and fail reliably instead of producing garbage.
	blockSize := block.BlockSize()
	if len(raw) >= 2*blockSize && len(raw)%blockSize == 0 {
		if plaintext, ok := decryptLegacyCBC(block, raw); ok {
			return plaintext, nil
		}
	}

	return "", errDecrypt
}

// decryptLegacyCBC decrypts a legacy AES-256-CBC blob (IV || ciphertext) and
// strips PKCS#7 padding, returning ok=false on any malformed input. The caller
// must have already validated the length/alignment invariant.
func decryptLegacyCBC(block cipher.Block, raw []byte) (string, bool) {
	blockSize := block.BlockSize()
	iv := raw[:blockSize]
	data := raw[blockSize:]
	decrypted := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(decrypted, data)

	padding := int(decrypted[len(decrypted)-1])
	if padding == 0 || padding > blockSize || padding > len(decrypted) {
		return "", false
	}
	for _, b := range decrypted[len(decrypted)-padding:] {
		if int(b) != padding {
			return "", false
		}
	}
	return string(decrypted[:len(decrypted)-padding]), true
}

// GenerateRSAKeys returns a base64 encoded public and private keys
func GenerateRSAKeys(bits int) (string, string, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return "", "", err
	}

	pubASN1, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", "", err
	}
	pubKeyStr := base64.StdEncoding.EncodeToString(pubASN1)

	privASN1 := x509.MarshalPKCS1PrivateKey(privateKey)
	privKeyStr := base64.StdEncoding.EncodeToString(privASN1)

	return pubKeyStr, privKeyStr, nil
}

// EncryptRSA text with RSA public key and returns a base64 encoded string
func EncryptRSA(publicKey string, plaintext string) (string, error) {
	pubASN1, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil {
		return "", err
	}
	pubKey, err := x509.ParsePKIXPublicKey(pubASN1)
	if err != nil {
		return "", err
	}

	public, ok := pubKey.(*rsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("public key is not an RSA key")
	}

	hash := sha256.New()
	step := public.Size() - 2*hash.Size() - 2
	if step <= 0 {
		return "", fmt.Errorf("RSA key too small for OAEP-SHA256")
	}

	msg := []byte(plaintext)
	msgLen := len(msg)
	var encryptedBytes []byte
	for start := 0; start < msgLen; start += step {
		finish := start + step
		if finish > msgLen {
			finish = msgLen
		}
		encryptedBlockBytes, err := rsa.EncryptOAEP(hash, rand.Reader, public, msg[start:finish], nil)
		if err != nil {
			return "", err
		}
		encryptedBytes = append(encryptedBytes, encryptedBlockBytes...)
	}

	return base64.StdEncoding.EncodeToString(encryptedBytes), nil
}

// DecryptRSA base64 string encoded by the RSA private key
func DecryptRSA(privateKey string, crypt64 string) (string, error) {
	privASN1, err := base64.StdEncoding.DecodeString(privateKey)
	if err != nil {
		return "", err
	}
	privKey, err := x509.ParsePKCS1PrivateKey(privASN1)
	if err != nil {
		return "", err
	}

	crypt, err := base64.StdEncoding.DecodeString(crypt64)
	if err != nil {
		return "", err
	}

	step := privKey.Size()
	msgLen := len(crypt)
	if step <= 0 || msgLen%step != 0 {
		return "", fmt.Errorf("invalid RSA ciphertext length")
	}

	hash := sha256.New()
	var decryptedBytes []byte
	for start := 0; start < msgLen; start += step {
		finish := start + step
		if finish > msgLen {
			finish = msgLen
		}
		decryptedBlockBytes, err := rsa.DecryptOAEP(hash, rand.Reader, privKey, crypt[start:finish], nil)
		if err != nil {
			return "", err
		}
		decryptedBytes = append(decryptedBytes, decryptedBlockBytes...)
	}

	return string(decryptedBytes), nil
}

// IsEncrypted returns true if the value is encrypted
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, aesPrefix) || strings.HasPrefix(value, cryptPrefix) || strings.HasPrefix(value, rsaPrefix)
}

// EncryptSecret encrypts plaintext secret by AES or RSA.
// It returns a string with a prefix "AES#" or "RSA#"
// and a base64 encoded encrypted secret.
// RSA has priority over AES if both keys are specified.
func (app *App) EncryptSecret(plaintext string) (string, error) {
	if app.rsaPublicKey != "" {
		secret, err := EncryptRSA(app.rsaPublicKey, plaintext)
		if err != nil {
			return "", err
		}
		return rsaPrefix + secret, nil
	}
	if app.aesPassword != "" {
		secret, err := EncryptAES(app.aesPassword, plaintext)
		if err != nil {
			return "", err
		}
		return aesPrefix + secret, nil
	}
	return "", fmt.Errorf("AES password or RSA public key not specified")
}

// GetDecryptedSecrets return decrypted secrets of App
func (app *App) GetDecryptedSecrets() (secrets map[string]string, err error) {
	secrets = make(map[string]string)
	for key, value := range app.Secrets {
		if strings.HasPrefix(value, aesPrefix) || strings.HasPrefix(value, cryptPrefix) {
			if app.aesPassword == "" {
				return nil, fmt.Errorf("AES password not specified in $%s", EnvPassword)
			}
			if strings.HasPrefix(value, aesPrefix) {
				value = value[len(aesPrefix):]
			} else {
				value = value[len(cryptPrefix):]
			}
			decrypted, err := DecryptAES(app.aesPassword, value)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt secret %q, check $%s is correct: %w", key, EnvPassword, err)
			}
			secrets[key] = decrypted
		} else if strings.HasPrefix(value, rsaPrefix) {
			if app.rsaPrivateKey == "" {
				return nil, fmt.Errorf("RSA private key not specified in $%s", EnvDecryptKey)
			}
			value = value[len(rsaPrefix):]
			decrypted, err := DecryptRSA(app.rsaPrivateKey, value)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt secret %q, check $%s is correct: %w", key, EnvDecryptKey, err)
			}
			secrets[key] = decrypted
		} else {
			secrets[key] = value
		}
	}
	return secrets, nil
}
