#!/usr/bin/env python3
"""
update_dependency: Bump an npm package version in package.json.

Reads $PARAMS_PATH for:
  - package     (str, required) – npm package name
  - to_version  (str, required) – target version, e.g. "4.17.21"
  - from_version (str, optional) – expected current version; fails if mismatch

Reads $REPO_PATH/package.json, updates the version in dependencies or
devDependencies, then writes a unified diff to stdout.

Exits 0 on success (including when already at target version).
Exits 1 on any failure.

Never touches the network.
"""

import difflib
import json
import os
import sys


def load_json(path: str) -> dict:
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)


def dump_json(obj: dict) -> str:
    return json.dumps(obj, indent=2, ensure_ascii=False) + "\n"


def main() -> int:
    params_path = os.environ.get("PARAMS_PATH")
    repo_path = os.environ.get("REPO_PATH")

    if not params_path:
        print("ERROR: PARAMS_PATH is not set", file=sys.stderr)
        return 1
    if not repo_path:
        print("ERROR: REPO_PATH is not set", file=sys.stderr)
        return 1

    # Load parameters.
    try:
        params = load_json(params_path)
    except Exception as exc:
        print(f"ERROR: cannot read PARAMS_PATH: {exc}", file=sys.stderr)
        return 1

    package = params.get("package")
    to_version = params.get("to_version")
    from_version = params.get("from_version")  # optional

    if not package:
        print("ERROR: params missing required key 'package'", file=sys.stderr)
        return 1
    if not to_version:
        print("ERROR: params missing required key 'to_version'", file=sys.stderr)
        return 1

    pkg_json_path = os.path.join(repo_path, "package.json")
    try:
        pkg = load_json(pkg_json_path)
    except Exception as exc:
        print(f"ERROR: cannot read package.json: {exc}", file=sys.stderr)
        return 1

    original_text = dump_json(pkg)

    # Locate the package in dependencies or devDependencies.
    found = False
    for section_key in ("dependencies", "devDependencies"):
        section = pkg.get(section_key)
        if not isinstance(section, dict):
            continue
        if package not in section:
            continue

        current_raw = section[package]
        # Strip common range prefixes (^, ~, >=, etc.) for comparison.
        current_plain = current_raw.lstrip("^~>=<").strip()

        if from_version is not None and current_plain != from_version.lstrip("^~>=<").strip():
            print(
                f"ERROR: expected {package} at {from_version!r} but found {current_raw!r}",
                file=sys.stderr,
            )
            return 1

        # Preserve prefix if any (e.g. "^4.17.20" → "^4.17.21").
        prefix = ""
        for ch in current_raw:
            if ch in ("^", "~", ">", "<", "=", " "):
                prefix += ch
            else:
                break

        section[package] = prefix + to_version
        found = True
        break

    if not found:
        print(
            f"ERROR: package '{package}' not found in dependencies or devDependencies",
            file=sys.stderr,
        )
        return 1

    updated_text = dump_json(pkg)

    # Emit unified diff to stdout.
    diff_lines = list(
        difflib.unified_diff(
            original_text.splitlines(keepends=True),
            updated_text.splitlines(keepends=True),
            fromfile=f"a/package.json",
            tofile=f"b/package.json",
        )
    )
    sys.stdout.writelines(diff_lines)
    return 0


if __name__ == "__main__":
    sys.exit(main())
