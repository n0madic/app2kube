package app2kube

import (
	"strings"

	apiv1 "k8s.io/api/core/v1"
)

// GetSecret YAML
func (app *App) GetSecret() (secret *apiv1.Secret, err error) {
	if len(app.Secrets) > 0 {
		secretBytes := make(map[string][]byte)
		for key, value := range app.Secrets {
			if strings.HasPrefix(value, CryptPrefix) {
				password, err := GetPassword()
				if err != nil {
					return nil, err
				}
				value = value[len(CryptPrefix):]
				decrypted, err := DecryptAES(password, value)
				if err != nil {
					return nil, err
				}
				secretBytes[key] = []byte(decrypted)
			} else {
				secretBytes[key] = []byte(value)
			}
		}

		secret = &apiv1.Secret{
			ObjectMeta: app.GetObjectMeta(app.GetReleaseName()),
			Data:       secretBytes,
		}
	}
	return secret, nil
}
