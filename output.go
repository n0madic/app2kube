package app2kube

// OutputResource type
type OutputResource int

const (
	// OutputAll for all resources (except namespace)
	OutputAll OutputResource = iota
	// OutputConfigMap only
	OutputConfigMap
	// OutputCronJob only
	OutputCronJob
	// OutputDeployment only
	OutputDeployment
	// OutputIngress only
	OutputIngress
	// OutputNamespace only
	OutputNamespace
	// OutputPersistentVolumeClaim only
	OutputPersistentVolumeClaim
	// OutputSecret only
	OutputSecret
	// OutputService only
	OutputService
)

// GetManifest returns a manifest with the specified resource types
func (app *App) GetManifest(outputFormat string, typeOutput ...OutputResource) (manifest string, err error) {
	for _, out := range typeOutput {
		if out == OutputNamespace {
			namespace := app.GetNamespace()
			yml, err := PrintObj(namespace, outputFormat)
			if err != nil {
				return "", err
			}
			manifest += yml
		}

		if out == OutputAll || out == OutputSecret {
			secret, err := app.GetSecret()
			if err != nil {
				return "", err
			}
			yml, err := PrintObj(secret, outputFormat)
			if err != nil {
				return "", err
			}
			manifest += yml
		}

		if out == OutputAll || out == OutputConfigMap {
			configmap, err := app.GetConfigMap()
			if err != nil {
				return "", err
			}
			yml, err := PrintObj(configmap, outputFormat)
			if err != nil {
				return "", err
			}
			manifest += yml
		}

		if out == OutputAll || out == OutputPersistentVolumeClaim {
			claims, err := app.GetPersistentVolumeClaims()
			if err != nil {
				return "", err
			}
			for _, claim := range claims {
				yml, err := PrintObj(claim, outputFormat)
				if err != nil {
					return "", err
				}
				manifest += yml
			}
		}

		if out == OutputAll || out == OutputCronJob {
			jobs, err := app.GetCronJobs()
			if err != nil {
				return "", err
			}
			for _, cron := range jobs {
				yml, err := PrintObj(cron, outputFormat)
				if err != nil {
					return "", err
				}
				manifest += yml
			}
		}

		if out == OutputAll || out == OutputDeployment {
			deployment, err := app.GetDeployment()
			if err != nil {
				return "", err
			}
			yml, err := PrintObj(deployment, outputFormat)
			if err != nil {
				return "", err
			}
			manifest += yml
		}

		if out == OutputAll || out == OutputService {
			services, err := app.GetServices()
			if err != nil {
				return "", err
			}
			for _, service := range services {
				yml, err := PrintObj(service, outputFormat)
				if err != nil {
					return "", err
				}
				manifest += yml
			}
		}

		if out == OutputAll || out == OutputSecret {
			for _, ingressSecret := range app.GetIngressSecrets() {
				yml, err := PrintObj(ingressSecret, outputFormat)
				if err != nil {
					return "", err
				}
				manifest += yml
			}
		}

		if out == OutputAll || out == OutputIngress {
			ingress, err := app.GetIngress()
			if err != nil {
				return "", err
			}
			for _, ing := range ingress {
				yml, err := PrintObj(ing, outputFormat)
				if err != nil {
					return "", err
				}
				manifest += yml
			}
		}
	}

	return manifest, nil
}
