#!/usr/bin/env python3
"""
update_image_tag: Update a container image tag in a Helm values.yaml.

Reads $PARAMS_PATH for:
  - image      (str, required) – image name to match, e.g. "nginx"
  - tag        (str, required) – new tag, e.g. "1.27.0"
  - file_path  (str, optional, default "values.yaml") – path relative to $REPO_PATH

Searches the YAML tree for mappings that contain an "image" key (or a nested
"repository"/"tag" split) matching the given image name and updates the tag.

Supported YAML shapes:
  image: nginx:1.26.0                       → image: nginx:1.27.0
  image:
    repository: nginx
    tag: "1.26.0"                           → tag: "1.27.0"

Writes a unified diff to stdout.
Exits 0 on success (including when already at target tag).
Exits 1 on any failure.

Never touches the network.
"""

import difflib
import io
import json
import os
import sys

from ruamel.yaml import YAML


def load_params(path: str) -> dict:
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)


def yaml_to_string(yaml: YAML, data) -> str:
    buf = io.StringIO()
    yaml.dump(data, buf)
    return buf.getvalue()


def update_node(node, image: str, tag: str) -> bool:
    """Recursively walk *node* and update matching image entries.

    Returns True if at least one update was made.
    """
    changed = False

    if isinstance(node, dict):
        # Shape 1: {"image": "nginx:1.26.0"}
        if "image" in node and isinstance(node["image"], str):
            current = node["image"]
            repo_part = current.split(":")[0]
            # Match on the basename of the repository path.
            if repo_part == image or repo_part.split("/")[-1] == image:
                new_val = f"{repo_part}:{tag}"
                if current != new_val:
                    node["image"] = new_val
                    changed = True

        # Shape 2: {"image": {"repository": "nginx", "tag": "1.26.0"}}
        if "image" in node and isinstance(node["image"], dict):
            img_block = node["image"]
            repo = img_block.get("repository", "")
            if repo == image or repo.split("/")[-1] == image:
                current_tag = str(img_block.get("tag", ""))
                if current_tag != tag:
                    img_block["tag"] = tag
                    changed = True

        # Recurse into all values.
        for v in node.values():
            if update_node(v, image, tag):
                changed = True

    elif isinstance(node, list):
        for item in node:
            if update_node(item, image, tag):
                changed = True

    return changed


def main() -> int:
    params_path = os.environ.get("PARAMS_PATH")
    repo_path = os.environ.get("REPO_PATH")

    if not params_path:
        print("ERROR: PARAMS_PATH is not set", file=sys.stderr)
        return 1
    if not repo_path:
        print("ERROR: REPO_PATH is not set", file=sys.stderr)
        return 1

    try:
        params = load_params(params_path)
    except Exception as exc:
        print(f"ERROR: cannot read PARAMS_PATH: {exc}", file=sys.stderr)
        return 1

    image = params.get("image")
    tag = params.get("tag")
    file_path = params.get("file_path", "values.yaml")

    if not image:
        print("ERROR: params missing required key 'image'", file=sys.stderr)
        return 1
    if not tag:
        print("ERROR: params missing required key 'tag'", file=sys.stderr)
        return 1

    target = os.path.join(repo_path, file_path)

    yaml = YAML()
    yaml.preserve_quotes = True

    try:
        with open(target, encoding="utf-8") as fh:
            original_text = fh.read()
        data = yaml.load(original_text)
    except Exception as exc:
        print(f"ERROR: cannot read {file_path}: {exc}", file=sys.stderr)
        return 1

    if not update_node(data, image, tag):
        # Already at target tag or image not found — idempotent success, empty diff.
        return 0

    updated_text = yaml_to_string(yaml, data)

    diff_lines = list(
        difflib.unified_diff(
            original_text.splitlines(keepends=True),
            updated_text.splitlines(keepends=True),
            fromfile=f"a/{file_path}",
            tofile=f"b/{file_path}",
        )
    )
    sys.stdout.writelines(diff_lines)
    return 0


if __name__ == "__main__":
    sys.exit(main())
