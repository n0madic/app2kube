# app2kube

The easiest way to create kubernetes manifests for an application

## Features

* Simple deployment to kubernetes without knowledge of manifest syntax
* Understandable set of values for configuration in YAML
* Supported Kubernetes resources:
  * CronJob
  * Deployment
  * Ingress
  * PersistentVolumeClaim
  * Secret
  * Service
* Secret value encryption with AES-256 CBC
* Support staging
* Track application deployment in kubernetes

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
  completion  Generates bash completion scripts
  encrypt     Encrypt secret values in YAML file
  help        Help about any command
  manifest    Generate kubernetes manifests for an application
  track       Track application deployment in kubernetes

Flags:
  -h, --help      help for app2kube
      --version   version for app2kube
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

```shell
app2kube manifest -f values.yaml | kubectl apply -f -
```

Track deployment till ready:

```shell
app2kube track ready -f values.yaml
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
      livenessProbe:
        tcpSocket:
          port: 8080
      readinessProbe:
        httpGet:
          port: 8080
          path: /healthz
  service:
  - name: http
    port: 8080
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
  - name: http
    port: 80
  ingress:
  - host: "example.com"
    aliases:
    - "www.example.com"
    letsencrypt: true
    sslRedirect: true
```
