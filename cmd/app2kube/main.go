package main

import (
	"os"

	"github.com/n0madic/app2kube/pkg/cmd"
)

var version = "DEV"

func main() {
	if err := cmd.Execute(version); err != nil {
		os.Exit(1)
	}
}
