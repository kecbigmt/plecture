"""Run shellcheck over every tracked shell script and validate its exceptions.

Usage: shellcheck_report.py <shellcheck-binary>, run from the repository root.

shellcheck itself never reports a `disable=` directive that suppresses
nothing, so each one is proven live here by re-running shellcheck with only
that directive removed and requiring a finding it disables to reappear.
A directive-shaped line counts only once shellcheck's own parser reads it
as a directive, so text inside a heredoc is never taken for one.
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


def findings_of(binary, path, body):
    """Runs shellcheck over body as a copy of path; returns (code, line) pairs."""
    with tempfile.TemporaryDirectory() as tmp:
        copy = os.path.join(tmp, os.path.basename(path))
        with open(copy, "w") as f:
            f.writelines(body)
        return [(f"SC{c['code']}", c["line"]) for c in shellcheck(binary, [copy])]


def parsed_directive_lines(binary, path, lines, candidates):
    """Returns the candidate indexes shellcheck parses as directives."""
    unknown = "SC1107"
    probe = list(lines)
    for index in candidates:
        indent = lines[index][: len(lines[index]) - len(lines[index].lstrip())]
        probe[index] = f"{indent}# shellcheck plecture-probe=1\n"
    before = {line for code, line in findings_of(binary, path, lines) if code == unknown}
    after = {line for code, line in findings_of(binary, path, probe) if code == unknown}
    return {index for index in candidates if index + 1 in after - before}


def live_codes(binary, path, lines, index, codes):
    """Returns the codes the directive at lines[index] still suppresses."""

    def count(body):
        found = {}
        for code, _ in findings_of(binary, path, body):
            found[code] = found.get(code, 0) + 1
        return found

    neutralized = list(lines)
    neutralized[index] = "#\n"
    before, after = count(lines), count(neutralized)
    return {c for c in codes if after.get(c, 0) > before.get(c, 0)}


def exception_problems(binary, path):
    with open(path) as f:
        lines = f.readlines()
    candidates = {i: p for i, p in enumerate(map(disabled_codes, lines)) if p}
    if not candidates:
        return []
    problems = []
    for index in sorted(parsed_directive_lines(binary, path, lines, candidates)):
        codes, reason = candidates[index]
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
        sys.stdout.flush()
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
