# Baseten harness setup

The `harness` group is hidden from root help; `baseten harness` lists its
`setup`, `status`, and `teardown` commands.
Validated CLI adapters on macOS: Claude Code 2.1.272, Codex 0.134.0, and
OpenCode 1.18.31 (V1 config). Other versions and platforms fail setup rather
than guessing compatibility. Status and teardown remain available after an
upgrade or executable removal. Desktop apps and launch integration are out of scope.

## Setup

Run `baseten harness setup` after logging in. Setup detects installed harnesses
and shows their versions and config paths in the selection list. Missing or
unsupported installations are listed separately and cannot be selected.
Use Space to toggle, Enter to confirm, and Ctrl+A to select all supported entries.
Select one or more harnesses,
a team when there is more than one, and an initial Route for each harness.
Setup uses each harness's native configuration path, displays the setting names
and paths it will change, then asks for confirmation. `--config` overrides the
path for a single harness. Existing ownership and conflict protections apply.

For scripts:

```sh
baseten harness setup --harness claude-code --team <team> \
  --model <route-name> --yes
```

Use `--dry-run` to validate and preview without creating a key or writing files.
Setup lists the selected team's accessible Routes through `GET /v1/routes`,
following cursor pagination, then joins Route names with the standard inference
`GET /v1/models` response at the returned invoke URL. It reuses the CLI login
transport for discovery and creates or reuses a separate Routes key for the
installed harness configurations. Validation and confirmation precede minting.
The setup command sends no inference requests.

The standard model catalog supplies `context_length`, `max_completion_tokens`,
`input_modalities`, and `supported_features`. The CLI does not infer these from
Route names or targets. Routes without usable metadata are reported and omitted;
currently this includes provider-backed Routes missing from the standard catalog.
Reasoning-effort levels and parallel-tool support are not advertised without
metadata. Gateway protocol selection follows the native adapter: Messages for
Claude, Responses for Codex, and Chat Completions for OpenCode.

The proposed Routes API is a deployment dependency. A missing API, denied
request, empty catalog, or unusable metadata produces an actionable error before
key creation or config writes. No fixture fallback exists in the CLI.

## Local tests

Fixtures are internal adapter test inputs only. Native probes use a test-only
executable, isolated config directories, dummy credentials, and loopback servers:

```sh
go build -o /tmp/harness-fixture ./internal/harness/testdata/fixture_setup
python3 internal/harness/testdata/claude_probe.py /tmp/harness-fixture
python3 internal/harness/testdata/codex_opencode_probe.py /tmp/harness-fixture
```

The CLI setup flow is tested separately through mock Routes, models, and key
creation APIs and an in-memory keyring. Tests never mint real credentials.

## Configuration and ownership

- `Catalog.Routes` returns normalized internal Route names, labels, and explicit
  protocol/tool compatibility and adapter-required metadata. `FixtureCatalog` is used only by tests.
  Its JSON is test input, not the Routes REST API response contract. Production setup normalizes the two API responses into the same internal Route type. Unknown
  capabilities fail closed. No model target or alias is used as local identity.
- Setup generates `modelPicker.options` and `availableModels`, plus initial,
  background and fallback settings. Secondary defaults use the explicit
  `--model`; subagent overrides are written only when explicitly selected. Setting the same fallback as the primary
  does not provide an independent fallback. Every selection must be in the
  catalog. Rerun setup and restart Claude after Route additions/removals.
  A target change behind an unchanged name requires no local identity change.
- Existing custom picker entries and allowlist entries are preserved.
  `--replace-picker` hides built-in picker rows; it still preserves custom user
  entries. The integration does not enforce that only Routes can be selected.
  It does not set persistent `ANTHROPIC_MODEL`, which would pin selection above
  the ordinary `model` setting. Gateway startup discovery is disabled.
- Conflicting settings are refused unless `--replace-existing` explicitly
  permits replacement. The original values are backed up. Settings already
  equal to the desired values stay user-owned. Authentication/provider
  overrides and known policy files require explicit resolution, even with
  `--replace-existing`.
- A private `settings.json.baseten-harness.json` sidecar records original bytes
  and per-setting ownership, beside the resolved config target. Setup uses
  private files, preserves symlinks, detects concurrent edits, and serializes
  cooperating mutations by resolved path. Previews and status never create
  directories, lock files, credentials, or other state. Reports omit values.
- Setup journals before replacing the settings file. A pending journal records
  both previous and desired values so interrupted initial setup or refresh can
  be recovered. Teardown restores only unchanged managed values, preserves
  later user edits, reports conflicts with a nonzero exit, and retains their
  backups. Unedited originals are restored byte for byte; new empty configs
  are removed. Existing JSON formatting is normalized while configured.
- Ownership of picker/allowlist arrays is at the whole-setting level. A later
  edit anywhere in an owned array blocks refresh and preserves that entire
  array on teardown. This intentionally avoids silently deleting user entries.
- There is no filesystem-wide transaction. A non-cooperating writer can still
  race the final check/rename. Retain the journal until teardown finishes.
  Backups may contain prior secrets and must remain private.

## Compatibility evidence and limits

Verified locally on 2026-09-16 with Claude Code 2.1.272 on macOS:

| Path | Observed model in local mock |
| --- | --- |
| Primary streamed response | `acme/primary` |
| Configured Haiku/background mapping via `--model haiku` | `acme/background` |
| Actual Agent tool call and subagent response | `acme/subagent` |
| Simulated 503 and configured fallback | `acme/fallback` |

No Route aliases were needed for those paths. Native Claude role variables map
to Route names; they do not create server aliases. The adapter does not set
`CLAUDE_CODE_SUBAGENT_MODEL_FORCE`: built-in inheritance, user agent definitions,
and per-invocation choices retain their native precedence. Advisor settings
are left alone. An explicit subagent selection sets the default only.
The local test consumes adapter-generated config, verifies idempotent setup, then
verifies teardown removes both the new settings and journal.

This does not certify production Tool Bank streaming/tools, token counting,
auto-mode classification, advisor execution, every hidden background call,
interactive picker rendering, or all environment/project/managed-policy
precedence. The normal setup command never sends an inference request. Arbitrary
Route IDs also lack Claude's built-in context/pricing metadata; reported usage
costs from a mock do not establish real costs or target context limits.

## Codex and OpenCode details

Codex writes a private local ModelInfo catalog beside the config and points
`model_catalog_json` at it. It uses Responses and keeps every Route's name as its
model ID. Catalog generation preserves the pinned version's native fallback
instructions, captured from a config-isolated localhost request without a custom
catalog, in `templates/codex-0.134.0-prompt.txt`. An empty `base_instructions`
was experimentally shown to remove the entire base prompt, so it is not used.
The integrated probe asserts byte-for-byte native instruction preservation and
uses app-server `model/list` to verify all generated Route IDs. Existing user
`model_instructions_file` and agent definitions are preserved. This preserves the
native generic fallback prompt, not model-specific tuning for an unknown target.

Context windows, input modalities, reasoning choices, and parallel-tool support
come from the normalized internal catalog. Production metadata comes from `/v1/models`; reasoning levels and parallel-tool support stay unspecified when absent. They are not inferred from Route names. Codex's catalog replaces built-ins because its native mechanism does so.
Review defaults to the primary Route; memory model defaults can be selected with
`--background-model`. Subagents inherit the primary Route unless a native role
already overrides it. A distinct `--subagent-model` fails explicitly for Codex
until a role-file adapter is validated. Existing agent configuration is retained.

OpenCode V1 adds a `baseten-harness` provider using Chat Completions. Its internal
`baseten-harness/<Route-name>` reference sends the unmodified Route name over the
wire. It installs all catalog entries, configures `small_model` for title work,
and changes general/explore agent defaults only with `--subagent-model`.
Other providers, agents, plugins and permissions are preserved. JSONC and V2
configurations fail explicitly. Neither Codex nor OpenCode gets an invented
fallback mechanism: `--fallback-model` is currently Claude-specific.

The additional native probe checks Codex catalog enumeration and streamed
inference, OpenCode model enumeration, primary inference, title generation,
and an actual task/subagent round trip. Multi-file Codex setup is preflighted;
its catalog is written before the referencing config. Interrupted writes retain
ownership journals for teardown. Native harness changes made while running are
preserved, so an originally absent config need not disappear after teardown.

## Create and save a real Routes key

Normal `baseten harness setup` now runs credential setup automatically:

```sh
baseten harness setup --profile <profile> --team <team> --key-name <name> \
  --harness claude-code --model <route-name> --yes
```

It authenticates through the existing CLI session, checks `/v1/users/me`, and
calls `POST /v1/api_keys` with `type: ROUTES`, `name`, and `team_id` without a creation confirmation. `--dry-run` previews setup without creating a key. Unlike the API's optional team selection,
this command selects the team automatically when only one is available, otherwise
prompts with available teams. `--team` accepts a name or ID for explicit selection.

The key is stored only in the system keyring under `baseten-harness-routes`.
There is no plaintext fallback, and neither text nor JSON output contains the
secret. It does not replace the CLI login credential. Reuse is scoped to the
management API URL, selected profile, authenticated user ID, team ID, and key
name. The default name is `baseten-harness-<normalized-hostname>`; changing `--key-name` selects
a different saved key. Reuse does not verify that a saved key is still valid.

A lock and pending keyring record prevent concurrent or ambiguous creation
attempts from automatically minting another key. After an uncertain request
or a failed save, review/revoke the named key in Baseten before removing the
pending keyring entry identified in the error and retrying. Definitive API
rejections such as permission denial clear the pending record. Do not remove
a lock while another setup process is running.

Normal setup validates the catalog and config plan, obtains confirmation, then
saves/reuses a Routes key and installs the selected configuration. Text and JSON
output show paths and setting names, never credential values. `--dry-run` never
creates a key or writes config files. `harness auth setup` remains available for
credential-only setup and uses `--name` for the same key label. Teardown restores
owned configuration and does not revoke a saved Routes key. Rotation and
revocation remain separate operations.

## Production dependencies

Requires the Routes REST API in backend PR #29394 and the standard gateway
`/v1/models` metadata contract. Local tests do not establish that either endpoint
is deployed for a particular organization. External-provider Route metadata and
live Tool Bank compatibility remain outside the local validation.

## Sources

- [New Routes/Harness proposal](https://app.notion.com/p/ml-infra/Baseten-Routes-Harness-CLI-API-Proposal-3db91d24727380ceaf43c2c9a80d8de3)
- [Older CLI context](https://app.notion.com/p/ml-infra/Tech-Spec-Baseten-Code-CLI-3d191d24727380288156f15d569c95c6)
- [Claude model configuration and fallback chains](https://code.claude.com/docs/en/model-config)
- [Claude subagent model selection and forcing](https://code.claude.com/docs/en/sub-agents)
- [Codex configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference)

Validation: `go test ./...`, both installed-harness loopback tests above, and
`git diff --check`. Unit tests cover planning, idempotence, refresh, restoration,
concurrent edits, interrupted refresh, symlinks, locks, permissions, drift,
version rejection, catalog validation, and output redaction. No live backend or
billable inference test has been run.
