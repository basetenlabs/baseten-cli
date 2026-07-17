package cmd

// commandModelImage groups the `baseten model image` subcommands, which build a
// local Docker image (or just its build context) from a model directory. Both
// subcommands shell out to the truss CLI via `uv` to generate the build context
// and, for `build`, to `docker` to build the image. They require no
// authentication and make no Baseten API calls.
var commandModelImage = Command{
	Name:    "image",
	Summary: "Build local Docker images from a model directory",
	Description: "Build a local Docker image, or just its Docker build context, from a " +
		"model directory.\n\n" +
		"These commands run entirely locally: they shell out to the truss CLI (via " +
		"'uv') to generate the build context and to 'docker' to build the image. They " +
		"need no authentication and make no Baseten API calls. 'uv' must be on PATH; " +
		"'build' also requires 'docker'.",
	Children: []Command{
		{
			Name:      "build",
			ArgsUsage: "[-- DOCKER_BUILD_ARGS...]",
			Summary:   "Build a local Docker image from a model directory",
			Description: "Generate the Docker build context for a model directory and build it " +
				"into a local Docker image.\n\n" +
				"The current directory is used by default; pass --dir to point at a model " +
				"directory elsewhere. The build context is written to a temporary directory " +
				"that is removed afterward unless --build-dir is given.\n\n" +
				"Any arguments after '--' are passed through verbatim to 'docker build'. If " +
				"you supply your own --iidfile there, it is honored and used to report the " +
				"image ID; otherwise one is injected internally.",
			MaxArgs: -1,
			Flags:   ModelImageBuildFlags{},
			Output: &CommandOutput[ModelImageBuildResult]{
				TextDescription: "Build progress from truss and docker is streamed to stderr. On " +
					"success a one-line summary is printed to stderr; no stdout output.",
				JSONDescription: "Under --output json, stdout is the {image_id, tag} result. " +
					"image_id is empty when it cannot be resolved (e.g. a custom buildx builder " +
					"that does not write the iidfile).",
				Examples: []CommandExample{
					{
						Description: "Build the current directory into a local Docker image.",
						Command:     "baseten model image build",
					},
					{
						Description: "Build another directory with a custom tag.",
						Command:     "baseten model image build --dir ./my-model --tag my-model:dev",
					},
					{
						Description: "Pass extra flags through to docker build.",
						Command:     "baseten model image build -- --no-cache --build-arg FOO=bar",
					},
				},
				JQExample: CommandExample{
					Description: "Print the built image ID.",
					Command:     "baseten model image build --jq '.image_id'",
				},
			},
		},
		{
			Name:      "prepare",
			ArgsUsage: "--build-dir DIR",
			Summary:   "Write a Docker build context from a model directory",
			Description: "Generate the Docker build context (Dockerfile and supporting files) for " +
				"a model directory into --build-dir, without building an image.\n\n" +
				"The current directory is used by default; pass --dir to point at a model " +
				"directory elsewhere. The resulting directory is self-contained and can be " +
				"built with 'docker build <build-dir>'.",
			Flags: ModelImagePrepareFlags{},
			Output: &CommandOutput[ModelImagePrepareResult]{
				TextDescription: "Progress from truss is streamed to stderr. On success a one-line " +
					"summary is printed to stderr; no stdout output.",
				JSONDescription: "Under --output json, stdout is the {build_dir, dockerfile} result " +
					"with absolute paths.",
				Examples: []CommandExample{
					{
						Description: "Write the current directory's build context to ./ctx.",
						Command:     "baseten model image prepare --build-dir ./ctx",
					},
				},
				JQExample: CommandExample{
					Description: "Print the generated Dockerfile path.",
					Command:     "baseten model image prepare --build-dir ./ctx --jq '.dockerfile'",
				},
			},
		},
	},
}

// ModelImageBuildResult is the JSON output of `baseten model image build`.
type ModelImageBuildResult struct {
	ImageID string `json:"image_id"`
	Tag     string `json:"tag"`
}

// ModelImagePrepareResult is the JSON output of `baseten model image prepare`.
type ModelImagePrepareResult struct {
	BuildDir   string `json:"build_dir"`
	Dockerfile string `json:"dockerfile"`
}

// ModelImageCommonFlags are the flags shared by both `baseten model image`
// subcommands.
type ModelImageCommonFlags struct {
	CommandFlags

	Dir string `flag:"dir" desc:"Model directory. Defaults to the current directory." default:"."`

	TrussVersion string `flag:"truss-version" desc:"Truss version to run via uv, e.g. '0.9.0' or 'latest'." default:"latest"`
}

// ModelImageBuildFlags configures `baseten model image build`.
type ModelImageBuildFlags struct {
	ModelImageCommonFlags

	BuildDir string `flag:"build-dir" desc:"Directory to write the Docker build context into. Defaults to a temporary directory that is removed after the build."`

	Tag string `flag:"tag" desc:"Image tag for 'docker build -t'. Defaults to '<model_name>:latest' from config.yaml."`
}

// ModelImagePrepareFlags configures `baseten model image prepare`.
type ModelImagePrepareFlags struct {
	ModelImageCommonFlags

	BuildDir string `flag:"build-dir" desc:"Directory to write the Docker build context into. Created if absent and kept after the command completes." required:"true"`
}
