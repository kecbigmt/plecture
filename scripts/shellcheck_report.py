"""Run shellcheck over shell scripts and validate their exceptions.

Usage:
  shellcheck_report.py <shellcheck>          every tracked script, from the repository root
  shellcheck_report.py --stdin <shellcheck> <args...>
                                             one script on stdin, standing in for shellcheck
                                             itself under actionlint's -shellcheck

shellcheck itself never reports a `disable=` directive that suppresses
nothing, nor one whose scope is a whole function or block, so each one is
probed here by re-running shellcheck over edited copies of the script. A
directive-shaped line counts only once shellcheck's own parser reads it as a
directive, so text inside a heredoc is never taken for one.
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
DIRECTIVE = re.compile(r"#[ \t]*shellcheck\s+(?P<body>.*)$")
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


def run_shellcheck(binary, args, stdin=None):
    proc = subprocess.run([binary, *args], input=stdin, capture_output=True, text=True)
    # Exit 1 means findings; anything else means shellcheck could not
    # analyze its input, which is not evidence the input is clean.
    if proc.returncode not in (0, 1):
        sys.exit(f"shellcheck exited {proc.returncode}: {proc.stderr.strip()}")
    return json.loads(proc.stdout)["comments"]


class Shellcheck:
    """Runs shellcheck over edited copies of one script.

    A copy of a file is written beside the original, so a `source` it
    follows resolves exactly as it does for the original.
    """

    def __init__(self, binary, args, path=None):
        self.binary, self.args, self.path = binary, args, path

    def comments(self, lines):
        body = "".join(lines)
        if self.path is None:
            return run_shellcheck(self.binary, [*self.args, "-"], stdin=body)
        directory, name = os.path.split(self.path)
        fd, copy = tempfile.mkstemp(
            dir=directory or ".", prefix=f".{name}.probe-", suffix=os.path.splitext(name)[1]
        )
        try:
            with os.fdopen(fd, "w") as f:
                f.write(body)
            return run_shellcheck(self.binary, [*self.args, copy])
        finally:
            os.unlink(copy)

    def findings(self, lines):
        return [(f"SC{c['code']}", c["line"]) for c in self.comments(lines)]


def disabled_codes(line):
    """Returns (codes, reason, start) for a disable directive, or None.

    A candidate may follow a command on the same line, as shellcheck honors
    one after `;`; whether it is a directive at all is left to shellcheck's
    own parser (see parsed_directive_lines).
    """
    m = DIRECTIVE.search(line)
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
    return codes, reason.strip(), m.start()


def replace_directive(line, start, directive):
    """Swaps the directive at line[start:] for another, keeping the code before it."""
    return f"{line[:start]}{directive}\n"


def precedes_first_command(lines, index, start):
    return not lines[index][:start].strip() and all(
        not l.strip() or l.lstrip().startswith("#") for l in lines[:index]
    )


def parsed_directive_lines(sc, lines, candidates):
    """Returns the candidate indexes shellcheck parses as directives."""
    unknown = "SC1107"
    probe = list(lines)
    for index, (_, _, start) in candidates.items():
        probe[index] = replace_directive(lines[index], start, "# shellcheck plecture-probe=1")
    before = {line for code, line in sc.findings(lines) if code == unknown}
    after = {line for code, line in sc.findings(probe) if code == unknown}
    return {index for index in candidates if index + 1 in after - before}


def live_codes(sc, lines, index, start, codes):
    """Returns the codes the directive at lines[index] still suppresses."""

    def count(body):
        found = {}
        for code, _ in sc.findings(body):
            found[code] = found.get(code, 0) + 1
        return found

    neutralized = list(lines)
    neutralized[index] = replace_directive(lines[index], start, "#")
    before, after = count(lines), count(neutralized)
    return {c for c in codes if after.get(c, 0) > before.get(c, 0)}


CANARY_CODE = "SC2046"
CANARY = "echo $(true)\n"


def covers_more_than_one_line(sc, lines, index, start):
    """Reports whether the directive at lines[index] may reach past one line.

    A canary finding is placed on the line after the directive's command
    line. The directive is confined to one line only when the canary is
    reported without the directive and still reported with a directive at
    the same spot. Inserting the canary inside a function, block, case list,
    continued line or heredoc either hides it or breaks the parse, and both
    count as reaching further: an unparsable probe proves nothing, so it
    must not pass.
    """
    command = next(
        (i for i in range(index + 1, len(lines)) if lines[i].strip() and not lines[i].lstrip().startswith("#")),
        None,
    )
    if command is None:
        return False

    def canary_reported(directive):
        probe = list(lines)
        probe[index] = directive
        probe.insert(command + 1, CANARY)
        return (CANARY_CODE, command + 2) in sc.findings(probe)

    confined = canary_reported(replace_directive(lines[index], start, "#")) and canary_reported(
        replace_directive(lines[index], start, f"# shellcheck disable={CANARY_CODE}")
    )
    return not confined


def exception_problems(sc, lines):
    """Returns (line, code, message) for each rejected exception in lines."""
    candidates = {i: p for i, p in enumerate(map(disabled_codes, lines)) if p}
    if not candidates:
        return []
    problems = []
    for index in sorted(parsed_directive_lines(sc, lines, candidates)):
        codes, reason, start = candidates[index]
        specific = [c for c in codes if CODE.match(c)]
        code = int(specific[0][2:]) if specific else 0

        def problem(message):
            problems.append((index + 1, code, message))

        if not reason:
            problem(f"exception has no reason; write it as {EXCEPTION_SYNTAX}")
        unspecific = [c for c in codes if not CODE.match(c)]
        if unspecific:
            problem(f"exception names {', '.join(unspecific)}, not specific SCxxxx codes")
        if precedes_first_command(lines, index, start):
            problem(
                "exception before the first command covers the whole file; "
                "move it to the line before the one command it excuses"
            )
            continue
        if covers_more_than_one_line(sc, lines, index, start):
            problem(
                "exception covers more than the one line after it (a function, block, case, "
                "continued or heredoc command); move it right above the single line it excuses"
            )
        stale = sorted(set(specific) - live_codes(sc, lines, index, start, specific))
        if stale:
            problem(f"exception for {', '.join(stale)} suppresses nothing; remove it")
    return problems


def read_lines(path):
    with open(path) as f:
        return f.readlines()


def stdin_main(binary, args):
    """Stands in for shellcheck under actionlint: same findings, plus exceptions."""
    args = [a for a in args if a != "-"]
    if "-f" in args:
        args[args.index("-f") + 1] = "json1"
    sc = Shellcheck(binary, args)
    lines = sys.stdin.read().splitlines(keepends=True)
    comments = sc.comments(lines)
    for line, code, message in exception_problems(sc, lines):
        comments.append(
            {"file": "-", "line": line, "endLine": line, "column": 1, "endColumn": 1,
             "level": "error", "code": code, "message": message}
        )
    print(json.dumps(comments))
    return 1 if comments else 0


def main():
    if sys.argv[1] == "--stdin":
        return stdin_main(sys.argv[2], sys.argv[3:])
    binary = sys.argv[1]
    scripts = shell_scripts()
    if not scripts:
        print("shellcheck check passed (no shell scripts found)")
        return 0
    findings = run_shellcheck(binary, [*ARGS, *scripts])
    for c in findings:
        print(
            f"{c['file']}:{c['line']}:{c['column']}: SC{c['code']} ({c['level']}): {c['message']}"
            f" fix: https://www.shellcheck.net/wiki/SC{c['code']}"
        )
    problems = [
        f"{path}:{line}: {message}"
        for path in scripts
        for line, _, message in exception_problems(Shellcheck(binary, ARGS, path), read_lines(path))
    ]
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
