"""Regenerate the deploymentpatch golden vectors from canonical Truss.

This script is the provenance for two committed goldens (both next to it):

``patchpoint_golden.json`` - from ``patchpoint_cases.json``: materializes each
single-state case and runs Truss's real signature code to produce ``content_hashes``,
verbatim ``config``, and resolved ``requirements``. The Go test asserts
``BuildPatchPoint`` reproduces these.

``patchop_golden.json`` - from ``patchop_cases.json``: each case has a ``prev``
and a ``next`` source tree. We compute the ``prev`` signature, then run Truss's
real ``calc_truss_patch`` (the SAME logic the model container applies patches
with) to get the ops that take ``prev`` to ``next``, and render them in the REST
patch-op shape. The Go test asserts ``BuildPatchOps`` reproduces these.

The Go tests never run Python - they materialize the same files and compare to
these committed values.

The ignore patterns are resolved exactly as the Truss watch client resolves
them (load_trussignore_patterns_from_truss_dir: the case's .truss_ignore if
present, else Truss's bundled defaults), so the golden reflects what a real
``truss watch`` would hash.

The patch-op golden renders Truss's ``calc_truss_patch`` output into the REST
shape ``BuildPatchOps`` emits. Truss still decides every op (which paths, which
action, requirement names, file contents, env/external/requirement diffs); only
the wire shape is adapted, in two documented spots:
  - env-var ops are flattened from Truss's ``item: {name: value}`` to the REST
    ``{name, value}`` (the server flattens them this way);
  - the config op carries the parsed-YAML config map (the shape ``baseten model
    push`` sends), not Truss's normalized ``to_dict()``. These are equivalent
    for the container applier, which does ``TrussConfig.from_dict(...)`` either
    way (from_dict(parsed_map) == from_dict(to_dict(cfg))).
A ``calc_truss_patch`` None (un-patchable change) is rendered as
``{"needs_full_deploy": true}``.

Run it from anywhere with an interpreter that has ``truss`` installed, e.g.::

    uv run --project internal/deploymentpatch/fixturegen \\
        internal/deploymentpatch/fixturegen/generate.py
"""

import base64
import json
import pathlib
import tempfile

import yaml
from truss.truss_handle.patch.calc_patch import calc_truss_patch
from truss.truss_handle.patch.signature import calc_truss_signature
from truss.util.path import load_trussignore_patterns_from_truss_dir

HERE = pathlib.Path(__file__).resolve().parent
CASES_PATH = HERE / "patchpoint_cases.json"
GOLDEN_PATH = HERE / "patchpoint_golden.json"
PATCHOP_CASES_PATH = HERE / "patchop_cases.json"
PATCHOP_GOLDEN_PATH = HERE / "patchop_golden.json"

CONFIG_FILE_NAME = "config.yaml"


def materialize(case: dict, root: pathlib.Path) -> None:
    for rel, content in case.get("files", {}).items():
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
    for rel, b64 in case.get("binary_files", {}).items():
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(base64.b64decode(b64))
    for rel in case.get("empty_dirs", []):
        (root / rel).mkdir(parents=True, exist_ok=True)


def compute_golden(case: dict) -> dict:
    with tempfile.TemporaryDirectory() as tmp:
        root = pathlib.Path(tmp)
        materialize(case, root)

        patterns = load_trussignore_patterns_from_truss_dir(root)
        signature = calc_truss_signature(root, patterns)

        return {
            "name": case["name"],
            "content_hashes": signature.content_hashes_by_path,
            "config": signature.config,
            "requirements": signature.requirements_file_requirements,
        }


def _render_patch_op(patch: object, next_config_map: dict) -> dict:
    """Render one Truss Patch into the REST patch-op shape BuildPatchOps emits.

    Truss has already decided the op; we only adapt the wire shape (see module
    docstring). Optional fields absent on the REST struct (null content, unset
    hot_reload, config path) are omitted so this matches the Go struct's
    ``omitempty`` JSON exactly.
    """
    body = patch.body.to_dict()
    patch_type = patch.type.value

    if patch_type == "model_code" or patch_type == "package":
        op = {"type": patch_type, "action": body["action"], "path": body["path"]}
        if body.get("content") is not None:
            op["content"] = body["content"]
        if body.get("content_bytes") is not None:
            op["content_bytes"] = body["content_bytes"]
        return op
    if patch_type == "python_requirement":
        return {
            "type": patch_type,
            "action": body["action"],
            "requirement": body["requirement"],
        }
    if patch_type == "config":
        # The REST config op carries the parsed config map (push shape), not
        # Truss's normalized to_dict(); see module docstring.
        return {"type": patch_type, "config": next_config_map}
    if patch_type == "environment_variable":
        # Flatten Truss's item {name: value} into the REST {name, value}.
        (name, value), = body["item"].items()
        return {"type": patch_type, "action": body["action"], "name": name, "value": value}
    if patch_type == "external_data":
        return {"type": patch_type, "action": body["action"], "item": body["item"]}
    raise ValueError(f"unhandled patch type: {patch_type}")


def compute_patchop_golden(case: dict) -> dict:
    with tempfile.TemporaryDirectory() as prev_tmp, tempfile.TemporaryDirectory() as next_tmp:
        prev_root = pathlib.Path(prev_tmp)
        next_root = pathlib.Path(next_tmp)
        materialize(case["prev"], prev_root)
        materialize(case["next"], next_root)

        # Same ignore basis as BuildPatchPoint on each side; cases keep
        # .truss_ignore identical across prev/next so the diff is consistent.
        prev_patterns = load_trussignore_patterns_from_truss_dir(prev_root)
        next_patterns = load_trussignore_patterns_from_truss_dir(next_root)
        prev_signature = calc_truss_signature(prev_root, prev_patterns)

        patches = calc_truss_patch(next_root, prev_signature, next_patterns)
        if patches is None:
            return {"name": case["name"], "needs_full_deploy": True}

        next_config_map = yaml.safe_load(
            (next_root / CONFIG_FILE_NAME).read_text()
        ) or {}
        ops = [_render_patch_op(p, next_config_map) for p in patches]
        return {"name": case["name"], "needs_full_deploy": False, "ops": ops}


def main() -> None:
    cases = json.loads(CASES_PATH.read_text())
    golden = [compute_golden(case) for case in cases]
    GOLDEN_PATH.write_text(json.dumps(golden, indent=2, sort_keys=True) + "\n")
    print(f"wrote {len(golden)} cases to {GOLDEN_PATH}")

    patchop_cases = json.loads(PATCHOP_CASES_PATH.read_text())
    patchop_golden = [compute_patchop_golden(case) for case in patchop_cases]
    PATCHOP_GOLDEN_PATH.write_text(
        json.dumps(patchop_golden, indent=2, sort_keys=True) + "\n"
    )
    print(f"wrote {len(patchop_golden)} patch-op cases to {PATCHOP_GOLDEN_PATH}")


if __name__ == "__main__":
    main()
