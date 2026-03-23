#!/usr/bin/env python3
"""
Add SPDX copyright headers to all non-generated .go files in the jitsudo monorepo.

Usage:
    python3 scripts/add_spdx_headers.py [--dry-run] [--check]

Modes:
    (default)  Apply changes to files on disk.
    --dry-run  Print which files would be changed, but do not write anything.
    --check    Exit with code 1 if any file is missing headers (for CI use).
"""
import re
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).parent.parent

APACHE_HEADER = (
    "// Copyright \u00a9 2026 Yu Technology Group, LLC d/b/a jitsudo\n"
    "// SPDX-License-Identifier: Apache-2.0\n"
)
ELV2_HEADER = (
    "// Copyright \u00a9 2026 Yu Technology Group, LLC d/b/a jitsudo\n"
    "// SPDX-License-Identifier: Elastic-2.0\n"
)

# Paths relative to REPO_ROOT — first match wins.
APACHE_PREFIXES = [
    "cmd/jitsudo/",
    "internal/cli/",
    "internal/providers/",
    "pkg/",
    "tools/",
    "internal/testutil/",
    "internal/version/",
]
ELV2_PREFIXES = [
    "cmd/jitsudod/",
    "internal/server/",
    "internal/store/",
    "internal/config/",
]
SKIP_PREFIXES = [
    "internal/gen/",
]

# Matches a blank comment line immediately before a "// License:..." line.
# We remove both together.
_BLANK_BEFORE_LICENSE = re.compile(r"^//\n(?=// License:)", re.MULTILINE)
_LICENSE_LINE = re.compile(r"^// License:.*\n", re.MULTILINE)
# Collapse 3+ consecutive newlines down to 2.
_TRIPLE_NEWLINE = re.compile(r"\n{3,}")


def classify(rel_path: str) -> str | None:
    """Return 'apache', 'elv2', or None (skip)."""
    for p in SKIP_PREFIXES:
        if rel_path.startswith(p):
            return None
    for p in APACHE_PREFIXES:
        if rel_path.startswith(p):
            return "apache"
    for p in ELV2_PREFIXES:
        if rel_path.startswith(p):
            return "elv2"
    raise ValueError(f"Unclassified path: {rel_path}")


def transform(content: str, header: str) -> str:
    """Return content with the SPDX header added (idempotent)."""
    if "SPDX-License-Identifier:" in content:
        return content  # already correct

    # Remove old inline license comment (blank comment line + License line).
    content = _BLANK_BEFORE_LICENSE.sub("", content)
    content = _LICENSE_LINE.sub("", content)
    # Collapse any triple-newlines left behind by the removal.
    content = _TRIPLE_NEWLINE.sub("\n\n", content)

    lines = content.splitlines(keepends=True)

    # Determine where to insert the header.
    insert_after = 0
    if lines and lines[0].startswith("//go:build"):
        # Skip past the constraint line and the blank line that follows it.
        insert_after = 1
        if len(lines) > 1 and lines[1].strip() == "":
            insert_after = 2

    # header block = copyright lines + blank separator line
    header_block = header + "\n"

    result = lines[:insert_after] + [header_block] + lines[insert_after:]
    return "".join(result)


def main() -> None:
    dry_run = "--dry-run" in sys.argv
    check_mode = "--check" in sys.argv

    go_files = sorted(REPO_ROOT.rglob("*.go"))
    changed: list[str] = []
    errors: list[str] = []

    for path in go_files:
        rel = str(path.relative_to(REPO_ROOT))
        try:
            license_type = classify(rel)
        except ValueError as e:
            errors.append(str(e))
            continue

        if license_type is None:
            continue  # skip generated files

        header = APACHE_HEADER if license_type == "apache" else ELV2_HEADER

        original = path.read_text(encoding="utf-8")
        transformed = transform(original, header)

        if transformed == original:
            continue  # already correct, no change needed

        changed.append(rel)

        if check_mode:
            print(f"NEEDS UPDATE: {rel}")
        elif dry_run:
            print(f"WOULD UPDATE: {rel}")
        else:
            path.write_text(transformed, encoding="utf-8")
            print(f"UPDATED: {rel}")

    if errors:
        print("\nErrors (unclassified paths):", file=sys.stderr)
        for e in errors:
            print(f"  {e}", file=sys.stderr)
        sys.exit(1)

    if check_mode:
        if changed:
            print(f"\n{len(changed)} file(s) need SPDX headers.", file=sys.stderr)
            sys.exit(1)
        else:
            print("All files have SPDX headers.")
    elif dry_run:
        print(f"\nWould update {len(changed)} file(s).")
    else:
        print(f"\nDone. Updated {len(changed)} file(s).")


if __name__ == "__main__":
    main()
