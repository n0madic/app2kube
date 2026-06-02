package app2kube

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	apiv1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/networking/v1"
)

// ingressTLSSecretName derives the TLS Secret name for a host: the explicit
// name when set, otherwise "tls-"+host (with the wildcard rewrite), always
// lowercased so the Ingress TLS reference and the emitted Secret share a
// byte-identical, DNS-1123-valid name.
func ingressTLSSecretName(explicit, host string) string {
	name := explicit
	if name == "" {
		name = "tls-" + strings.Replace(host, "*", "wildcard", 1)
	}
	return strings.ToLower(name)
}

// GetIngress resource
func (app *App) GetIngress() (ingress []*v1.Ingress, err error) {
	if len(app.Deployment.Containers) > 0 && len(app.Service) > 0 {
		for _, ing := range app.Ingress {
			ingressName := strings.ToLower(app.Name + "-" + strings.Replace(ing.Host, "*", "wildcard", 1))

			newIngress := &v1.Ingress{
				ObjectMeta: app.GetObjectMeta(ingressName),
			}

			var foundIngress bool
			for _, availableIngress := range ingress {
				if availableIngress.ObjectMeta.Name == ingressName {
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

			newIngress.Spec.IngressClassName = &ing.Class
			if app.Common.Ingress.Letsencrypt || ing.Letsencrypt {
				newIngress.Annotations["kubernetes.io/tls-acme"] = "true"
			}
			for key, value := range app.Common.Ingress.Annotations {
				newIngress.Annotations[key] = value
			}
			for key, value := range ing.Annotations {
				newIngress.Annotations[key] = value
			}

			serviceName := app.getServiceName(app.Common.Ingress.ServiceName)
			servicePort := app.Common.Ingress.ServicePort
			if ing.ServiceName != "" {
				if svc, ok := app.Service[ing.ServiceName]; ok {
					serviceName = app.getServiceName(ing.ServiceName)
					if svc.ExternalPort > 0 {
						servicePort = svc.ExternalPort
					} else {
						servicePort = svc.Port
					}
				} else {
					return ingress, fmt.Errorf("Service with name %s for the ingress %s not found", ing.ServiceName, ing.Host)
				}
			} else {
				if app.Common.Ingress.ServiceName == "" && len(app.Service) == 1 {
					for name, svc := range app.Service {
						serviceName = app.getServiceName(name)
						if svc.ExternalPort > 0 {
							servicePort = svc.ExternalPort
						} else {
							servicePort = svc.Port
						}
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
			ingressRule := v1.IngressRule{
				Host: ing.Host,
				IngressRuleValue: v1.IngressRuleValue{
					HTTP: &v1.HTTPIngressRuleValue{
						Paths: []v1.HTTPIngressPath{ingressPath},
					},
				},
			}

			foundHost := false
			for i, rule := range newIngress.Spec.Rules {
				// Only append the path to the rule that serves this host, not to
				// every existing rule (which would corrupt other hosts/aliases
				// sharing the same ingress object).
				if rule.Host == ing.Host {
					foundHost = true
					newIngress.Spec.Rules[i].IngressRuleValue.HTTP.Paths = append(
						newIngress.Spec.Rules[i].IngressRuleValue.HTTP.Paths,
						ingressPath,
					)
				}
			}

			if !foundHost {
				newIngress.Spec.Rules = append(newIngress.Spec.Rules, ingressRule)
			}

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

			if app.Staging == "" {
				for _, alias := range ing.Aliases {
					newIngress.Spec.Rules = append(newIngress.Spec.Rules, v1.IngressRule{
						Host: alias,
						IngressRuleValue: v1.IngressRuleValue{
							HTTP: &v1.HTTPIngressRuleValue{
								Paths: []v1.HTTPIngressPath{ingressPath},
							},
						},
					})
					// Attach aliases to the TLS entry created for this host above,
					// not to a hardcoded TLS[0] that may belong to another host.
					if tlsIndex >= 0 {
						newIngress.Spec.TLS[tlsIndex].Hosts = append(newIngress.Spec.TLS[tlsIndex].Hosts, alias)
					}
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
