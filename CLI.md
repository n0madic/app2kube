# app2kube CLI Reference

This document describes the command-line flags exposed by `app2kube`.

All commands support `-h, --help`. The root command also supports `-v, --version`.
All commands inherit the Kubernetes connection flags listed below.

## Command Tree

```text
app2kube
  apply
  blue-green
    color
    prune
    rollback
  build
  completion [bash|zsh|fish|powershell]
  config
    domain
    dotenv
    encrypt
    generate-keys
    secrets
  delete
  help [command]
  manifest
  status
  track
    follow
    ready
```

## Global Kubernetes Flags

These flags are inherited by every command and subcommand.

| Flag | Type | Description | Default |
| --- | --- | --- | --- |
| `--as-uid` | string | UID to impersonate for the Kubernetes operation. | empty |
| `--certificate-authority` | string | Path to a certificate authority file. | empty |
| `--client-certificate` | string | Path to a client certificate file for TLS. | empty |
| `--client-key` | string | Path to a client key file for TLS. | empty |
| `--cluster` | string | Kubeconfig cluster name to use. | empty |
| `--context` | string | Kubeconfig context name to use. If unset, `KUBECONTEXT` is used when present. | empty |
| `--disable-compression` | bool | Disable response compression for requests to the Kubernetes API server. | `false` |
| `--insecure-skip-tls-verify` | bool | Skip Kubernetes API server certificate validation. This makes HTTPS connections insecure. | `false` |
| `--kubeconfig` | string | Path to the kubeconfig file to use. | empty |
| `-n, --namespace` | string | Namespace scope for Kubernetes requests. | empty |
| `--request-timeout` | string | Timeout for a single server request. Use values such as `1s`, `2m`, or `3h`; `0` disables the timeout. | `0` |
| `-s, --server` | string | Kubernetes API server address and port. | empty |
| `--tls-server-name` | string | Server name used for TLS certificate validation. If omitted, the contacted hostname is used. | empty |
| `--token` | string | Bearer token for Kubernetes API authentication. | empty |
| `--user` | string | Kubeconfig user name to use. This is unrelated to Docker registry authentication. | empty |

## Standard and Hidden Global Flags

| Flag | Type | Description | Default |
| --- | --- | --- | --- |
| `-h, --help` | bool | Show help for the selected command. | `false` |
| `-v, --version` | bool | Show app2kube version. Root command only. | `false` |
| `--as` | string | Hidden. Kubernetes username to impersonate. The value can be a regular user or a service account name. | empty |
| `--as-group` | stringArray | Hidden. Kubernetes groups to impersonate. May be repeated. | `[]` |
| `--cache-dir` | string | Hidden. Kubernetes discovery and HTTP cache directory. | Kubernetes default cache directory |

app2kube hides `--as`, `--as-group`, and `--cache-dir` from command help, but
the underlying Kubernetes client still accepts them.

## Common Application Value Flags

The following flags are added to app-aware commands that load app2kube values:
`apply`, `build`, `delete`, `manifest`, `status`, `track follow`, `track ready`,
`blue-green color`, `blue-green prune`, `blue-green rollback`, `config dotenv`,
`config domain`, and `config secrets`.

| Flag | Type | Description | Default |
| --- | --- | --- | --- |
| `-f, --values` | valueFiles | Load values from a YAML file. May be repeated. Add `?` to the file name to skip it when it does not exist. | `[]` |
| `--set` | stringArray | Set values from the command line. May be repeated or comma-separated, for example `key1=val1,key2=val2`. | `[]` |
| `--set-file` | stringArray | Set values from files, for example `key1=path1,key2=path2`. May be repeated or comma-separated. | `[]` |
| `--set-string` | stringArray | Set command-line values as strings. May be repeated or comma-separated. | `[]` |
| `-v, --verbose` | bool | Print the merged YAML values to stderr before running the command. | `false` |
| `--include-namespace` | bool | Include or target the Kubernetes Namespace object. Visible on `apply`, `delete`, and `manifest`; accepted but hidden on other app-aware commands where it is not useful. | `false` |

When `.app2kube.yml` exists in the current directory, app2kube loads it as the
base values file before any `--values`, `--set`, `--set-string`, or `--set-file`
overrides. Values are required unless the command supports a special mode such
as `status --all`.

Namespace precedence is: `--namespace` flag, then value-file `namespace:`, then
`default`. An explicitly set empty namespace flag, `--namespace ""`, forces the
`default` namespace.

Value files are trusted input. They are rendered through sprig templates before
YAML parsing, so they can read environment variables and perform template-time
lookups.

## `app2kube manifest`

Generates Kubernetes manifests for an application.

Usage:

```text
app2kube manifest [flags]
```

Includes the common application value flags.

| Flag | Type | Description | Default |
| --- | --- | --- | --- |
| `--blue-green` | bool | Render manifests for the next blue/green deployment color. | `false` |
| `-o, --output` | string | Output format passed to the Kubernetes printer. Common values are `yaml` and `json`. | `yaml` |
| `--type` | stringArray | Resource types to render. May be repeated. Accepted values are case-insensitive: `all`, `certificate`, `configmap`, `cronjob`, `deployment`, `ingress`, `pdb`, `pvc`, `secret`, `service`. | `[all]` |

`all` renders all generated resources except the Namespace. Use
`--include-namespace` to prepend the Namespace manifest when the resolved
namespace is not `default`.

## `app2kube apply`

Applies the generated manifest to Kubernetes.

Usage:

```text
app2kube apply [flags]
```

Includes the common application value flags.

| Flag | Type | Description | Default |
| --- | --- | --- | --- |
| `--allow-missing-template-keys` | bool | Ignore missing keys in templates for `go-template` and `jsonpath` output formats. | `true` |
| `--blue-green` | bool | Run a two-phase blue/green apply: deploy the target color, wait for it to be ready, then switch Service and Ingress resources. | `false` |
| `--dry-run` | string | Must be `none`, `server`, or `client`. With `client`, print the object without sending it; with `server`, submit the request without persisting it. | `none` |
| `--field-manager` | string | Field manager name used to track apply ownership. | `kubectl-client-side-apply` |
| `--force-conflicts` | bool | With server-side apply, force changes against conflicts. | `false` |
| `-o, --output` | string | Print applied objects in one of Kubernetes' output formats: `json`, `yaml`, `name`, `go-template`, `go-template-file`, `template`, `templatefile`, `jsonpath`, `jsonpath-as-json`, or `jsonpath-file`. | empty |
| `--prune` | bool | Delete app2kube-managed objects matching the app selector that no longer appear in the generated manifest. | `false` |
| `--server-side` | bool | Use server-side apply instead of client-side apply. | `false` |
| `--show-managed-fields` | bool | Keep `managedFields` when printing objects in JSON or YAML. | `false` |
| `--status` | bool | Show application resource status after apply. | `false` |
| `--template` | string | Template string or template file path for `go-template` and related output formats. | empty |
| `--timeout` | int | Timeout in minutes for `--track`; `0` waits forever. | `15` |
| `--track` | string | Track the Deployment after apply. Accepted values are `ready` and `follow`. | empty |
| `--validate` | string | Schema validation mode. Accepted values: `strict` or `true`, `warn`, `ignore` or `false`. | `strict` |

`--prune` cannot be used together with `--blue-green`.
For `apply`, `--dry-run` is a kubectl-style optional-value flag: using
`--dry-run` without `=client` or `=server` parses as `unchanged`, which kubectl
treats as client-side dry-run with a deprecation warning.
`--validate` also accepts an omitted value and treats it as `strict`.

## `app2kube build`

Builds a Docker image from a Dockerfile and optionally pushes it.

Usage:

```text
app2kube build [flags]
```

Includes the common application value flags except `--include-namespace`, which
is hidden because it has no build effect.

| Flag | Type | Description | Default |
| --- | --- | --- | --- |
| `--build-arg` | list | Set Docker build-time variables. May be repeated. | empty |
| `--docker-username` | string | Registry username. If omitted, `APP2KUBE_DOCKER_USERNAME` is used. | empty |
| `--file` | string | Dockerfile path. Use `-` to read the Dockerfile from stdin. | Docker default, `PATH/Dockerfile` |
| `--label` | list | Set image metadata labels. May be repeated. | empty |
| `--no-cache` | bool | Do not use the Docker build cache. | `false` |
| `--password-stdin` | bool | Read the registry password from stdin. Requires `--docker-username` or `APP2KUBE_DOCKER_USERNAME`; cannot be combined with `--file -`. | `false` |
| `--platform` | string | Build platform, for example `linux/amd64`. | empty |
| `--pull` | bool | Always attempt to pull a newer base image. | `false` |
| `--push` | bool | Push built image tags to the registry. | `false` |
| `-t, --tag` | list | Additional image name and optional tag in `name:tag` format. May be repeated. | empty |
| `--target` | string | Dockerfile build stage to build. | empty |

The primary image name comes from `common.image.repository` and
`common.image.tag`; if those are missing and exactly one deployment container is
configured, that container image is used.

For `--push`, app2kube resolves registry credentials in this order:
`--docker-username` or `APP2KUBE_DOCKER_USERNAME` plus
`APP2KUBE_DOCKER_PASSWORD`, then Docker's saved credentials from `docker login`.
If no credentials are found, app2kube warns and pushes unauthenticated.

## `app2kube delete`

Deletes generated application resources from Kubernetes.

Usage:

```text
app2kube delete [flags]
app2kube delete all [flags]
```

Includes the common application value flags.

| Flag | Type | Description | Default |
| --- | --- | --- | --- |
| `--all-instances` | bool | With `delete all`, delete all instances of the application by omitting the instance label from the selector. | `false` |
| `--blue-green` | bool | Resolve the blue/green target color before deleting generated resources. | `false` |
| `--dry-run` | string | Must be `none`, `server`, or `client`. With `client`, print the object without sending it; with `server`, submit the request without persisting it. | `none` |
| `--ignore-not-found` | bool | Treat "resource not found" as a successful delete. | `false` |
| `--wait` | bool | Wait for resources to be gone before returning, including finalizers. | `true` |

With no positional argument, app2kube deletes the exact generated manifest.
With `all`, it deletes all app2kube-generated resource kinds for the app using
a safe label selector. `--include-namespace` deletes the Namespace itself and
cannot be combined with `delete all`.

## `app2kube status`

Shows application resource status in Kubernetes.

Usage:

```text
app2kube status [flags]
```

Includes the common application value flags except `--include-namespace`, which
is hidden.

| Flag | Type | Description | Default |
| --- | --- | --- | --- |
| `--all` | bool | Show all applications managed by app2kube. | `false` |
| `--all-instances` | bool | Show all instances of the application by omitting the instance label from the selector. | `false` |

## `app2kube track`

Tracks Deployment rollout status.

Parent usage:

```text
app2kube track [command]
```

Subcommands:

```text
app2kube track follow [flags]
app2kube track ready [flags]
```

`track follow` follows Deployment progress and logs. `track ready` waits until
the Deployment is ready.

The parent flags below are inherited by `track follow` and `track ready`.

| Flag | Type | Description | Default |
| --- | --- | --- | --- |
| `-l, --logs-since` | string | Start log records from this point. Use a duration such as `30s`, `5m`, or `2h`, or use `all` or `now`. | `now` |
| `-t, --timeout` | int | Timeout in minutes; `0` waits forever. | `15` |

Both subcommands include the common application value flags except
`--include-namespace`, which is hidden.

| Flag | Type | Description | Default |
| --- | --- | --- | --- |
| `--blue-green` | bool | Track the Deployment for the resolved blue/green target color. | `false` |

## `app2kube blue-green`

Manages blue/green deployment state.

Parent usage:

```text
app2kube blue-green [command]
```

The parent command has no blue/green-specific flags.

### `app2kube blue-green color`

Prints the current live Deployment color.

Usage:

```text
app2kube blue-green color [flags]
```

Includes the common application value flags except `--include-namespace`, which
is hidden.

### `app2kube blue-green prune`

Deletes the previous-color Deployment and its matching PodDisruptionBudget when
present.

Usage:

```text
app2kube blue-green prune [flags]
```

Includes the common application value flags except `--include-namespace`, which
is hidden.

### `app2kube blue-green rollback`

Switches Services back to the previous color after verifying that the previous
Deployment is ready.

Usage:

```text
app2kube blue-green rollback [flags]
```

Includes the common application value flags except `--include-namespace`, which
is hidden.

| Flag | Type | Description | Default |
| --- | --- | --- | --- |
| `-t, --timeout` | int | Timeout in minutes while waiting for the previous-color Deployment; `0` waits forever. | `15` |

## `app2kube config`

Prints or mutates application configuration.

Parent usage:

```text
app2kube config [command]
```

The parent command has no config-specific flags.

### `app2kube config dotenv`

Prints ConfigMap, env, and decrypted Secret values in `.env` format.

Usage:

```text
app2kube config dotenv [flags]
```

Includes the common application value flags except `--include-namespace`, which
is hidden.

| Flag | Type | Description | Default |
| --- | --- | --- | --- |
| `-e, --export` | bool | Prefix each output line with `export `. | `false` |
| `-q, --quotes` | bool | Wrap output values in double quotes. | `false` |

### `app2kube config domain`

Prints the sorted list of ingress hosts and aliases.

Usage:

```text
app2kube config domain [flags]
```

Includes the common application value flags except `--include-namespace`, which
is hidden.

### `app2kube config secrets`

Prints decrypted secret values.

Usage:

```text
app2kube config secrets [flags]
```

Includes the common application value flags except `--include-namespace`, which
is hidden.

### `app2kube config encrypt`

Encrypts one string or encrypts the `secrets:` sections of YAML files in place.

Usage:

```text
app2kube config encrypt [flags]
```

| Flag | Type | Description | Default |
| --- | --- | --- | --- |
| `--string` | string | Encrypt the given string and print the encrypted value. Use `-` to read the string from stdin. | empty |
| `-f, --values` | valueFiles | YAML files whose `secrets:` values should be encrypted in place. May be repeated. | `[]` |

Encryption uses `APP2KUBE_PASSWORD` for AES or `APP2KUBE_ENCRYPT_KEY` for RSA.
RSA has priority when both are set. Files modified by this command are written
with mode `0600`.

### `app2kube config generate-keys`

Generates RSA-2048 encryption and decryption keys and prints export statements.

Usage:

```text
app2kube config generate-keys [flags]
```

This command has no local flags beyond `--help` and the global Kubernetes flags.

## `app2kube completion`

Generates a shell completion script.

Usage:

```text
app2kube completion [bash|zsh|fish|powershell]
```

This command has no local flags beyond `--help` and the global Kubernetes flags.

The required argument selects the target shell: `bash`, `zsh`, `fish`, or
`powershell`.

## `app2kube help`

Cobra provides the generated help command:

```text
app2kube help [command]
```

This command has no app2kube-specific flags beyond inherited global flags and
standard help behavior.
