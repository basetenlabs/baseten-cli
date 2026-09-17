# Routes HTTP contract fixtures

Source: backend master at
`c10f76e76799201c2036d4acedd021c1a29acb29`, including the target-union follow-up after
[baseten PR #29394](https://github.com/basetenlabs/baseten/pull/29394).

`openapi.json` contains the exact `/v1/routes` and `/v1/routes/{route_id}` paths
and their transitively referenced schemas and parameters from
`backend/rest_api/openapi_v1_spec.json.jinja` at that revision. Only the document
metadata is changed. No schema fields are synthesized from the proposal.

`cases.json` supplies synthetic requests and responses for all six target
variants: Model API, Anthropic, OpenAI, xAI, Vertex, and OpenAI-compatible.
`command.route_test.go` sends the requests through the actual command runner and
HTTP client to local fixture servers, compares complete request bodies, and
exercises response handling. No real credentials, Routes, or inference are used.

Validate the fixtures separately against the extracted OpenAPI:

```sh
uv run --with jsonschema==4.26.0 internal/cmd/testdata/routes/validate.py
```

The schema permits nullable optional fields even where runtime validators reject
explicit null, and provider cross-field constraints are enforced by backend
services. Command tests separately cover omission, empty labels, mutually
exclusive target flags, required companions, and provider-specific fields.

The target discriminator is the provider itself: `BASETEN_MODEL_API`,
`ANTHROPIC`, `OPENAI`, `XAI`, `VERTEX`, or `OPENAI_COMPATIBLE`. There is no
separate `provider` field. Only OpenAI-compatible targets carry `base_url`,
returned with its HTTPS scheme. Only Vertex targets carry `vertex_config`.
The legacy `MODEL_API` and `PROVIDER` discriminators are rejected.
