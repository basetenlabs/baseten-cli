# Baseten Code implementation boundary

This is internal developer documentation. All `code` commands, including every
group and leaf, are hidden in the declarative command tree. Explicit invocation
and explicit `--help` still work. Cobra 1.10.2 filters hidden commands from
completion, suggestions, Markdown docs and man pages. The tests cover the CLI's
help renderer, dynamic completion, generated scripts and both doc generators.
This checkout has no `cmd/docs` JSON exporter; any future exporter must also
filter `Command.Hidden` before descending.

## Available behavior

- `init --dry-run` detects harness executables/versions, consolidates Codex CLI
  and desktop, inspects supported config files, and previews paths and setting
  names. No sign-in, keyring write, config write, inference or restart occurs.
- `status` reports local installation and human-profile metadata, explicitly
  distinguishing a stored credential from verified authorization.
- The isolated Code credential store uses the CLI's existing keyring/fallback
  implementation. Ordinary profiles, OAuth tokens and `BASETEN_API_KEY` are
  never treated as Code credentials. `auth token` writes only the installed
  credential; all failures stay on stderr, including parser/formatting errors.
- With a provisioned installation, `models` reads `coding.baseten.co/v1/models`
  using its Code credential. It reports fetch time, accepts the existing `data`
  envelope, and never filters IDs or guesses capabilities from model names.
- Codex and Claude Code adapters configure the selected Route and the coding
  endpoint. Codex uses Responses and command-backed authentication. Claude uses
  Messages, `apiKeyHelper` and explicit secondary model roles. Helpers pin the
  absolute binary and Code state-directory paths, including for GUI launches.
  No web-search provider-priority header or permission/sandbox policy is written.
- Reinitialization of an existing installation validates catalog membership,
  streaming and a synthetic tool-result replay before writes. `doctor --test`
  exposes the same two-turn probe; ordinary doctor sends catalog reads only.
  Probes never execute model-generated commands. Only mocked protocols have
  been tested here, not live harness compatibility.
- A private journal saves original bytes and managed values before changes.
  File writes preserve existing permissions and symlinks, check identity and
  content for concurrent edits, and serialize CLI mutations with a lock.
  `teardown` restores only matching managed values, preserves later user edits,
  and retains conflicting backup entries with a nonzero exit. Newly created
  files are removed only when empty after restoration. Unedited original files
  are restored byte for byte. Modified TOML is normalized, which can remove
  comments until rollback; unrelated setting values are preserved.
- Interrupted multi-file setup can leave a partial installation. Its journal
  permits conservative recovery with `teardown`; this is not a filesystem-wide
  atomic transaction. Non-cooperating editors can race the final check/rename.

## Required follow-up contracts

1. **Code credentials:** the pinned `baseten-go` SDK at
   `7c00cba027b9` has no dedicated Code issuance/list/revoke operations. Neither
   the CLI spec nor auth spec publishes their paths or request/response schemas.
   Initial `init`, `keys list`, `keys revoke` and `logout` fail explicitly and
   make no guessed management requests. Logout retains credentials until
   confirmed revocation. No ordinary API-key management endpoint is substituted.
   Wire the existing OAuth device login/profile machinery to these contracts,
   including installation reuse, organization resolution and
   creator identity. M1 does not introduce a separate eligibility/team gate.
   There is intentionally no production command to seed local Code credentials.
2. **Spend:** the SDK has daily Cost API user/model/API-key-prefix filters, but
   Code-key inventory and creator attribution are not integrated. `spend`
   validates date and grouping syntax and fails instead of returning org totals,
   ordinary MAPI spend, or incomplete per-installation totals as user Code spend.
   Route attribution, freshness and month-to-date semantics need backend wiring.
3. **Catalog generation and live refresh:** final Route capability metadata and
   versioned harness catalog adapters are unresolved. `sync --dry-run` validates
   the selected Route and reports this gap; actual `sync` fails with no config
   writes. No init-time fetch is claimed as active-session refresh.
4. **OpenCode and Claude desktop:** recognized and diagnosed, but installation
   and probes fail explicitly. OpenCode needs verified V1/V2 credential delivery,
   model catalog generation and JSONC preservation. Claude desktop needs a
   version-checked profile installer, credential delivery and arbitrary Route
   discovery validation. No aliases disguising Routes as Claude are generated.
5. **Harness coverage:** minimum versions, desktop picker/profile behavior,
   effective project/launch overrides and managed MDM policy resolution remain
   validation work. Known local managed-policy files cause setup to stop;
   existing policy settings are never edited. Device login and real inference
   were not exercised while implementing this change.

Sources read on 2026-09-11:

- [CLI spec](https://app.notion.com/p/3d191d24727380288156f15d569c95c6)
- [Auth spec](https://app.notion.com/p/3d191d24727380838f94d2dece50341f)
- [Codex configuration](https://learn.chatgpt.com/docs/config-file/config-advanced)
- [Codex tool-bank guide](https://app.notion.com/p/3c191d24727381b381eaf08b984e994e)
- [Claude tool-bank guide](https://app.notion.com/p/3c291d2472738107a9e8d30fa540a7d4)
- [Protocol probes](https://app.notion.com/p/3cf91d2472738117987ed2d18342d4d5)
