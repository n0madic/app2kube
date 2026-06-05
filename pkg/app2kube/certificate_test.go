package app2kube

import (
	"slices"
	"testing"
)

// certByName returns the Certificate with the given object name, or nil.
func certByName(certs []*Certificate, name string) *Certificate {
	for _, c := range certs {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// One Certificate is emitted per domain/secret regardless of how many ingress
// entries (routes/paths) reference the same host — mirroring the single TLS
// Secret per host.
func TestCertificatesOnePerSecretAcrossRoutes(t *testing.T) {
	app := ingressTestApp()
	app.Ingress = []Ingress{
		{Host: "le.example.com", Path: "/a", IngressCommon: IngressCommon{Letsencrypt: true}},
		{Host: "le.example.com", Path: "/b", IngressCommon: IngressCommon{Letsencrypt: true}},
	}
	certs, err := app.GetCertificates()
	if err != nil {
		t.Fatalf("GetCertificates: %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("expected exactly one Certificate for repeated host, got %d", len(certs))
	}
	if got := certs[0].Spec.DNSNames; len(got) != 1 || got[0] != "le.example.com" {
		t.Errorf("dnsNames must be deduplicated to [le.example.com], got %v", got)
	}
}

// dnsNames include the ingress aliases; under staging the aliases are suppressed
// (same rule as the Ingress rules/TLS hosts, via IngressAliases).
func TestCertificatesDNSNamesIncludeAliases(t *testing.T) {
	app := ingressTestApp()
	app.Ingress = []Ingress{{
		Host:          "le.example.com",
		Aliases:       []string{"www.le.example.com", "alt.example.com"},
		IngressCommon: IngressCommon{Letsencrypt: true},
	}}

	certs, err := app.GetCertificates()
	if err != nil {
		t.Fatalf("GetCertificates: %v", err)
	}
	want := []string{"le.example.com", "www.le.example.com", "alt.example.com"}
	if got := certs[0].Spec.DNSNames; !slices.Equal(got, want) {
		t.Errorf("dnsNames: got %v, want %v", got, want)
	}

	// Under staging, aliases are suppressed.
	app.Staging = "stg"
	certs, err = app.GetCertificates()
	if err != nil {
		t.Fatalf("GetCertificates (staging): %v", err)
	}
	if got := certs[0].Spec.DNSNames; len(got) != 1 || got[0] != "le.example.com" {
		t.Errorf("staging dnsNames must drop aliases, got %v", got)
	}
}

// The effective ClusterIssuer resolves per-entry → common → default.
func TestCertificatesClusterIssuerResolution(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		app := ingressTestApp()
		app.Ingress = []Ingress{{Host: "le.example.com", IngressCommon: IngressCommon{Letsencrypt: true}}}
		certs, err := app.GetCertificates()
		if err != nil {
			t.Fatalf("GetCertificates: %v", err)
		}
		if got := certs[0].Spec.IssuerRef.Name; got != "letsencrypt-prod" {
			t.Errorf("default issuer: got %q, want letsencrypt-prod", got)
		}
	})

	t.Run("common override", func(t *testing.T) {
		app := ingressTestApp()
		app.Common.Ingress.ClusterIssuer = "letsencrypt-staging"
		app.Ingress = []Ingress{{Host: "le.example.com", IngressCommon: IngressCommon{Letsencrypt: true}}}
		certs, err := app.GetCertificates()
		if err != nil {
			t.Fatalf("GetCertificates: %v", err)
		}
		if got := certs[0].Spec.IssuerRef.Name; got != "letsencrypt-staging" {
			t.Errorf("common issuer: got %q, want letsencrypt-staging", got)
		}
	})

	t.Run("per-entry override wins", func(t *testing.T) {
		app := ingressTestApp()
		app.Common.Ingress.ClusterIssuer = "letsencrypt-staging"
		app.Ingress = []Ingress{{
			Host:          "le.example.com",
			IngressCommon: IngressCommon{Letsencrypt: true, ClusterIssuer: "letsencrypt-cloudflare"},
		}}
		certs, err := app.GetCertificates()
		if err != nil {
			t.Fatalf("GetCertificates: %v", err)
		}
		if got := certs[0].Spec.IssuerRef.Name; got != "letsencrypt-cloudflare" {
			t.Errorf("per-entry issuer must win: got %q, want letsencrypt-cloudflare", got)
		}
	})
}

// A wildcard entry (DNS-01, e.g. a Cloudflare issuer) alongside a regular entry
// on the default issuer yields two Certificates with distinct issuerRefs, each
// untouched by the other.
func TestCertificatesMixedWildcardAndDefault(t *testing.T) {
	app := ingressTestApp()
	app.Ingress = []Ingress{
		{Host: "le.example.com", IngressCommon: IngressCommon{Letsencrypt: true}},
		{Host: "*.example.com", IngressCommon: IngressCommon{Letsencrypt: true, ClusterIssuer: "letsencrypt-cloudflare"}},
	}
	certs, err := app.GetCertificates()
	if err != nil {
		t.Fatalf("GetCertificates: %v", err)
	}
	if len(certs) != 2 {
		t.Fatalf("expected two Certificates, got %d", len(certs))
	}

	regular := certByName(certs, "tls-le.example.com")
	if regular == nil {
		t.Fatalf("missing regular certificate tls-le.example.com: %+v", certs)
	}
	if regular.Spec.IssuerRef.Name != "letsencrypt-prod" {
		t.Errorf("regular issuer: got %q, want letsencrypt-prod", regular.Spec.IssuerRef.Name)
	}

	wildcard := certByName(certs, "tls-wildcard.example.com")
	if wildcard == nil {
		t.Fatalf("missing wildcard certificate tls-wildcard.example.com: %+v", certs)
	}
	if wildcard.Spec.IssuerRef.Name != "letsencrypt-cloudflare" {
		t.Errorf("wildcard issuer: got %q, want letsencrypt-cloudflare", wildcard.Spec.IssuerRef.Name)
	}
	if got := wildcard.Spec.DNSNames; len(got) != 1 || got[0] != "*.example.com" {
		t.Errorf("wildcard dnsNames must keep the raw SAN, got %v", got)
	}
}

// Two entries sharing one TLS secret name but resolving to different issuers are
// a fatal misconfiguration: only one Certificate can carry that name.
func TestCertificatesConflictingIssuerForSecret(t *testing.T) {
	app := ingressTestApp()
	app.Ingress = []Ingress{
		{Host: "a.example.com", TLSSecretName: "shared-tls", IngressCommon: IngressCommon{Letsencrypt: true, ClusterIssuer: "issuer-a"}},
		{Host: "b.example.com", TLSSecretName: "shared-tls", IngressCommon: IngressCommon{Letsencrypt: true, ClusterIssuer: "issuer-b"}},
	}
	if _, err := app.GetCertificates(); err == nil {
		t.Error("expected an error for conflicting clusterIssuer under one secret name")
	}
}

// A wildcard host maps to a DNS-1123-valid object name (tls-wildcard.<domain>)
// while keeping the raw "*" in the requested SAN.
func TestCertificatesWildcardNaming(t *testing.T) {
	app := ingressTestApp()
	app.Ingress = []Ingress{{Host: "*.example.com", IngressCommon: IngressCommon{Letsencrypt: true}}}
	certs, err := app.GetCertificates()
	if err != nil {
		t.Fatalf("GetCertificates: %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("expected one Certificate, got %d", len(certs))
	}
	if certs[0].Name != "tls-wildcard.example.com" || certs[0].Spec.SecretName != "tls-wildcard.example.com" {
		t.Errorf("wildcard naming: got name=%q secretName=%q, want tls-wildcard.example.com", certs[0].Name, certs[0].Spec.SecretName)
	}
	if got := certs[0].Spec.DNSNames; len(got) != 1 || got[0] != "*.example.com" {
		t.Errorf("wildcard dnsNames: got %v, want [*.example.com]", got)
	}
}

// The Certificate must carry the app's recommended labels so `apply --prune` /
// `delete all` (which select by those labels) recognise it as part of the app.
func TestCertificatesCarryAppLabels(t *testing.T) {
	app := ingressTestApp()
	app.ensureLabels()
	app.Labels[LabelName] = truncateName(app.Name)
	app.Ingress = []Ingress{{Host: "le.example.com", IngressCommon: IngressCommon{Letsencrypt: true}}}

	certs, err := app.GetCertificates()
	if err != nil {
		t.Fatalf("GetCertificates: %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("expected one Certificate, got %d", len(certs))
	}
	for _, k := range []string{LabelManagedBy, LabelInstance, LabelName} {
		if _, ok := certs[0].Labels[k]; !ok {
			t.Errorf("Certificate missing prune-selector label %q: %v", k, certs[0].Labels)
		}
	}
}

// common.ingress.letsencrypt enables Certificate emission for every entry even
// when the entry itself does not set letsencrypt.
func TestCertificatesCommonLetsencrypt(t *testing.T) {
	app := ingressTestApp()
	app.Common.Ingress.Letsencrypt = true
	app.Ingress = []Ingress{{Host: "le.example.com"}}
	certs, err := app.GetCertificates()
	if err != nil {
		t.Fatalf("GetCertificates: %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("common letsencrypt must emit a Certificate, got %d", len(certs))
	}
}

// Without letsencrypt no Certificate is emitted (an inline-cert ingress manages
// its own Secret and needs no cert-manager Certificate).
func TestCertificatesNoneWithoutLetsencrypt(t *testing.T) {
	app := ingressTestApp()
	app.Ingress = []Ingress{
		{Host: "plain.example.com"},
		{Host: "cert.example.com", TLSCrt: "CRT", TLSKey: "KEY"},
	}
	certs, err := app.GetCertificates()
	if err != nil {
		t.Fatalf("GetCertificates: %v", err)
	}
	if len(certs) != 0 {
		t.Errorf("no Certificate must be emitted without letsencrypt, got %d: %+v", len(certs), certs)
	}
}
