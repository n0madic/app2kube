package app2kube

import (
	"fmt"
	"strconv"
	"strings"

	apiv1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/networking/v1"
)

// GetIngress resource
func (app *App) GetIngress() (ingress []*v1.Ingress, err error) {
	if len(app.Deployment.Containers) > 0 && len(app.Service) > 0 {
		for _, ing := range app.Ingress {
			ingressName := app.Name + "-" + strings.Replace(ing.Host, "*", "wildcard", 1)

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
				if rule.Host == ing.Host {
					foundHost = true
				}
				newIngress.Spec.Rules[i].IngressRuleValue.HTTP.Paths = append(
					newIngress.Spec.Rules[i].IngressRuleValue.HTTP.Paths,
					ingressPath,
				)
			}

			if !foundHost {
				newIngress.Spec.Rules = append(newIngress.Spec.Rules, ingressRule)
			}

			if app.Common.Ingress.Letsencrypt || ing.Letsencrypt || ing.TLSSecretName != "" || (ing.TLSCrt != "" && ing.TLSKey != "") {
				newIngress.Annotations["nginx.ingress.kubernetes.io/ssl-redirect"] = strconv.FormatBool(app.Common.Ingress.SslRedirect || ing.SslRedirect)
				if ing.TLSSecretName == "" {
					ing.TLSSecretName = "tls-" + strings.Replace(ing.Host, "*", "wildcard", 1)
				}
				newIngress.Spec.TLS = append(newIngress.Spec.TLS, v1.IngressTLS{
					Hosts:      []string{ing.Host},
					SecretName: strings.ToLower(ing.TLSSecretName),
				})
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
					if app.Common.Ingress.Letsencrypt || ing.Letsencrypt || ing.TLSSecretName != "" || (ing.TLSCrt != "" && ing.TLSKey != "") {
						newIngress.Spec.TLS[0].Hosts = append(newIngress.Spec.TLS[0].Hosts, alias)
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
		for _, ingress := range app.Ingress {
			if app.Common.Ingress.Letsencrypt || ingress.Letsencrypt || (ingress.TLSCrt != "" && ingress.TLSKey != "") {
				if ingress.TLSSecretName == "" {
					ingress.TLSSecretName = "tls-" + strings.Replace(ingress.Host, "*", "wildcard", 1)
				}
				secret := &apiv1.Secret{
					ObjectMeta: app.GetObjectMeta(ingress.TLSSecretName),
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
