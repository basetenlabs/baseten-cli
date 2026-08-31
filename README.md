# Baseten CLI

[![CI](https://github.com/basetenlabs/baseten-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/basetenlabs/baseten-cli/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/basetenlabs/baseten-cli)](https://github.com/basetenlabs/baseten-cli/releases/latest)
[![License](https://img.shields.io/github/license/basetenlabs/baseten-cli)](LICENSE)

CLI for [Baseten](https://baseten.co).

[CLI Reference Docs](https://docs.baseten.co/reference/cli/baseten/overview)

⚠️ Commands and flags may change between releases until this CLI reaches 1.0.

## Installation

### Homebrew (macOS and Linux)

```bash
# Add and trust the tap
brew tap basetenlabs/baseten
brew trust basetenlabs/baseten

# Install the CLI
brew install baseten

# Upgrade to the latest release
brew update
brew upgrade baseten
```

### Nix (macOS and Linux)

This repository is a Nix flake. Run the CLI without installing anything:

```bash
nix run github:basetenlabs/baseten-cli -- --help
```

or install it into your profile:

```bash
nix profile install github:basetenlabs/baseten-cli
```

To install with [Home Manager](https://github.com/nix-community/home-manager),
add the flake as an input and put the package in `home.packages`:

```nix
{
  inputs.baseten-cli.url = "github:basetenlabs/baseten-cli";

  # In your home-manager configuration (with `inputs` passed through):
  # home.packages = [ inputs.baseten-cli.packages.${pkgs.system}.default ];
}
```

The flake also exposes `overlays.default`, which adds the package as
`pkgs.baseten`.

### Prebuilt binaries

Download the [latest release](https://github.com/basetenlabs/baseten-cli/releases/latest) for Windows, macOS, or Linux, extract the archive, and place the `baseten` executable on your `PATH`.

<details>
<summary>Windows, macOS, and Linux one-liners</summary>

#### Windows (x64)

PowerShell:

    Invoke-WebRequest `
      https://github.com/basetenlabs/baseten-cli/releases/download/v0.3.0/baseten_0.3.0_windows_amd64.zip `
      -OutFile baseten.zip; Expand-Archive -Force baseten.zip .

Then move `baseten.exe` to a directory on your `PATH`.

#### macOS (arm64)

    curl -sL https://github.com/basetenlabs/baseten-cli/releases/download/v0.3.0/baseten_0.3.0_darwin_arm64.tar.gz \
      | sudo tar xz -C /usr/local/bin baseten

#### Linux (x64)

    curl -sL https://github.com/basetenlabs/baseten-cli/releases/download/v0.3.0/baseten_0.3.0_linux_amd64.tar.gz \
      | sudo tar xz -C /usr/local/bin baseten

</details>

## Usage

Authenticate via `baseten auth login`, or set `BASETEN_API_KEY` in the environment.

Run `baseten --help` (or `baseten <command> --help`) for the full command tree.

### Deploying Models

From inside a model directory containing a `config.yaml`:

    baseten model push

The directory defaults to the current working directory and is configurable via `--dir`. Useful flags:

- `--tail` streams build and runtime logs to stderr after the push completes.
- `--wait` blocks until the deployment reaches an active status and exits non-zero on terminal failure.

### Calling a Model

    baseten model predict --model-id <model-id> --data '{"prompt":"hello"}'

`--model-name` is also accepted. Pass `--file <path>` (or `--file -` for stdin) to send a request body from a file.

### Viewing Logs

    baseten model deployment logs --model-id <model-id> --deployment-id <deployment-id> --tail

Omit `--tail` and pass `--since 1h` (or `--start`/`--end`) to fetch a historical window.

Run `baseten --help` for more, and see [docs.baseten.co](https://docs.baseten.co) for general Baseten platform documentation.

## Building

To build from source, clone this repository and run:

    go build ./cmd/baseten

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines.
