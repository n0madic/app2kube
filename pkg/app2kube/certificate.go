package app2kube

import (
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Certificate is a minimal, dependency-free rendering of the cert-manager
// cert-manager.io/v1 Certificate resource. cert-manager itself is intentionally
// NOT added to go.mod (it would drag in controller-runtime, apiextensions, …);
// this typed struct carries json tags and a pre-filled TypeMeta so the shared
// printer (output.go) serializes a correct manifest. Because TypeMeta is
// pre-populated, the printer's type setter sees a non-empty GVK and delegates
// straight to the YAML/JSON encoder instead of consulting the (unaware) scheme.
type Certificate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              CertificateSpec `json:"spec"`
}

// CertificateSpec is the subset of cert-manager's CertificateSpec app2kube
// emits: the placeholder Secret to fill, the SANs to request, and the issuer.
type CertificateSpec struct {
	SecretName string          `json:"secretName"`
	DNSNames   []string        `json:"dnsNames"`
	IssuerRef  IssuerReference `json:"issuerRef"`
}

// IssuerReference points at the (cluster-scoped) issuer that signs the cert.
type IssuerReference struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Group string `json:"group"`
}

// DeepCopyObject implements runtime.Object so the Certificate can flow through
// the same printing pipeline as the built-in kinds.
func (c *Certificate) DeepCopyObject() runtime.Object {
	if c == nil {
		return nil
	}
	out := new(Certificate)
	out.TypeMeta = c.TypeMeta
	c.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec.SecretName = c.Spec.SecretName
	out.Spec.IssuerRef = c.Spec.IssuerRef
	if c.Spec.DNSNames != nil {
		out.Spec.DNSNames = make([]string, len(c.Spec.DNSNames))
		copy(out.Spec.DNSNames, c.Spec.DNSNames)
	}
	return out
}

// ingressClusterIssuer resolves the effective cert-manager ClusterIssuer name
// for an ingress entry: the per-entry override wins, then the common default,
// then the built-in defaultClusterIssuer (which equals the cluster's
// ingress-shim defaultIssuerName, so tls-acme-style ingresses keep working
// without naming an issuer). A domain whose cert uses a different issuer (e.g. a
// DNS-01 letsencrypt-cloudflare for wildcards) sets clusterIssuer explicitly.
func (app *App) ingressClusterIssuer(ing Ingress) string {
	if ing.ClusterIssuer != "" {
		return ing.ClusterIssuer
	}
	if app.Common.Ingress.ClusterIssuer != "" {
		return app.Common.Ingress.ClusterIssuer
	}
	return defaultClusterIssuer
}

// GetCertificates returns one cert-manager Certificate per unique TLS Secret
// name among the letsencrypt-enabled ingresses — one Certificate per
// domain/secret regardless of how many routes or paths reference the host. The
// Certificate name, its spec.secretName and the Ingress TLS secretName all come
// from ingressTLSSecretName, so cert-manager fills the very placeholder Secret
// GetIngressSecrets emits (which prune-protects the live cert).
//
// It mirrors the guard and dedup of GetIngressSecrets: a repeated host
// accumulates dnsNames on the existing Certificate, while two entries sharing a
// secret name but resolving to different issuers are a fatal misconfiguration
// (only one Certificate can carry that name).
func (app *App) GetCertificates() (certs []*Certificate, err error) {
	if len(app.Deployment.Containers) > 0 && len(app.Service) > 0 {
		emitted := make(map[string]*Certificate)
		for _, ingress := range app.Ingress {
			if !(app.Common.Ingress.Letsencrypt || ingress.Letsencrypt) {
				continue
			}
			// Share the lowercasing/wildcard helper with GetIngress and
			// GetIngressSecrets so the Certificate name, its secretName and the
			// Ingress TLS reference are byte-identical.
			secretName := ingressTLSSecretName(ingress.TLSSecretName, ingress.Host)
			issuer := app.ingressClusterIssuer(ingress)
			// dnsNames keep the raw host (preserving a "*" wildcard for the SAN);
			// only the object name has the wildcard rewritten. Aliases are
			// suppressed under staging, exactly as in GetIngress (#69).
			dnsNames := appendUnique([]string{ingress.Host}, app.IngressAliases(ingress)...)

			if cert, ok := emitted[secretName]; ok {
				if cert.Spec.IssuerRef.Name != issuer {
					return nil, fmt.Errorf("conflicting clusterIssuer %q and %q for certificate %q", cert.Spec.IssuerRef.Name, issuer, secretName)
				}
				cert.Spec.DNSNames = appendUnique(cert.Spec.DNSNames, dnsNames...)
				continue
			}

			cert := &Certificate{
				TypeMeta: metav1.TypeMeta{
					APIVersion: certManagerAPIVersion,
					Kind:       certManagerKind,
				},
				ObjectMeta: app.GetObjectMeta(secretName),
				Spec: CertificateSpec{
					SecretName: secretName,
					DNSNames:   dnsNames,
					IssuerRef: IssuerReference{
						Name:  issuer,
						Kind:  clusterIssuerKind,
						Group: certManagerGroup,
					},
				},
			}
			emitted[secretName] = cert
			certs = append(certs, cert)
		}
	}
	return certs, nil
}

// appendUnique appends each value not already present in dst, preserving order.
func appendUnique(dst []string, values ...string) []string {
	for _, v := range values {
		if !slices.Contains(dst, v) {
			dst = append(dst, v)
		}
	}
	return dst
}
