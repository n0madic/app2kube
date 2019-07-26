# app2kube

The easiest way to create kubernetes manifests for an application

## Install

Download binaries from [release](https://github.com/n0madic/app2kube/releases) page.

Or install from source:

```shell
go get -u github.com/n0madic/app2kube/cmd/app2kube
```

## Help

```
Usage:
  app2kube [flags]

Flags:
  -h, --help                     help for app2kube
  -i, --ingress string           Ingress class (default "nginx")
  -n, --namespace string         Namespace used for manifests
      --set stringArray          Set values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)
      --set-file stringArray     Set values from respective files specified via the command line (can specify multiple or separate values with commas: key1=path1,key2=path2)
      --set-string stringArray   Set STRING values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)
  -s, --snapshot string          Save the merged YAML values in the specified file for reuse
  -f, --values valueFiles        Specify values in a YAML file or a URL (can specify multiple) (default [])
  -v, --verbose                  Show the merged YAML values as well
```
