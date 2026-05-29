package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/distribution/reference"
	"github.com/docker/cli/cli/command/image/build"
	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/opts"
	"github.com/moby/go-archive"
	buildtypes "github.com/moby/moby/api/types/build"
	registrytypes "github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/jsonmessage"
	"github.com/moby/term"
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
			return errors.New("requires common application image values (repository and tag)")
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

	registryDomain := reference.Domain(named)

	configFile, err := config.Load(config.Dir())
	if err != nil {
		return fmt.Errorf("error loading Docker config file: %v", err)
	}

	creds, err := configFile.GetAllCredentials()
	if err != nil {
		return err
	}
	authConfigs := make(map[string]registrytypes.AuthConfig, len(creds))
	for k, auth := range creds {
		authConfigs[k] = registrytypes.AuthConfig(auth)
	}

	var registryAuth registrytypes.AuthConfig
	var password string

	username := *kubeConfigFlags.AuthInfoName
	if username == "" {
		username = os.Getenv("APP2KUBE_DOCKER_USERNAME")
	}

	if username != "" {
		if flagPassStdin {
			bytes, err := io.ReadAll(os.Stdin)
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
		registryAuth = registrytypes.AuthConfig{
			Username:      username,
			Password:      password,
			ServerAddress: registryDomain,
		}
		authConfigs[registryDomain] = registryAuth
	} else {
		auth, err := configFile.GetAuthConfig(registryDomain)
		if err == nil {
			registryAuth = registrytypes.AuthConfig(auth)
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
	relDockerfile = filepath.ToSlash(relDockerfile)

	excludes = build.TrimBuildFilesFromExcludes(excludes, relDockerfile, false)
	buildCtx, err := archive.TarWithOptions(contextDir, &archive.TarOptions{
		ExcludePatterns: excludes,
		ChownOpts:       &archive.ChownOpts{UID: 0, GID: 0},
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

	result, err := cli.ImageBuild(context.Background(), buildCtx, client.ImageBuildOptions{
		AuthConfigs:    authConfigs,
		BuildArgs:      configFile.ParseProxyConfig(cli.DaemonHost(), opts.ConvertKVStringsToMapWithNil(buildArgs.GetSlice())),
		Dockerfile:     relDockerfile,
		PullParent:     flagPull,
		Remove:         true,
		SuppressOutput: false,
		Tags:           tags.GetSlice(),
		Version:        buildtypes.BuilderV1,
	})
	if err != nil {
		return fmt.Errorf("docker image build error: %s", err)
	}
	defer result.Body.Close()

	fd, isTerminal := term.GetFdInfo(os.Stdout)

	err = jsonmessage.DisplayJSONMessagesStream(result.Body, os.Stdout, fd, isTerminal, nil)
	if err != nil {
		return err
	}

	if flagPush {
		for _, tag := range tags.GetSlice() {
			fmt.Printf("\nPush image %s to registry\n", tag)

			res, err := cli.ImagePush(context.Background(), tag, client.ImagePushOptions{
				RegistryAuth: encodeAuthToBase64(registryAuth),
			})
			if err != nil {
				return fmt.Errorf("docker image push error: %s", err)
			}

			err = jsonmessage.DisplayJSONMessagesStream(res, os.Stdout, fd, isTerminal, nil)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func encodeAuthToBase64(authConfig registrytypes.AuthConfig) string {
	buf, _ := json.Marshal(authConfig)
	return base64.URLEncoding.EncodeToString(buf)
}
