"""Validate the HTTP fixtures against the pinned backend OpenAPI extract.

Run: uv run --with jsonschema==4.26.0 internal/cmd/testdata/routes/validate.py
Only validates local JSON; does not contact a Baseten API.
"""
import json
from pathlib import Path

from jsonschema import Draft202012Validator

ROOT = Path(__file__).parent
spec = json.loads((ROOT / "openapi.json").read_text())
cases = json.loads((ROOT / "cases.json").read_text())


def validate(schema_name, value):
    schema = {
        "$ref": f"#/components/schemas/{schema_name}",
        "components": spec["components"],
    }
    Draft202012Validator(schema).validate(value)


for case in cases:
    validate("CreateRouteRequestV1", case["create"])
    validate("UpdateRouteRequestV1", case["update"])
    validate("RouteV1", case["response"])
    validate("RoutesResponseV1", {
        "items": [case["response"]],
        "pagination": {"has_more": True, "cursor": "opaque-fixture-cursor"},
    })
    validate("RouteTombstoneV1", {
        "id": case["response"]["id"], "name": case["response"]["name"],
    })
print(f"Validated create, update, describe, list, and delete fixtures for {len(cases)} target variants.")
