"""Run shellcheck over shell scripts and reject every suppression directive.

Usage:
  shellcheck_report.py <shellcheck>          every tracked script, from the repository root
  shellcheck_report.py --stdin <shellcheck> <args...>
                                             one script on stdin, standing in for shellcheck
                                             itself under actionlint's -shellcheck

The check takes no exception, so a `disable=` directive fails it. Whether a
directive-shaped piece of text is a directive is left to shellcheck's own
parser, so text inside a heredoc or a quoted string is never taken for one.
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
DIRECTIVE = re.compile(r"#[ \t]*shellcheck\s")
SUPPRESSION = "suppression directive: delete it and fix the finding it hides"
NO_EXCEPTION = "This check takes no exception; if the finding cannot be fixed at its cause, stop and ask the owner."


def tracked_files():
    out = subprocess.run(["git", "ls-files", "-z"], check=True, capture_output=True).stdout.decode()
    return sorted(p for p in out.split("\0") if p)


def shell_scripts(paths):
    scripts = []
    for path in paths:
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

    def unknown_directive_lines(self, lines):
        return {c["line"] for c in self.comments(lines) if c["code"] == 1107}


def disable_keys(line):
    """Returns the column of every `disable=` after a directive-shaped prefix.

    Nothing is cut at a `#` or at the first match: a look-alike inside a
    quoted string or inside another key's value (`source=foo#bar`,
    `source=disable=x`) can precede the real key, and only shellcheck's
    parse decides which occurrence is one.
    """
    first = DIRECTIVE.search(line)
    if not first:
        return []
    return [m.start() for m in re.finditer("disable=", line) if m.start() > first.start()]


def directive_lines(sc, lines):
    """Returns the 1-based lines holding a `disable=` directive shellcheck parses.

    Respelling `disable` as the unknown key `disablx` keeps every quote and
    column in place, and shellcheck reports SC1107 only where it parsed a
    directive.
    """
    candidates = {i: disable_keys(line) for i, line in enumerate(lines)}
    candidates = {i: keys for i, keys in candidates.items() if keys}
    if not candidates:
        return []
    before = sc.unknown_directive_lines(lines)
    found = set()
    for round_ in range(max(len(keys) for keys in candidates.values())):
        probe = list(lines)
        for index, keys in candidates.items():
            if round_ < len(keys):
                key = keys[round_]
                probe[index] = lines[index][:key] + "disablx" + lines[index][key + len("disablx"):]
        found |= sc.unknown_directive_lines(probe) - before
    return sorted(found)


def read_lines(path):
    with open(path) as f:
        return f.readlines()


def stdin_main(binary, args):
    """Stands in for shellcheck under actionlint: same findings, plus directives."""
    args = [a for a in args if a != "-"]
    if "-f" in args:
        args[args.index("-f") + 1] = "json1"
    sc = Shellcheck(binary, args)
    lines = sys.stdin.read().splitlines(keepends=True)
    comments = sc.comments(lines)
    for line in directive_lines(sc, lines):
        comments.append(
            {"file": "-", "line": line, "endLine": line, "column": 1, "endColumn": 1,
             "level": "error", "code": 0, "message": SUPPRESSION}
        )
    print(json.dumps(comments))
    return 1 if comments else 0


def main():
    if sys.argv[1] == "--stdin":
        return stdin_main(sys.argv[2], sys.argv[3:])
    binary = sys.argv[1]
    tracked = tracked_files()
    problems = [f"{p}:1: shellcheck configuration file: {SUPPRESSION}" for p in tracked
                if os.path.basename(p) == ".shellcheckrc"]
    scripts = shell_scripts(tracked)
    findings = run_shellcheck(binary, [*ARGS, *scripts]) if scripts else []
    for c in findings:
        print(
            f"{c['file']}:{c['line']}:{c['column']}: SC{c['code']} ({c['level']}): {c['message']}"
            f" fix: https://www.shellcheck.net/wiki/SC{c['code']}"
        )
    for path in scripts:
        lines = read_lines(path)
        problems += [f"{path}:{line}: {SUPPRESSION}" for line in directive_lines(Shellcheck(binary, ARGS, path), lines)]
    for p in problems:
        print(p)
    if findings or problems:
        sys.stdout.flush()
        print(
            "\nshellcheck (severity warning): fix each finding as its wiki page describes. "
            + NO_EXCEPTION,
            file=sys.stderr,
        )
        return 1
    print(f"shellcheck check passed ({len(scripts)} shell scripts)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
