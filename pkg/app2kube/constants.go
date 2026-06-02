package app2kube

// Recommended Kubernetes labels emitted on generated resources. They are also
// used by the prune/delete selector and blue/green color tracking, so they are
// centralized here: a rename in one place cannot silently desync a selector
// from the labels it is supposed to match.
const (
	LabelName      = "app.kubernetes.io/name"
	LabelInstance  = "app.kubernetes.io/instance"
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelColor     = "app.kubernetes.io/color"

	// ManagedByValue is the value of the managed-by label.
	ManagedByValue = "app2kube"
)

// Environment variables holding the secret encryption password / RSA keys. They
// are read when building an App and echoed in decrypt error messages, so they
// must stay identical across both sites.
const (
	EnvPassword   = "APP2KUBE_PASSWORD"
	EnvEncryptKey = "APP2KUBE_ENCRYPT_KEY"
	EnvDecryptKey = "APP2KUBE_DECRYPT_KEY"
)

const (
	// sharedDataVolumeName is the EmptyDir shared between an app's containers.
	sharedDataVolumeName = "shared-data"
	// tlsSecretPrefix prefixes a host-derived TLS Secret name.
	tlsSecretPrefix = "tls-"
)
