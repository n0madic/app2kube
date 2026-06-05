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

// cert-manager Certificate generation constants. The type is rendered from a
// minimal local struct (certificate.go) so cert-manager is not pulled into
// go.mod.
const (
	// defaultClusterIssuer matches the cluster's ingress-shim defaultIssuerName,
	// used when letsencrypt is on but no explicit clusterIssuer is configured —
	// so tls-acme-style ingresses keep working without naming an issuer.
	defaultClusterIssuer = "letsencrypt-prod"

	certManagerGroup      = "cert-manager.io"
	certManagerAPIVersion = "cert-manager.io/v1"
	certManagerKind       = "Certificate"
	// clusterIssuerKind is the issuerRef kind app2kube emits — only the cluster
	// scoped ClusterIssuer is supported (group cert-manager.io).
	clusterIssuerKind = "ClusterIssuer"
)

// Pod-template annotation keys carrying the sha256 of the referenced config, so
// a change to the ConfigMap/Secret content rolls the workload (#22).
const (
	annotationChecksumConfigMap = "checksum/configmap"
	annotationChecksumSecret    = "checksum/secret"
)
