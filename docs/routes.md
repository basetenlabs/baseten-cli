# Managing Routes (pre-release)

**Pre-release:** the entire `baseten route` feature is pre-release. Commands and
the API may change.

`baseten route` manages Routes through the unstable management API. Use the
existing `baseten auth login`, `BASETEN_API_KEY`, and `--profile` mechanisms.
The backend must have Routes enabled for your organization and an eligible name
prefix configured. Slugs are globally unique and must retain that prefix.

```sh
baseten route list --all
baseten route list --team Engineering --name acme/assistant
baseten route describe --name acme/assistant
baseten route describe --id <route-id> --output json

baseten route create --name acme/assistant --team Engineering \
  --target-model-api moonshotai/glm-5.3 --display-name Assistant

baseten route update --name acme/assistant --target-model-api moonshotai/glm-5.3
baseten route update --id <route-id> --display-name 'Team assistant'
baseten route delete --name acme/assistant --yes
```

`--team` accepts a team name or ID using the shared team resolver. Creation
requires a team, supplied via `--team` or selected interactively. Describe, update,
and delete accept exactly one of `--id` or `--name`, or prompt for a Route when
neither is supplied in a terminal. A supplied name is resolved with the API's
exact-name filter before addressing the stable ID. Slug and team ownership cannot be updated.

List returns one page by default, with `--limit` between 1 and 1000 (default
100). Use the returned `pagination.cursor` with `--cursor`, or `--all` to fetch
all remaining pages. `--limit` remains the per-request size with `--all`.
Cursors preserve filters, so you can omit `--team` and `--name` on continuation;
if repeated, they must match. Text output prints a continuation hint to stderr.
JSON and JSONL contain `{items, pagination}`; for example:

```sh
baseten route list --all --jq '.items[] | {id, name, team_id}'
```

## Interactive use

In a terminal, run a command without flags for guided input:

```sh
baseten route create
baseten route describe
baseten route update
baseten route delete
```

- Create shows a **team dropdown** populated from the API, with team names and
  IDs, then asks for the Route slug and target configuration. Target type and
  provider use dropdowns. Optional display-name and description prompts appear
  when their flags are absent. Press Enter to use the Route slug as the label
  and skip the description. Supplied `--display-name` and `--description` values
  skip these prompts, including an explicitly empty `--description ""`.
- External provider targets show a searchable secret-name picker for the
  Route's owning team. Choose **Create new secret** to enter a name (prefilled
  as `<provider>-api-key`) and a masked value. Existing names are rejected by
  this creation flow; select the existing secret instead. Explicit
  `--target-provider-secret` skips the picker. If listing fails, enter an
  existing secret name manually. A newly created secret remains in the team
  if the subsequent Route save fails.
- Model API targets use a dropdown of the full visible catalog. Press `/` to
  search, or choose manual entry. Empty or unavailable catalogs fall back to
  manual entry. Supplying `--target-model-api` skips the picker. This also
  applies when replacing a target through interactive update.
- The Route slug prompt prefills the organization prefix from the app API.
  Multiple prefixes use a dropdown. If discovery fails or returns no prefixes,
  enter the full name manually. An explicit `--name` skips prefix discovery.
- Describe, update, and delete show a dropdown of visible Routes when neither
  `--id` nor `--name` is supplied. The picker includes all API pages.
- Update asks whether to change the display name, replace the target, or both
  when no changes are supplied. Target replacement still requires a complete
  new target; no fields are inherited from the old target.
- Supplied flags skip their corresponding prompts. For example, `--team`
  skips the team dropdown, and a partially supplied provider target prompts
  only for its missing fields. Invalid or conflicting flags remain errors.
- Delete still requires confirmation unless `--yes` is passed. Ctrl+C or a
  declined confirmation stops before a mutation is sent.
- Prompts are written to stderr so JSON on stdout stays machine-readable.
  With nonterminal stdin, missing required inputs produce a usage error and
  deletion requires `--yes`. `list` has useful defaults and never prompts.

Text-mode `route describe` also resolves and displays the owning team name.
If that lookup is unavailable, Route details still display with the team ID.
Structured output retains the backend Route shape and does not perform this
additional lookup.

## External providers

`--target-provider` accepts `anthropic`, `openai`, `xai`, `vertex`, and
`openai-compatible`. Every provider requires `--target-provider-model` and
`--target-provider-secret`. The secret flag names an existing secret in the
Route's owning team; it does not accept or create a credential value.

```sh
baseten route create --team Engineering --name acme/assistant \
  --target-provider anthropic --target-provider-model claude-opus-5 \
  --target-provider-secret anthropic-key

baseten route update --name acme/assistant \
  --target-provider openai-compatible --target-provider-model upstream-model \
  --target-provider-secret upstream-key \
  --target-provider-base-url https://api.example.com/v1

baseten route update --name acme/assistant \
  --target-provider vertex --target-provider-model upstream-model \
  --target-provider-secret vertex-key \
  --target-provider-vertex-project my-gcp-project \
  --target-provider-vertex-location global
```

Base URL is required only for OpenAI-compatible targets. Vertex requires both
project and location. The backend additionally validates allowed hosts,
organization prefixes, model access, team/secret permissions, and Vertex
credentials. Vertex writes may cause the backend to mint a Google access token.

A target update replaces the whole target. Restate its head and all required
companions. A metadata-only update leaves the target untouched. Use `--description` to set
a description of up to 1000 characters, or `--description ""` to clear it. The CLI never
reads an old target and merges it with a partial update.

## Availability and current limitations

Implemented against [backend PR #29394](https://github.com/basetenlabs/baseten/pull/29394)
and its subsequent target-schema changes at
`c10f76e76799201c2036d4acedd021c1a29acb29`.
It requires the display-name migration, `ORG_ENABLE_ROUTES`, and an eligible
organization prefix. All five successful operations return HTTP 200.

- Lists and name lookups contain Routes the caller can invoke. A caller with
  management rights but no visibility may need to use the stable ID for update
  or delete. Creation/update/deletion require the backend's team management
  permissions; provider secrets also require access in the owning team.
- Target `type` is `BASETEN_MODEL_API` or the external provider name. The CLI
  uses that discriminator without a separate `provider` field. OpenAI-compatible
  targets return the full HTTPS `base_url`.
- The returned `invoke_url` is currently `https://coding.baseten.co`. The CLI
  displays the backend value and does not derive another hostname.
- Dedicated deployment targets, aliases, region selection, capability metadata,
  key lifecycle, and harness configuration are outside this API/command scope.
- Management success does not validate live inference or SEG propagation.

The CLI uses the existing management client's authentication, transport, and
error handling. A small local Route request helper and wire types bridge the
current SDK, which does not yet include these endpoints. They can be replaced
with generated SDK methods once available.
