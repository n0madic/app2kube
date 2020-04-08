package app2kube

import (
	apiv1 "k8s.io/api/core/v1"
)

// GetSecret resource
func (app *App) GetSecret() (secret *apiv1.Secret, err error) {
	if len(app.Secrets) > 0 {
		secretBytes := make(map[string][]byte)
		secretDecrypted, err := app.GetDecryptedSecrets()
		if err != nil {
			return nil, err
		}
		for key, value := range secretDecrypted {
			secretBytes[key] = []byte(value)
		}

		secret = &apiv1.Secret{
			ObjectMeta: app.GetObjectMeta(app.GetReleaseName()),
			Data:       secretBytes,
		}
	}
	return secret, nil
}
