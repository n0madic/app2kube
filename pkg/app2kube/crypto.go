package app2kube

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const (
	// aesPrefix for encrypted values by AES-256 CBC
	aesPrefix = "AES#"
	// Deprecated: cryptPrefix for encrypted values by AES-256 CBC
	cryptPrefix = "CRYPT#"
	// rsaPrefix for encrypted values by RSA
	rsaPrefix = "RSA#"
)

// EncryptAES text with AES-256 CBC and returns a base64 encoded string
func EncryptAES(password string, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	key := make([]byte, 32)
	copy(key, []byte(password))
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	content := []byte(plaintext)
	blockSize := block.BlockSize()
	padding := blockSize - len(content)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	content = append(content, padtext...)

	ciphertext := make([]byte, aes.BlockSize+len(content))

	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", err
	}

	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(ciphertext[aes.BlockSize:], content)

	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptAES base64 string encoded by the AES-256 CBC
func DecryptAES(password string, crypt64 string) (string, error) {
	if crypt64 == "" {
		return "", nil
	}

	key := make([]byte, 32)
	copy(key, []byte(password))

	crypt, err := base64.StdEncoding.DecodeString(crypt64)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	iv := crypt[:aes.BlockSize]
	crypt = crypt[aes.BlockSize:]
	decrypted := make([]byte, len(crypt))
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(decrypted, crypt)

	return string(decrypted[:len(decrypted)-int(decrypted[len(decrypted)-1])]), nil
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

	encryptedBytes, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pubKey.(*rsa.PublicKey), []byte(plaintext), nil)
	if err != nil {
		return "", err
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

	decryptedBytes, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, privKey, crypt, nil)
	if err != nil {
		return "", err
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
				return nil, fmt.Errorf("AES password not specified in $APP2KUBE_PASSWORD")
			}
			if strings.HasPrefix(value, aesPrefix) {
				value = value[len(aesPrefix):]
			} else {
				value = value[len(cryptPrefix):]
			}
			decrypted, err := DecryptAES(app.aesPassword, value)
			if err != nil {
				return nil, err
			}
			secrets[key] = decrypted
		} else if strings.HasPrefix(value, rsaPrefix) {
			if app.rsaPrivateKey == "" {
				return nil, fmt.Errorf("RSA private key not specified in $APP2KUBE_DECRYPT_KEY")
			}
			value = value[len(rsaPrefix):]
			decrypted, err := DecryptRSA(app.rsaPrivateKey, value)
			if err != nil {
				return nil, err
			}
			secrets[key] = decrypted
		} else {
			secrets[key] = value
		}
	}
	return secrets, nil
}
