package app2kube

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	apiv1 "k8s.io/api/core/v1"
)

// dataChecksum returns a deterministic lowercase-hex sha256 over a key/value
// data map. Keys are sorted and each pair is emitted in the canonical
// "key=value\n" form, so the digest is stable regardless of Go's random map
// iteration order. It backs the checksum/* pod-template annotations, so a change
// to the rendered ConfigMap/Secret content changes the digest and rolls the
// workload (#22).
func dataChecksum(data map[string][]byte) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte("="))
		h.Write(data[k])
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// configChecksumAnnotations returns the checksum/configmap and checksum/secret
// pod-template annotations for a workload, but only for the config that is
// actually wired into the given containers via envFrom (referenced by the
// release name). This keeps the annotation off workloads that do not consume the
// config — e.g. a pod built solely from a third-party image gets no checksum and
// is never rolled by an unrelated config change. Returns nil when nothing is
// referenced.
//
// The Secret digest is computed over the values exactly as loaded from the
// values file (ciphertext or plaintext), NOT the decrypted form. app2kube never
// re-encrypts during a render, so the loaded value is fixed and the digest is
// deterministic across renders; crucially, it needs no decrypt key, so rendering
// a Deployment that references encrypted secrets never starts to require
// APP2KUBE_DECRYPT_KEY. A change to the stored secret changes the digest and
// rolls the workload; identical ciphertext implies identical plaintext, so a
// real content change is never missed. The value is a one-way sha256, so no
// secret content leaks into the annotation.
func (app *App) configChecksumAnnotations(containers []apiv1.Container) map[string]string {
	releaseName := app.GetReleaseName()
	var refConfigMap, refSecret bool
	for _, c := range containers {
		for _, ef := range c.EnvFrom {
			if ef.ConfigMapRef != nil && ef.ConfigMapRef.Name == releaseName {
				refConfigMap = true
			}
			if ef.SecretRef != nil && ef.SecretRef.Name == releaseName {
				refSecret = true
			}
		}
	}

	annotations := map[string]string{}
	if refConfigMap && len(app.ConfigMap) > 0 {
		annotations[annotationChecksumConfigMap] = dataChecksum(stringMapToBytes(app.ConfigMap))
	}
	if refSecret && len(app.Secrets) > 0 {
		annotations[annotationChecksumSecret] = dataChecksum(stringMapToBytes(app.Secrets))
	}

	if len(annotations) == 0 {
		return nil
	}
	return annotations
}

// stringMapToBytes converts a string-valued data map to the []byte-valued form
// dataChecksum consumes.
func stringMapToBytes(m map[string]string) map[string][]byte {
	out := make(map[string][]byte, len(m))
	for k, v := range m {
		out[k] = []byte(v)
	}
	return out
}
