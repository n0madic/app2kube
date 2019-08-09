# app2kube

The easiest way to create kubernetes manifests for an application

## Features

* Simple deployment to kubernetes without knowledge of manifest syntax
* Understandable set of values for configuration in YAML
* Supported Kubernetes resources:
  * ConfigMap
  * CronJob
  * Deployment
  * Ingress
  * Namespace
  * PersistentVolumeClaim
  * Secret
  * Service
* Secret value encryption with AES-256 CBC
* Support staging
* Build and push docker image
* Apply/delete a configuration to a resource in kubernetes
* Track application deployment in kubernetes
* Portable - `apply`/`delete` command ported from kubectl, `build` from docker-cli

## Install

Download binaries from [release](https://github.com/n0madic/app2kube/releases) page.

Or install from source:

```shell
go get -u github.com/n0madic/app2kube/cmd/app2kube
```

## Help

```help
Usage:
  app2kube [command]

Available Commands:
  apply       Apply a configuration to a resource in kubernetes
  build       Build and push an image from a Dockerfile
  completion  Generates bash completion scripts
  delete      Delete resources from kubernetes
  encrypt     Encrypt secret values in YAML file
  help        Help about any command
  manifest    Generate kubernetes manifests for an application
  track       Track application deployment in kubernetes

Flags:
      --as string                      Username to impersonate for the operation
      --as-group stringArray           Group to impersonate for the operation, this flag can be repeated to specify multiple groups.
      --cache-dir string               Default HTTP cache directory (default "/home/nomadic/.kube/http-cache")
      --certificate-authority string   Path to a cert file for the certificate authority
      --client-certificate string      Path to a client certificate file for TLS
      --client-key string              Path to a client key file for TLS
      --cluster string                 The name of the kubeconfig cluster to use
      --context string                 The name of the kubeconfig context to use
  -h, --help                           help for app2kube
      --insecure-skip-tls-verify       If true, the server's certificate will not be checked for validity. This will make your HTTPS connections insecure
      --kubeconfig string              Path to the kubeconfig file to use for CLI requests.
  -n, --namespace string               If present, the namespace scope for this CLI request
      --request-timeout string         The length of time to wait before giving up on a single server request. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h). A value of zero means don't timeout requests. (default "0")
  -s, --server string                  The address and port of the Kubernetes API server
      --token string                   Bearer token for authentication to the API server
      --user string                    The name of the kubeconfig user to use
      --version                        version for app2kube
```

## Usage

Minimum required values for application deployment in kubernetes - `name` and `image`.

```shell
app2kube manifest --set name=example --set deployment.containers.example.image=example/image:latest
```

OR

`values.yaml`:

```yaml
---
name: example
deployment:
  containers:
    example:
      image: "example/image:latest"
```

Get application manifest:

```shell
app2kube manifest -f values.yaml
```

Apply application manifest in kubernetes:

```shell
app2kube apply -f values.yaml
```

Track deployment till ready:

```shell
app2kube track ready -f values.yaml
```

Delete application manifest from kubernetes:

```shell
app2kube delete -f values.yaml
```

## Staging

For a staging release, you must set the `staging` and optional `branch` parameters:

```shell
app2kube manifest -f values.yaml --set staging=alpha --set branch=develop
```

In this case, some values will be reset to more optimal for staging:

```yaml
common:
  image:
    pullPolicy: Always
deployment:
  replicaCount: 1
  revisionHistoryLimit: 0
```

Also, aliases for ingress and resource requests for containers (for a denser filling of the staging environment) will not be used.

For ingress domains, prefixes from the above values will be automatically added: `staging.example.com` or `branch.staging.example.com`

## Examples

Simple web service:

```yaml
---
name: example
deployment:
  containers:
    example:
      image: "example/image:latest"
      ports:
      - containerPort: 8080
        name: http
      readinessProbe:
        httpGet:
          path: /healthz
ingress:
- host: "example.com"
```

PHP-FPM application with web service (common image) and cron jobs, include memcached:

```yaml
---
name: example
common:
  image:
    repository: "example/php-nginx"
    tag: "7.3"
cronjob:
  update:
    schedule: "0 */1 * * *"
    container:
      command: ["/usr/bin/curl"]
      args: ["https://www.example.com/update"]
  cleaning:
    schedule: "0 0 * * *"
    container:
      command: ["/usr/bin/php"]
      args: ["/var/www/cron.php", "--cleaning"]
deployment:
  containers:
    php-fpm:
      command: ["/usr/sbin/php-fpm7.3"]
      args: ["--nodaemonize", "--force-stderr"]
      resources:
        requests:
          memory: 350Mi
          cpu: 10m
      livenessProbe:
        tcpSocket:
          port: 9000
    nginx:
      command: ["/usr/sbin/nginx"]
      args: ["-g", "daemon off;"]
      lifecycle:
        preStop:
          exec:
            command: ["/usr/sbin/nginx", "-s"," quit"]
      livenessProbe:
        tcpSocket:
          port: 80
      readinessProbe:
        httpGet:
          port: 80
          path: /
        timeoutSeconds: 60
    memcached:
      image: "memcached:alpine"
      imagePullPolicy: IfNotPresent
      livenessProbe:
        tcpSocket:
          port: 11211
service:
  http:
    port: 80
ingress:
- host: "example.com"
  aliases:
  - "www.example.com"
  letsencrypt: true
  sslRedirect: true
```
