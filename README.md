# app2kube

The easiest way to create and apply kubernetes manifests for an application

## Features

* Simple deployment to kubernetes without knowledge of manifest syntax
* There are no built-in manifest templates under the hood, only native kubernetes objects
* Understandable set of values for configuration in YAML
* Templating values in YAML file with [sprig](http://masterminds.github.io/sprig/) functions
* Supported Kubernetes resources:
  * ConfigMap
  * CronJob
  * Deployment
  * Ingress
  * Namespace
  * PersistentVolumeClaim
  * Secret
  * Service
* Secret value encryption with AES-256 CBC or RSA-2048
* Support staging
* Build and push docker image
* Apply/delete a configuration to a resource in kubernetes
* Track application deployment in kubernetes
* Blue/green deployment
* Portable - `apply`/`delete` command ported from kubectl, `build` from docker-cli

## Install

Download binaries from [release](https://github.com/n0madic/app2kube/releases) page.

Or install from source:

```shell
go get -u github.com/n0madic/app2kube
```

## Help

```help
Usage:
  app2kube [command]

Available Commands:
  apply       Apply a configuration to a resource in kubernetes
  blue-green  Commands for blue-green deployment
  build       Build and push an image from a Dockerfile
  completion  Generates bash completion scripts
  config      Manage application config
  delete      Delete resources from kubernetes
  help        Help about any command
  manifest    Generate kubernetes manifests for an application
  status      Show application resources status in kubernetes
  track       Track application deployment in kubernetes

Flags:
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

By default, it tries to use the `.app2kube.yml` file in the current directory:

```yaml
---
name: example
deployment:
  containers:
    example:
      image: "example/image:latest"
```

OR

```yaml
---
name: example
common:
  image:
    repository: "example/image"
    tag: "latest"
deployment:
  containers:
    example: {}
```

Get kubernetes manifest:

```shell
app2kube manifest
```

Build and push docker image:

```shell
app2kube build --push
```

Apply application manifest in kubernetes:

```shell
app2kube apply
```

Track deployment till ready:

```shell
app2kube track ready
```

Delete application manifest from kubernetes:

```shell
app2kube delete
```

## Secrets

The `secrets:` section of the YAML file can store secrets, which can be encrypted using either AES-256 CBC or RSA-2048. To use AES-256 CBC, set the `APP2KUBE_PASSWORD` environment variable. For RSA-2048, set the `APP2KUBE_ENCRYPT_KEY` or `APP2KUBE_DECRYPT_KEY` environment variable. If both are set, RSA will take precedence over AES.

To encrypt YAML file:
```shell
app2kube config encrypt --values secrets.yml
```

To encrypt a single value:
```shell
app2kube config encrypt --string "secret"
```

Encrypted values are prefixed with `AES#` or `RSA#` depending on the encryption algorithm. It is possible to use both encryption algorithms in one file.

To decrypt, set environment variable `APP2KUBE_PASSWORD` or `APP2KUBE_DECRYPT_KEY`.
```shell
app2kube config secrets
```

## Staging

For a staging release, you must set the `staging` and optional `branch` parameters:

```shell
app2kube manifest --set staging=alpha --set branch=develop
```

In this case, some values will be reset to more optimal for staging:

```yaml
common:
  image:
    pullPolicy: Always
deployment:
  blueGreenColor: ""
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
