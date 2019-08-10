package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/docker/cli/cli/config"
	"github.com/docker/distribution/reference"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/archive"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/docker/docker/pkg/term"
	"github.com/docker/docker/registry"
	"github.com/spf13/cobra"
)

var (
	buildArgs      []string
	buildContext   string
	dockerfileName string
	flagPull       bool
	flagPush       bool
)

func init() {
	buildCmd := &cobra.Command{
		Use:   "build",
		Short: "Build and push an image from a Dockerfile",
		RunE:  build,
	}

	addAppFlags(buildCmd)

	buildCmd.Flags().StringArrayVar(&buildArgs, "build-arg", []string{}, "Set build-time variables")
	buildCmd.Flags().StringVarP(&buildContext, "build-context", "", ".", "Path to the docker build context")
	buildCmd.Flags().StringVarP(&dockerfileName, "file", "", "Dockerfile", "Name of the Dockerfile")
	buildCmd.Flags().BoolVar(&flagPull, "pull", false, "Always attempt to pull a newer version of the image")
	buildCmd.Flags().BoolVarP(&flagPush, "push", "", false, "Push an image to a registry")

	buildCmd.Flags().MarkHidden("include-namespace")

	rootCmd.AddCommand(buildCmd)
}

func build(cmd *cobra.Command, args []string) error {
	err := initApp()
	if err != nil {
		return err
	}

	imageName := app.Common.Image.Repository + ":" + app.Common.Image.Tag

	if app.Common.Image.Repository == "" || app.Common.Image.Tag == "" {
		if len(app.Deployment.Containers) == 1 {
			for _, container := range app.Deployment.Containers {
				imageName = container.Image
			}
		} else {
			return errors.New("Requires common application image values (repository and tag)")
		}
	}

	cmd.SilenceUsage = true

	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return err
	}

	named, err := reference.ParseNormalizedNamed(imageName)
	if err != nil {
		return err
	}

	info, err := registry.ParseRepositoryInfo(named)
	if err != nil {
		return err
	}

	var registryAuth types.AuthConfig

	username, ok := os.LookupEnv("APP2KUBE_DOCKER_USERNAME")
	if ok {
		password, ok := os.LookupEnv("APP2KUBE_DOCKER_PASSWORD")
		if !ok {
			return fmt.Errorf("not specified $APP2KUBE_DOCKER_PASSWORD")
		}

		registryAuth = types.AuthConfig{
			Username:      username,
			Password:      password,
			ServerAddress: info.Index.Name,
		}
	} else {
		authConfigKey := registry.GetAuthConfigKey(info.Index)

		configFile, err := config.Load(config.Dir())
		if err != nil {
			return fmt.Errorf("error loading Docker config file: %v", err)
		}

		auth, err := configFile.GetAuthConfig(authConfigKey)
		if err == nil {
			registryAuth = types.AuthConfig(auth)
		}
	}

	registryAuthBase64 := encodeAuthToBase64(registryAuth)

	buildContext, err := archive.TarWithOptions(buildContext, &archive.TarOptions{})
	if err != nil {
		return fmt.Errorf("build context tar failed: %s", err)
	}

	resp, err := cli.ImageBuild(context.Background(), buildContext, types.ImageBuildOptions{
		BuildArgs:      listToMap(buildArgs),
		Dockerfile:     dockerfileName,
		PullParent:     flagPull,
		Remove:         true,
		SuppressOutput: false,
		Tags:           []string{imageName},
	})
	if err != nil {
		return fmt.Errorf("Docker image build error: %s", err)
	}
	defer resp.Body.Close()

	fd, isTerminal := term.GetFdInfo(os.Stdout)

	err = jsonmessage.DisplayJSONMessagesStream(resp.Body, os.Stdout, fd, isTerminal, nil)
	if err != nil {
		return err
	}

	if flagPush {
		fmt.Printf("\nPush image %s to registry\n", imageName)
		res, err := cli.ImagePush(context.Background(), imageName, types.ImagePushOptions{
			RegistryAuth: registryAuthBase64,
		})
		if err != nil {
			return fmt.Errorf("Docker image push error: %s", err)
		}

		err = jsonmessage.DisplayJSONMessagesStream(res, os.Stdout, fd, isTerminal, nil)
		if err != nil {
			return err
		}
	}
	return nil
}

func listToMap(values []string) map[string]*string {
	result := make(map[string]*string, len(values))
	for _, value := range values {
		kv := strings.SplitN(value, "=", 2)
		if len(kv) == 1 {
			env := os.Getenv(kv[0])
			result[kv[0]] = &env
		} else {
			result[kv[0]] = &kv[1]
		}
	}
	return result
}

func encodeAuthToBase64(authConfig types.AuthConfig) string {
	buf, _ := json.Marshal(authConfig)
	return base64.URLEncoding.EncodeToString(buf)
}
