package app2kube

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	apiv1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/networking/v1"
)

// wildcardHost rewrites a leading wildcard ("*") in a host into "wildcard" so it
// can appear in a DNS-1123-valid object name.
func wildcardHost(host string) string {
	return strings.Replace(host, "*", "wildcard", 1)
}

// ingressTLSSecretName derives the TLS Secret name for a host: the explicit
// name when set, otherwise tlsSecretPrefix+host (with the wildcard rewrite),
// always lowercased so the Ingress TLS reference and the emitted Secret share a
// byte-identical, DNS-1123-valid name.
func ingressTLSSecretName(explicit, host string) string {
	name := explicit
	if name == "" {
		name = tlsSecretPrefix + wildcardHost(host)
	}
	return strings.ToLower(name)
}

// addRuleForHost appends path to the existing IngressRule serving host, or
// creates a new rule when none exists yet. Routing both the primary host and
// its aliases through this dedup keeps a host to a single rule that accumulates
// all of its paths instead of emitting one duplicate rule per entry (#15).
func addRuleForHost(rules []v1.IngressRule, host string, path v1.HTTPIngressPath) []v1.IngressRule {
	for i := range rules {
		if rules[i].Host == host {
			rules[i].HTTP.Paths = append(rules[i].HTTP.Paths, path)
			return rules
		}
	}
	return append(rules, v1.IngressRule{
		Host: host,
		IngressRuleValue: v1.IngressRuleValue{
			HTTP: &v1.HTTPIngressRuleValue{
				Paths: []v1.HTTPIngressPath{path},
			},
		},
	})
}

// IngressAliases returns the additional hostnames (aliases) to serve alongside
// ing.Host. Aliases are suppressed when a staging environment is configured: a
// staging host is environment-specific and must not also claim the production
// aliases. Centralizing the rule here keeps the ingress generator and the
// status printer from each re-implementing the Staging == "" gate (#69).
func (app *App) IngressAliases(ing Ingress) []string {
	if app.Staging != "" {
		return nil
	}
	return ing.Aliases
}

// GetIngress resource
func (app *App) GetIngress() (ingress []*v1.Ingress, err error) {
	if len(app.Deployment.Containers) > 0 && len(app.Service) > 0 {
		for _, ing := range app.Ingress {
			ingressName := strings.ToLower(app.Name + "-" + wildcardHost(ing.Host))

			newIngress := &v1.Ingress{
				ObjectMeta: app.GetObjectMeta(ingressName),
			}

			var foundIngress bool
			for _, availableIngress := range ingress {
				if availableIngress.Name == ingressName {
					newIngress = availableIngress
					foundIngress = true
					break
				}
			}

			if ing.Class == "" {
				if app.Common.Ingress.Class != "" {
					ing.Class = app.Common.Ingress.Class
				} else {
					ing.Class = "nginx"
				}
			}

			// When this host's ingress object is reused for a repeated entry, its
			// IngressClassName is already set from the first entry. IngressClassName
			// is ingress-wide, so a second entry asking for a different class cannot
			// be represented — error instead of silently letting the last entry win
			// (#58).
			if foundIngress && newIngress.Spec.IngressClassName != nil && *newIngress.Spec.IngressClassName != ing.Class {
				return ingress, fmt.Errorf("ingress %s: conflicting ingressClass %q and %q for the same host", ingressName, *newIngress.Spec.IngressClassName, ing.Class)
			}
			newIngress.Spec.IngressClassName = &ing.Class
			// GetObjectMeta leaves Annotations nil (#67); the ingress is the only
			// resource that adds annotations, so initialize the map lazily here
			// before writing to it (a nil map write would panic).
			if newIngress.Annotations == nil {
				newIngress.Annotations = make(map[string]string)
			}
			if app.Common.Ingress.Letsencrypt || ing.Letsencrypt {
				newIngress.Annotations["kubernetes.io/tls-acme"] = "true"
			}
			for key, value := range app.Common.Ingress.Annotations {
				newIngress.Annotations[key] = value
			}
			for key, value := range ing.Annotations {
				newIngress.Annotations[key] = value
			}

			serviceName := app.GetServiceName(app.Common.Ingress.ServiceName)
			servicePort := app.Common.Ingress.ServicePort
			if ing.ServiceName != "" {
				if svc, ok := app.Service[ing.ServiceName]; ok {
					serviceName = app.GetServiceName(ing.ServiceName)
					servicePort = svc.effectiveServicePort()
				} else {
					return ingress, fmt.Errorf("service with name %s for the ingress %s not found", ing.ServiceName, ing.Host)
				}
			} else {
				if app.Common.Ingress.ServiceName == "" && len(app.Service) == 1 {
					for name, svc := range app.Service {
						serviceName = app.GetServiceName(name)
						servicePort = svc.effectiveServicePort()
					}
				} else {
					return ingress, fmt.Errorf("you must specify a serviceName for the ingress %s", ing.Host)
				}
			}

			if servicePort == 0 {
				return ingress, fmt.Errorf("you must specify a servicePort for the ingress %s", ing.Host)
			}

			if ing.Path == "" {
				ing.Path = "/"
			}

			pathTypeImplementationSpecific := v1.PathTypeImplementationSpecific
			ingressPath := v1.HTTPIngressPath{
				Path:     ing.Path,
				PathType: &pathTypeImplementationSpecific,
				Backend: v1.IngressBackend{
					Service: &v1.IngressServiceBackend{
						Name: strings.ToLower(serviceName),
						Port: v1.ServiceBackendPort{Number: servicePort},
					},
				},
			}
			// Append the path to this host's rule, deduplicating so a host
			// described by multiple entries keeps a single rule accumulating all
			// of its paths.
			newIngress.Spec.Rules = addRuleForHost(newIngress.Spec.Rules, ing.Host, ingressPath)

			tlsIndex := -1
			if app.Common.Ingress.Letsencrypt || ing.Letsencrypt || ing.TLSSecretName != "" || (ing.TLSCrt != "" && ing.TLSKey != "") {
				newIngress.Annotations["nginx.ingress.kubernetes.io/ssl-redirect"] = strconv.FormatBool(app.Common.Ingress.SslRedirect || ing.SslRedirect)
				secretName := ingressTLSSecretName(ing.TLSSecretName, ing.Host)
				// Reuse an existing TLS block sharing this secret name instead of
				// appending a duplicate when the same host repeats across entries;
				// only ensure this host is listed on it.
				for i := range newIngress.Spec.TLS {
					if newIngress.Spec.TLS[i].SecretName == secretName {
						tlsIndex = i
						break
					}
				}
				if tlsIndex < 0 {
					newIngress.Spec.TLS = append(newIngress.Spec.TLS, v1.IngressTLS{
						Hosts:      []string{ing.Host},
						SecretName: secretName,
					})
					tlsIndex = len(newIngress.Spec.TLS) - 1
				} else if !slices.Contains(newIngress.Spec.TLS[tlsIndex].Hosts, ing.Host) {
					newIngress.Spec.TLS[tlsIndex].Hosts = append(newIngress.Spec.TLS[tlsIndex].Hosts, ing.Host)
				}
			}

			// Aliases (suppressed under staging) come from IngressAliases — the
			// single source of that rule, shared with the status printer (#69).
			for _, alias := range app.IngressAliases(ing) {
				// Route aliases through the same per-host dedup as the primary
				// host so a repeated alias accumulates paths on one rule
				// instead of producing a duplicate rule per entry.
				newIngress.Spec.Rules = addRuleForHost(newIngress.Spec.Rules, alias, ingressPath)
				// Attach aliases to the TLS entry created for this host above,
				// not to a hardcoded TLS[0] that may belong to another host;
				// dedup so a repeated alias is not listed twice.
				if tlsIndex >= 0 && !slices.Contains(newIngress.Spec.TLS[tlsIndex].Hosts, alias) {
					newIngress.Spec.TLS[tlsIndex].Hosts = append(newIngress.Spec.TLS[tlsIndex].Hosts, alias)
				}
			}

			if !foundIngress {
				ingress = append(ingress, newIngress)
			}
		}
	}
	return ingress, nil
}

// GetIngressSecrets return TLS secrets for ingress
func (app *App) GetIngressSecrets() (secrets []*apiv1.Secret) {
	if len(app.Deployment.Containers) > 0 && len(app.Service) > 0 {
		emitted := make(map[string]bool)
		for _, ingress := range app.Ingress {
			// Only emit a TLS Secret when actual certificate material is
			// provided. With letsencrypt (or an externally referenced
			// TLSSecretName) the Secret is managed by cert-manager; emitting an
			// empty kubernetes.io/tls Secret here would be rejected as invalid.
			if ingress.TLSCrt != "" && ingress.TLSKey != "" {
				// Share the lowercasing helper with GetIngress so the emitted
				// Secret name is byte-identical to the Ingress TLS reference, and
				// skip duplicates when the same host (secret name) repeats.
				secretName := ingressTLSSecretName(ingress.TLSSecretName, ingress.Host)
				if emitted[secretName] {
					continue
				}
				emitted[secretName] = true
				secret := &apiv1.Secret{
					ObjectMeta: app.GetObjectMeta(secretName),
					Data: map[string][]byte{
						"tls.crt": []byte(ingress.TLSCrt),
						"tls.key": []byte(ingress.TLSKey),
					},
					Type: apiv1.SecretTypeTLS,
				}
				secrets = append(secrets, secret)
			}
		}
	}
	return
}
