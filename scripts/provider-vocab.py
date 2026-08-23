#!/usr/bin/env python3
"""Print check-provider-boundary.sh's provider-name vocabulary, one word per
line: every plugin catalog.toml publishes, plus every executable name that
plugin's own plugin.toml declares.

Decodes TOML rather than scanning it as text (a commented-out entry or a
name embedded in some other field can't be mistaken for live vocabulary),
using only the standard library so this stays a repo script, not a change
to core.

Usage: provider-vocab.py <plugins-root>
"""
import os
import sys
import tomllib


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: provider-vocab.py <plugins-root>", file=sys.stderr)
        return 2
    plugins_root = sys.argv[1]

    with open(os.path.join(plugins_root, "catalog.toml"), "rb") as f:
        catalog = tomllib.load(f)

    # catalog.toml's `plugins` list is the exact, reviewable set of
    # published plugins (see plugins/catalog.toml's own header comment) —
    # a plugin.toml present on disk but not listed here is not yet
    # published and contributes no vocabulary.
    words = set()
    for rel in catalog["plugins"]:
        words.add(os.path.basename(os.path.normpath(rel)))
        manifest_path = os.path.join(plugins_root, rel, "plugin.toml")
        with open(manifest_path, "rb") as f:
            manifest = tomllib.load(f)
        for executable in manifest.get("executables", []):
            name = executable.get("name")
            if name:
                words.add(name)

    for word in sorted(words):
        print(word)
    return 0


if __name__ == "__main__":
    sys.exit(main())
