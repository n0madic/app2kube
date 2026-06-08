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

	"github.com/containerd/platforms"
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
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/cobra"
)

var (
	buildArgs      opts.ListOpts
	buildLabels    opts.ListOpts
	buildPlatform  string
	buildTarget    string
	dockerUsername string
	dockerfileName string
	flagNoCache    bool
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
	buildLabels = opts.NewListOpts(opts.ValidateLabel)
	tags = opts.NewListOpts(func(rawRepo string) (string, error) {
		_, err := reference.ParseNormalizedNamed(rawRepo)
		if err != nil {
			return "", err
		}
		return rawRepo, nil
	})

	buildCmd.Flags().VarP(&buildArgs, "build-arg", "", "Set build-time variables")
	// No -f shorthand: addAppFlags already binds -f to --values (the Helm-style
	// values file, shared across every app command). docker/cli uses -f for the
	// Dockerfile, but app2kube's --values shorthand takes precedence here.
	buildCmd.Flags().StringVar(&dockerfileName, "file", "", `Name of the Dockerfile (Default is "PATH/Dockerfile")`)
	// Registry login is its own identity, independent of the kubeconfig --user
	// flag (AuthInfoName selects a Kubernetes auth-info, not a Docker account).
	buildCmd.Flags().StringVar(&dockerUsername, "docker-username", "", "Username for the image registry (or $APP2KUBE_DOCKER_USERNAME)")
	buildCmd.Flags().Var(&buildLabels, "label", "Set metadata for an image")
	buildCmd.Flags().BoolVar(&flagNoCache, "no-cache", false, "Do not use cache when building the image")
	buildCmd.Flags().BoolVar(&flagPassStdin, "password-stdin", false, "Take the docker password from stdin")
	// Default to "" (native build), NOT $DOCKER_DEFAULT_PLATFORM as docker/cli
	// does: app2kube pins the legacy builder (BuilderV1), which cannot build a
	// foreign platform on the host's native arch (the containerd image store
	// rejects the platform-mismatched intermediate images). Honoring the env
	// would silently force an unbuildable cross-arch build on e.g. an arm64 Mac
	// with DOCKER_DEFAULT_PLATFORM=linux/amd64. Pass --platform explicitly to opt in.
	buildCmd.Flags().StringVar(&buildPlatform, "platform", "", "Set platform if server is multi-platform capable")
	buildCmd.Flags().BoolVar(&flagPull, "pull", false, "Always attempt to pull a newer version of the image")
	buildCmd.Flags().BoolVarP(&flagPush, "push", "", false, "Push an image to a registry")
	buildCmd.Flags().StringVar(&buildTarget, "target", "", "Set the target build stage to build")
	buildCmd.Flags().VarP(&tags, "tag", "t", "Additional name and optionally a tag in the 'name:tag' format")

	_ = buildCmd.Flags().MarkHidden("include-namespace")

	return buildCmd
}

func runBuild(appOpts *appOptions, cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	// Silence the usage dump on every error from here on: a build/validation
	// failure is not a CLI-misuse error, so printing the full usage after it is
	// just noise (matches manifest/status/track).
	cmd.SilenceUsage = true

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

	// Validate --platform up front, before assembling the build context, so a
	// malformed value fails fast instead of after taring the context (matches
	// docker/cli, which parses the platform at the start of runBuild).
	buildPlatforms, err := parseBuildPlatforms(buildPlatform)
	if err != nil {
		return err
	}

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

	username := dockerUsername
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
		auth, err := configFile.GetAuthConfig(registryAuthKey(registryDomain))
		if err == nil {
			registryAuth = registrytypes.AuthConfig(auth)
		}
	}

	// A push with no resolved credentials would otherwise go out with an empty
	// auth blob and surface only as an opaque registry-side 401 buried in the
	// JSON stream. Warn up front so the unauthenticated state is visible (some
	// insecure/local registries legitimately need no auth, so this is not fatal).
	if flagPush && !hasRegistryAuth(registryAuth) {
		fmt.Fprintf(os.Stderr, "WARNING: no registry credentials resolved for %s; pushing unauthenticated. Run `docker login %s` or set --docker-username and $APP2KUBE_DOCKER_PASSWORD if the push fails.\n", registryDomain, registryDomain)
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
			return fmt.Errorf("unable to open Dockerfile: %w", err)
		}
		defer func() { _ = dockerfileCtx.Close() }()
	}

	if err != nil {
		return fmt.Errorf("unable to prepare context: %w", err)
	}

	// read from a directory into tar archive
	excludes, err := build.ReadDockerignore(contextDir)
	if err != nil {
		return err
	}

	// exclude git stuff
	excludes = append(excludes, ".git*", "*/.git*")

	if err := build.ValidateContextDirectory(contextDir, excludes); err != nil {
		return fmt.Errorf("error checking context: '%w'", err)
	}

	// canonicalize dockerfile name to a platform-independent one
	relDockerfile = filepath.ToSlash(relDockerfile)

	excludes = build.TrimBuildFilesFromExcludes(excludes, relDockerfile, dockerfileName == "-")
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
		Labels:         opts.ConvertKVStringsToMap(buildLabels.GetSlice()),
		NoCache:        flagNoCache,
		Platforms:      buildPlatforms,
		PullParent:     flagPull,
		Remove:         true,
		SuppressOutput: false,
		Tags:           tags.GetSlice(),
		Target:         buildTarget,
		Version:        buildtypes.BuilderV1,
	})
	if err != nil {
		return fmt.Errorf("docker image build error: %w", err)
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

// parseBuildPlatforms converts a --platform value to the slice the build API
// expects; an empty string yields no constraint. This mirrors docker/cli's
// classic (BuilderV1) build path, where the moby client maps the single element
// to the legacy "platform" query parameter (multi-platform builds require
// BuildKit and are rejected by the client).
func parseBuildPlatforms(platform string) ([]ocispec.Platform, error) {
	if platform == "" {
		return nil, nil
	}
	p, err := platforms.Parse(platform)
	if err != nil {
		return nil, fmt.Errorf("invalid platform: %w", err)
	}
	return []ocispec.Platform{p}, nil
}

// registryAuthKey maps an image's registry domain to the key its credentials
// are stored under in the Docker config. `docker login` to Docker Hub (and the
// "docker.io" domain that reference.Domain reports) saves the credential under
// the legacy index address "https://index.docker.io/v1/", so a plain GetAuthConfig
// lookup keyed on "docker.io" misses it and the push goes out unauthenticated.
// Translate Docker Hub to its index key; every other registry is keyed by domain.
func registryAuthKey(domain string) string {
	if domain == "docker.io" {
		return "https://index.docker.io/v1/"
	}
	return domain
}

func encodeAuthToBase64(authConfig registrytypes.AuthConfig) string {
	buf, _ := json.Marshal(authConfig)
	return base64.URLEncoding.EncodeToString(buf)
}

// hasRegistryAuth reports whether an AuthConfig carries any usable credential.
// ServerAddress alone does not count — only a username/password pair, a stored
// auth string, or a token authenticates a push.
func hasRegistryAuth(a registrytypes.AuthConfig) bool {
	return a.Username != "" || a.Password != "" || a.Auth != "" ||
		a.IdentityToken != "" || a.RegistryToken != ""
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
		return errors.New("--password-stdin requires a username (set --docker-username or $APP2KUBE_DOCKER_USERNAME)")
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
