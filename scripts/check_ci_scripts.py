"""Check that check-all.sh runs what the jobs an aggregator job requires run.

Usage: check_ci_scripts.py <workflow> <aggregator-job> <check-all.sh>

Each job the aggregator needs, directly or transitively, must be setup
actions followed by run steps that each call one scripts/ci/*.sh, and
check-all.sh --list must name every script those steps call. The workflow is
decoded by yq, which keeps line numbers; each run line is tokenized by shlex
as the shell would split it.
"""

import json
import os
import re
import shlex
import subprocess
import sys

SETUP_ACTION = re.compile(r"^actions/(checkout|setup-[a-z]+|cache)@")
JOB_SCRIPT = re.compile(r"^(?:\./)?(scripts/ci/[A-Za-z0-9_.-]+\.sh)$")
SHELL_OPERATOR = set("();<>|&")
# check-all.sh does not run the browser half of web-browser-acceptance, for
# the reason it states where it leaves the call out.
OMITTED = "scripts/ci/web-browser-acceptance-browser.sh"

YQ_ANNOTATE = (
    '(.. | select(tag == "!!map")) |= (. + {"__line": line, '
    '"__keylines": (to_entries | map({"key": .key, "value": (.key | line)}) | from_entries)})'
)

RULE = (
    "Every job the aggregator requires is setup actions (actions/checkout, "
    "actions/setup-*, actions/cache) followed by run steps of the form "
    "`./scripts/ci/<job>.sh [args]`, and scripts/check-all.sh calls each of "
    "those scripts, so a developer runs what CI runs."
)
EXCEPTION = (
    "The one omission this check accepts is check-all.sh not calling "
    f"{OMITTED}; it takes no other exception, and a new one needs the owner."
)


def keyline(node, key):
    return node.get("__keylines", {}).get(key, node.get("__line", 0))


def omission_line():
    with open(__file__) as source:
        for number, text in enumerate(source, 1):
            if text.startswith("OMITTED = "):
                return number
    return 0


class Check:
    def __init__(self, workflow, aggregator, check_all):
        self.workflow = workflow
        self.aggregator = aggregator
        self.check_all = check_all
        self.findings = []

    def add(self, path, line, message):
        self.findings.append((path, line, message))

    def at(self, line, message):
        self.add(self.workflow, line, message)

    def run(self):
        proc = subprocess.run(["yq", "-o=json", YQ_ANNOTATE, self.workflow], capture_output=True, text=True)
        if proc.returncode != 0:
            self.add(self.workflow, 0, f"yq could not decode the workflow: {proc.stderr.strip()}")
            return {}
        jobs = (json.loads(proc.stdout) or {}).get("jobs") or {}
        if self.aggregator not in jobs:
            self.add(self.workflow, 0, f"no job {self.aggregator!r}: the aggregator names the jobs this check covers")
            return {}
        calls = {}
        for job_id in self.required(jobs):
            self.check_job(job_id, jobs[job_id], calls)
        return calls

    def required(self, jobs):
        order, pending = [], [self.aggregator]
        while pending:
            job_id = pending.pop()
            job = jobs[job_id]
            needs = job.get("needs") or []
            needs = [needs] if isinstance(needs, str) else needs
            line = keyline(job, "needs")
            for need in needs:
                if need not in jobs:
                    self.at(line, f"job {job_id}: needs {need!r}, which is not a job in this workflow")
                elif need not in order:
                    order.append(need)
                    pending.append(need)
        return [job_id for job_id in jobs if job_id in order]

    def check_job(self, job_id, job, calls):
        if "uses" in job:
            self.at(keyline(job, "uses"), f"job {job_id}: calls a reusable workflow, whose steps check-all.sh cannot see; inline them as setup actions and scripts/ci calls")
            return
        ran_script = False
        for step in job.get("steps") or []:
            if "uses" in step:
                line = keyline(step, "uses")
                if not SETUP_ACTION.match(str(step["uses"])):
                    self.at(line, f"job {job_id}: uses {step['uses']}, which is not a setup action; move what it does into the job's scripts/ci script")
                elif ran_script:
                    self.at(line, f"job {job_id}: a setup action follows a scripts/ci call; move it before the job's first run step")
            elif "run" in step:
                ran_script = True
                script = self.check_run(job_id, step)
                if script:
                    calls.setdefault(script, (job_id, keyline(step, "run")))

    def check_run(self, job_id, step):
        line = keyline(step, "run")
        for key in ("working-directory", "shell"):
            if key in step:
                self.at(keyline(step, key), f"job {job_id}: a run step sets {key}, which check-all.sh's call would not; let the script choose its directory")
                return None
        text = str(step["run"]).strip()
        if "\n" in text:
            self.at(line, f"job {job_id}: a run step spans several lines; put the commands in the job's scripts/ci script and call it on one line")
            return None
        lexer = shlex.shlex(text, posix=True, punctuation_chars=True)
        lexer.whitespace_split = True
        try:
            words = list(lexer)
        except ValueError as err:
            self.at(line, f"job {job_id}: the run step does not parse as a shell command ({err})")
            return None
        match = JOB_SCRIPT.match(words[0]) if words else None
        if not match or any(set(word) <= SHELL_OPERATOR or "`" in word for word in words):
            self.at(line, f"job {job_id}: the run step `{text}` is not a single scripts/ci/*.sh call; move its commands into the job's scripts/ci script")
            return None
        script = match.group(1)
        try:
            open(script).close()
        except OSError:
            self.at(line, f"job {job_id}: calls {script}, which does not exist")
            return None
        return script

    def compare(self, calls):
        proc = subprocess.run(["./" + self.check_all, "--list"], capture_output=True, text=True)
        if proc.returncode != 0:
            self.add(self.check_all, None, f"--list exited {proc.returncode}, so what it runs is unknown: {proc.stderr.strip()}")
            return
        listed = set(proc.stdout.split())
        for script, (job_id, line) in sorted(calls.items(), key=lambda item: item[1][1]):
            if script not in listed and script != OMITTED:
                self.at(line, f"job {job_id}: calls {script}, which {self.check_all} never calls; call it there too")
        here = "scripts/" + os.path.basename(__file__)
        if OMITTED not in calls:
            self.add(here, omission_line(), f"no required job calls {OMITTED}, so its omission from {self.check_all} suppresses nothing; remove the omission here and its reason there")
        elif OMITTED in listed:
            self.add(here, omission_line(), f"{self.check_all} calls {OMITTED}, so the omission suppresses nothing; remove it here and the reason there")


def main():
    if len(sys.argv) != 4:
        sys.exit(__doc__)
    check = Check(*sys.argv[1:])
    calls = check.run()
    if not check.findings:
        check.compare(calls)
    if not check.findings:
        print(f"ci-scripts check passed ({len(calls)} job scripts, all called by {check.check_all})")
        return
    for path, line, message in sorted(check.findings, key=lambda f: (f[0], f[1] or 0)):
        print(f"{path}:{line}: {message}" if line is not None else f"{path}: {message}")
    print(RULE)
    print(EXCEPTION)
    sys.exit(1)


if __name__ == "__main__":
    main()
