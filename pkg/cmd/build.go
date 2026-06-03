package cmd

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/distribution/reference"
	"github.com/docker/cli/cli/command"
	"github.com/docker/cli/cli/command/image/build"
	"github.com/docker/cli/cli/flags"
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
	}

	appOpts := addAppFlags(buildCmd)
	buildCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runBuild(appOpts, cmd, args)
	}

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

	_ = buildCmd.Flags().MarkHidden("include-namespace")

	return buildCmd
}

func runBuild(appOpts *appOptions, cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	app, err := appOpts.initApp(ctx)
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

	// Use docker/cli to create the API client so that the active Docker
	// context (and DOCKER_HOST) is respected, exactly like the docker CLI.
	// A bare client.FromEnv only reads DOCKER_HOST and otherwise falls back
	// to the default socket, ignoring contexts such as colima.
	dockerCli, err := command.NewDockerCli()
	if err != nil {
		return err
	}
	if err := dockerCli.Initialize(flags.NewClientOptions()); err != nil {
		return err
	}
	cli := dockerCli.Client()

	named, err := reference.ParseNormalizedNamed(imageName)
	if err != nil {
		return err
	}

	registryDomain := reference.Domain(named)

	configFile := dockerCli.ConfigFile()

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

	// --password-stdin and a Dockerfile from stdin both consume os.Stdin, and a
	// piped password is useless without a username; reject these up front (#41).
	if err := validatePasswordStdin(flagPassStdin, dockerfileName == "-", username); err != nil {
		return err
	}

	if username != "" {
		if flagPassStdin {
			password, err = readStdinSecret(os.Stdin)
			if err != nil {
				return err
			}
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
		defer func() { _ = dockerfileCtx.Close() }()
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

	result, err := cli.ImageBuild(ctx, buildCtx, client.ImageBuildOptions{
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
	defer func() { _ = result.Body.Close() }()

	fd, isTerminal := term.GetFdInfo(os.Stdout)

	err = jsonmessage.DisplayJSONMessagesStream(result.Body, os.Stdout, fd, isTerminal, nil)
	if err != nil {
		return err
	}

	if flagPush {
		// Push every tag, continuing past a failure so the operator learns which
		// tags did and did not reach the registry rather than seeing only the
		// first error (#56).
		err = pushTags(tags.GetSlice(), func(tag string) error {
			fmt.Printf("\nPush image %s to registry\n", tag)
			res, err := cli.ImagePush(ctx, tag, client.ImagePushOptions{
				RegistryAuth: encodeAuthToBase64(registryAuth),
			})
			if err != nil {
				return err
			}
			return jsonmessage.DisplayJSONMessagesStream(res, os.Stdout, fd, isTerminal, nil)
		})
		if err != nil {
			return fmt.Errorf("docker image push error: %w", err)
		}
	}
	return nil
}

func encodeAuthToBase64(authConfig registrytypes.AuthConfig) string {
	buf, _ := json.Marshal(authConfig)
	return base64.URLEncoding.EncodeToString(buf)
}

// maxStdinSecretBytes bounds a secret read from stdin (--password-stdin); a
// registry password is tiny, so 1 MiB is a generous cap that still prevents an
// unbounded read (DoS) from a misdirected/huge stdin (#41).
const maxStdinSecretBytes = 1 << 20

// validatePasswordStdin rejects flag combinations that misuse --password-stdin:
// it cannot share stdin with a Dockerfile read from stdin (--file -), and it is
// meaningless without a username (the password would be silently discarded)
// (#41).
func validatePasswordStdin(passStdin, dockerfileFromStdin bool, username string) error {
	if !passStdin {
		return nil
	}
	if dockerfileFromStdin {
		return errors.New("--password-stdin cannot be combined with --file - (both read stdin)")
	}
	if username == "" {
		return errors.New("--password-stdin requires a username (set --user or $APP2KUBE_DOCKER_USERNAME)")
	}
	return nil
}

// readStdinSecret reads a secret (e.g. a registry password) from r, bounded to
// maxStdinSecretBytes to avoid an unbounded read, and trims the trailing newline
// a pipe/heredoc adds so it does not corrupt authentication (#41).
func readStdinSecret(r io.Reader) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxStdinSecretBytes))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(raw), "\r\n"), nil
}

// pushTags pushes each tag via push, continuing past a failing tag and
// aggregating the errors, so a partial-push state (some tags already in the
// registry) is reported instead of being masked by the first error. Docker
// pushes are idempotent, so re-running after a partial failure is safe (#56).
func pushTags(tags []string, push func(tag string) error) error {
	var errs []error
	for _, tag := range tags {
		if err := push(tag); err != nil {
			errs = append(errs, fmt.Errorf("push %s: %w", tag, err))
		}
	}
	return errors.Join(errs...)
}
