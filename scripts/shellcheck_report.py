"""Run shellcheck over every tracked shell script and validate its exceptions.

Usage: shellcheck_report.py <shellcheck-binary>, run from the repository root.

shellcheck itself never reports a `disable=` directive that suppresses
nothing, so each one is proven live here by re-running shellcheck with only
that directive removed and requiring a finding it disables to reappear.
"""

import json
import os
import re
import subprocess
import sys
import tempfile

ARGS = [
    "--norc",
    "--external-sources",
    "--source-path=SCRIPTDIR",
    "--severity=warning",
    "--format=json1",
]
SHEBANG = re.compile(r"^#!\s*\S*/(?:env\s+(?:-\S+\s+)*)?(?:ba|da|k)?sh(?:\s|$)")
DIRECTIVE = re.compile(r"^\s*#\s*shellcheck\s+(?P<body>.*)$")
CODE = re.compile(r"^SC\d{4}$")
EXCEPTION_SYNTAX = "# shellcheck disable=SCxxxx # <reason>"


def shell_scripts():
    out = subprocess.run(
        ["git", "ls-files", "-z"], check=True, capture_output=True
    ).stdout.decode()
    scripts = []
    for path in sorted(p for p in out.split("\0") if p):
        if os.path.islink(path) or not os.path.isfile(path):
            continue
        if path.endswith((".sh", ".bash")):
            scripts.append(path)
            continue
        with open(path, "rb") as f:
            first = f.readline(256)
        if SHEBANG.match(first.decode("utf-8", "replace")):
            scripts.append(path)
    return scripts


def shellcheck(binary, paths):
    proc = subprocess.run([binary, *ARGS, *paths], capture_output=True, text=True)
    # Exit 1 means findings; anything else means shellcheck could not
    # analyze its input, which is not evidence the input is clean.
    if proc.returncode not in (0, 1):
        sys.exit(f"shellcheck exited {proc.returncode}: {proc.stderr.strip()}")
    return json.loads(proc.stdout)["comments"]


def disabled_codes(line):
    """Returns (codes, reason) for a disable directive, or None."""
    m = DIRECTIVE.match(line)
    if not m:
        return None
    body, _, reason = m.group("body").partition("#")
    codes = []
    for item in body.split():
        key, _, value = item.partition("=")
        if key == "disable":
            codes.extend(c for c in value.split(",") if c)
    if not codes:
        return None
    return codes, reason.strip()


def precedes_first_command(lines, index):
    return all(not l.strip() or l.lstrip().startswith("#") for l in lines[:index])


def live_codes(binary, path, lines, index, codes):
    """Returns the codes the directive at lines[index] still suppresses."""
    with tempfile.TemporaryDirectory() as tmp:
        copy = os.path.join(tmp, os.path.basename(path))

        def count(body):
            with open(copy, "w") as f:
                f.writelines(body)
            found = {}
            for c in shellcheck(binary, [copy]):
                found[f"SC{c['code']}"] = found.get(f"SC{c['code']}", 0) + 1
            return found

        before = count(lines)
        neutralized = list(lines)
        neutralized[index] = "#\n"
        after = count(neutralized)
    return {c for c in codes if after.get(c, 0) > before.get(c, 0)}


def exception_problems(binary, path):
    with open(path) as f:
        lines = f.readlines()
    problems = []
    for index, line in enumerate(lines):
        parsed = disabled_codes(line)
        if parsed is None:
            continue
        codes, reason = parsed
        where = f"{path}:{index + 1}"
        if not reason:
            problems.append(f"{where}: exception has no reason; write it as {EXCEPTION_SYNTAX}")
        unspecific = [c for c in codes if not CODE.match(c)]
        if unspecific:
            problems.append(f"{where}: exception names {', '.join(unspecific)}, not specific SCxxxx codes")
        if precedes_first_command(lines, index):
            problems.append(
                f"{where}: exception before the first command covers the whole file; "
                "move it to the line before the one command it excuses"
            )
            continue
        stale = [c for c in codes if CODE.match(c)]
        stale = sorted(set(stale) - live_codes(binary, path, lines, index, stale))
        if stale:
            problems.append(
                f"{where}: exception for {', '.join(stale)} suppresses nothing; remove it"
            )
    return problems


def main():
    binary = sys.argv[1]
    scripts = shell_scripts()
    if not scripts:
        print("shellcheck check passed (no shell scripts found)")
        return 0
    findings = shellcheck(binary, scripts)
    for c in findings:
        print(
            f"{c['file']}:{c['line']}:{c['column']}: SC{c['code']} ({c['level']}): {c['message']}"
            f" fix: https://www.shellcheck.net/wiki/SC{c['code']}"
        )
    problems = [p for path in scripts for p in exception_problems(binary, path)]
    for p in problems:
        print(p)
    if findings or problems:
        print(
            "\nshellcheck (severity warning): fix each finding as its wiki page describes. "
            "Only when the fix is wrong for that one command, put "
            f"`{EXCEPTION_SYNTAX}` on the line before it; an exception that "
            "no longer suppresses anything fails this check.",
            file=sys.stderr,
        )
        return 1
    print(f"shellcheck check passed ({len(scripts)} shell scripts)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
