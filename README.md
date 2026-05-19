# Baseten CLI

CLI for the [Baseten Inference Platform](https://baseten.co).

⚠️ Under active development. Nothing should be considered stable at this time.

## Installation

No release has been made/published yet, so you must build. Clone this repository and run:

    go build ./cmd/baseten

## Usage

Authenticate via `baseten auth login`, or set `BASETEN_API_KEY` in the environment.

Run `baseten --help` (or `baseten <command> --help`) for the full command tree.

### Deploying Models

From inside a model directory containing a `config.yaml`:

    baseten model push

The directory defaults to the current working directory and is configurable via `--dir`. Useful flags:

- `--tail` streams build and runtime logs to stderr after the push completes.
- `--wait` blocks until the deployment reaches an active status and exits non-zero on terminal failure.

See [docs.baseten.co](https://docs.baseten.co) for more.
