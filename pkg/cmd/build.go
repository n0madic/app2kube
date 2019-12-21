package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/cli/cli/command/image/build"
	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/opts"
	"github.com/docker/distribution/reference"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/archive"
	"github.com/docker/docker/pkg/idtools"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/docker/docker/pkg/term"
	"github.com/docker/docker/registry"
	"github.com/spf13/cobra"
)

var (
	buildArgs      opts.ListOpts
	dockerfileName string
	flagPassStdin  bool
	flagPull       bool
	flagPush       bool
	tags           opts.ListOpts
)

// NewCmdBuild return build command
func NewCmdBuild() *cobra.Command {
	buildCmd := &cobra.Command{
		Use:   "build",
		Short: "Build and push an image from a Dockerfile",
		RunE:  runBuild,
	}

	addAppFlags(buildCmd)

	buildArgs = opts.NewListOpts(opts.ValidateEnv)
	tags = opts.NewListOpts(func(rawRepo string) (string, error) {
		_, err := reference.ParseNormalizedNamed(rawRepo)
		if err != nil {
			return "", err
		}
		return rawRepo, nil
	})

	buildCmd.Flags().VarP(&buildArgs, "build-arg", "", "Set build-time variables")
	buildCmd.Flags().StringVarP(&dockerfileName, "file", "", "Dockerfile", "Name of the Dockerfile")
	buildCmd.Flags().BoolVar(&flagPassStdin, "password-stdin", false, "Take the docker password from stdin")
	buildCmd.Flags().BoolVar(&flagPull, "pull", false, "Always attempt to pull a newer version of the image")
	buildCmd.Flags().BoolVarP(&flagPush, "push", "", false, "Push an image to a registry")
	buildCmd.Flags().VarP(&tags, "tag", "t", "Additional name and optionally a tag in the 'name:tag' format")

	buildCmd.Flags().MarkHidden("include-namespace")

	return buildCmd
}

func runBuild(cmd *cobra.Command, args []string) error {
	app, err := initApp()
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

	cli, err := client.NewClientWithOpts(client.WithAPIVersionNegotiation(), client.FromEnv)
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

	configFile, err := config.Load(config.Dir())
	if err != nil {
		return fmt.Errorf("error loading Docker config file: %v", err)
	}

	creds, err := configFile.GetAllCredentials()
	if err != nil {
		return err
	}
	authConfigs := make(map[string]types.AuthConfig, len(creds))
	for k, auth := range creds {
		authConfigs[k] = types.AuthConfig(auth)
	}

	var registryAuth types.AuthConfig
	var password string

	username := *kubeConfigFlags.AuthInfoName
	if username == "" {
		username = os.Getenv("APP2KUBE_DOCKER_USERNAME")
	}

	if username != "" {
		if flagPassStdin {
			bytes, err := ioutil.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			password = string(bytes)
		} else {
			password = os.Getenv("APP2KUBE_DOCKER_PASSWORD")
			if password == "" {
				return fmt.Errorf("not specified $APP2KUBE_DOCKER_PASSWORD")
			}
		}
	}

	if username != "" && password != "" {
		registryAuth = types.AuthConfig{
			Username:      username,
			Password:      password,
			ServerAddress: info.Index.Name,
		}
		authConfigs[info.Index.Name] = types.AuthConfig(registryAuth)
	} else {
		authConfigKey := registry.GetAuthConfigKey(info.Index)

		auth, err := configFile.GetAuthConfig(authConfigKey)
		if err == nil {
			registryAuth = types.AuthConfig(auth)
		}
	}

	var dockerfileCtx io.ReadCloser

	if dockerfileName == "-" {
		dockerfileCtx = os.Stdin
	}

	buildContext := "."
	if len(args) == 1 {
		buildContext = args[0]
	}

	contextDir, relDockerfile, err := build.GetContextFromLocalDir(buildContext, dockerfileName)
	if err == nil && strings.HasPrefix(relDockerfile, ".."+string(filepath.Separator)) {
		// Dockerfile is outside of build-context; read the Dockerfile and pass it as dockerfileCtx
		dockerfileCtx, err = os.Open(dockerfileName)
		if err != nil {
			return fmt.Errorf("unable to open Dockerfile: %v", err)
		}
		defer dockerfileCtx.Close()
	}

	if err != nil {
		return fmt.Errorf("unable to prepare context: %s", err)
	}

	// read from a directory into tar archive
	excludes, err := build.ReadDockerignore(contextDir)
	if err != nil {
		return err
	}

	// exclude git stuff
	excludes = append(excludes, ".git*", "*/.git*")

	if err := build.ValidateContextDirectory(contextDir, excludes); err != nil {
		return fmt.Errorf("error checking context: '%s'", err)
	}

	// canonicalize dockerfile name to a platform-independent one
	relDockerfile = archive.CanonicalTarNameForPath(relDockerfile)

	excludes = build.TrimBuildFilesFromExcludes(excludes, relDockerfile, false)
	buildCtx, err := archive.TarWithOptions(contextDir, &archive.TarOptions{
		ExcludePatterns: excludes,
		ChownOpts:       &idtools.Identity{UID: 0, GID: 0},
	})
	if err != nil {
		return err
	}

	// replace Dockerfile if it was added from stdin or a file outside the build-context, and there is archive context
	if dockerfileCtx != nil && buildCtx != nil {
		buildCtx, relDockerfile, err = build.AddDockerfileToBuildContext(dockerfileCtx, buildCtx)
		if err != nil {
			return err
		}
	}

	if !tags.Get(imageName) {
		err = tags.Set(imageName)
		if err != nil {
			return err
		}
	}

	resp, err := cli.ImageBuild(context.Background(), buildCtx, types.ImageBuildOptions{
		AuthConfigs:    authConfigs,
		BuildArgs:      configFile.ParseProxyConfig(cli.DaemonHost(), opts.ConvertKVStringsToMapWithNil(buildArgs.GetAll())),
		Dockerfile:     relDockerfile,
		PullParent:     flagPull,
		Remove:         true,
		SuppressOutput: false,
		Tags:           tags.GetAll(),
		Version:        types.BuilderV1,
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
		for _, tag := range tags.GetAll() {
			fmt.Printf("\nPush image %s to registry\n", tag)

			res, err := cli.ImagePush(context.Background(), tag, types.ImagePushOptions{
				RegistryAuth: encodeAuthToBase64(registryAuth),
			})
			if err != nil {
				return fmt.Errorf("Docker image push error: %s", err)
			}

			err = jsonmessage.DisplayJSONMessagesStream(res, os.Stdout, fd, isTerminal, nil)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func encodeAuthToBase64(authConfig types.AuthConfig) string {
	buf, _ := json.Marshal(authConfig)
	return base64.URLEncoding.EncodeToString(buf)
}
