#!/usr/bin/env python3
"""Runs, on the local machine, every step of every job a workflow's
aggregator job requires, read from the workflow file itself.

check-all.sh executes the steps rather than keeping its own copy of their
commands: a copy is a second authority that drifts from the workflow, which
is how a change could pass check-all.sh and still fail CI. The `check`
command is the standing half of that design: it fails when a required step
uses a construct this runner does not model, since such a step is one
check-all.sh would not run the way CI runs it.

A step that cannot run locally carries a per-step exception, a comment line
directly above the step:

    # check-all-skip: while "<text>": <reason>

The exception holds only while the step's `run` (or `uses`) contains
<text>; once it does not, the exception is stale and the check fails.

The workflow is decoded by yq, which keeps the line numbers and comments a
JSON or YAML loader drops.
"""

import itertools
import json
import os
import re
import subprocess
import sys
import tempfile

MARKER = "check-all-skip"
EXCEPTION_RE = re.compile(r'^check-all-skip: while "([^"]+)": (\S.*)$')

# These only provision tools and caches; a local run uses the developer's
# own toolchain instead.
ENVIRONMENT_ACTIONS = {
    "actions/checkout",
    "actions/setup-go",
    "actions/setup-python",
    "actions/setup-node",
    "actions/cache",
}

# Keys that do not change what a step or job runs on a local machine.
# Anything else (a reusable `uses`, `services`, `container`, `defaults`,
# `shell`, `continue-on-error`, matrix `include`) changes it in a way this
# runner does not reproduce, so it is a finding.
WORKFLOW_KEYS = {"name", "on", "concurrency", "env", "jobs", "permissions"}
JOB_KEYS = {
    "name", "runs-on", "needs", "if", "steps", "strategy", "env", "outputs",
    "timeout-minutes", "permissions", "concurrency",
}
STRATEGY_KEYS = {"matrix", "fail-fast", "max-parallel"}
STEP_KEYS = {
    "name", "id", "if", "uses", "with", "run", "working-directory", "env",
    "timeout-minutes",
}


def github_context(base_sha, head_sha, branch):
    """A local run stands in for a pull_request event on the current
    branch; a github.* field not returned here has no meaningful local
    value, so an expression reading one is a finding."""
    return {
        "event_name": "pull_request",
        "event": {"pull_request": {"base": {"sha": base_sha}, "head": {"sha": head_sha}}},
        "head_ref": branch,
        # A pull_request run's ref is its merge ref, which compares unequal
        # to every branch ref; no such ref exists locally.
        "ref": "",
    }


def leaf_paths(tree, prefix=()):
    for key, value in tree.items():
        if isinstance(value, dict):
            yield from leaf_paths(value, prefix + (key,))
        else:
            yield prefix + (key,)


GITHUB_FIELDS = set(leaf_paths(github_context("", "", "")))

YQ_ANNOTATE = (
    '(.. | select(tag == "!!map")) |= (. + {'
    '"__line": line, "__head": head_comment, '
    '"__keylines": (to_entries | map({"key": .key, "value": (.key | line)})'
    " | from_entries)})"
)
YQ_MARKERS = (
    "[... | select((head_comment + line_comment + foot_comment)"
    ' | test("' + MARKER + '")) | line] | unique'
)


class Finding(Exception):
    def __init__(self, line, message):
        super().__init__(message)
        self.line = line
        self.message = message


# ---------------------------------------------------------------------------
# Expressions


TOKEN_RE = re.compile(
    r"\s*(?:(?P<str>'(?:[^']|'')*')|(?P<num>\d+(?:\.\d+)?)"
    r"|(?P<id>[A-Za-z_][A-Za-z0-9_-]*)|(?P<op>==|!=|&&|\|\||[!().,]))"
)


def tokenize(text):
    tokens, pos = [], 0
    while pos < len(text):
        if text[pos:].strip() == "":
            break
        m = TOKEN_RE.match(text, pos)
        if not m:
            raise ValueError(f"unsupported syntax at {text[pos:]!r}")
        pos = m.end()
        kind = m.lastgroup
        value = m.group(kind)
        if kind == "str":
            value = value[1:-1].replace("''", "'")
        elif kind == "num":
            value = float(value)
        tokens.append((kind, value))
    return tokens


class Parser:
    def __init__(self, text):
        self.tokens = tokenize(text)
        self.pos = 0

    def peek(self):
        return self.tokens[self.pos] if self.pos < len(self.tokens) else (None, None)

    def take(self, value=None):
        tok = self.peek()
        if tok[0] is None or (value is not None and tok[1] != value):
            raise ValueError(f"expected {value or 'a token'}, found {tok[1]!r}")
        self.pos += 1
        return tok

    def parse(self):
        node = self.binary(0)
        if self.peek()[0] is not None:
            raise ValueError(f"unexpected {self.peek()[1]!r}")
        return node

    LEVELS = [("||",), ("&&",), ("==", "!=")]

    def binary(self, level):
        if level == len(self.LEVELS):
            return self.unary()
        node = self.binary(level + 1)
        while self.peek()[0] == "op" and self.peek()[1] in self.LEVELS[level]:
            op = self.take()[1]
            node = ("bin", op, node, self.binary(level + 1))
        return node

    def unary(self):
        if self.peek() == ("op", "!"):
            self.take()
            return ("not", self.unary())
        return self.primary()

    def primary(self):
        kind, value = self.take()
        if kind in ("str", "num"):
            return ("lit", value)
        if kind == "op" and value == "(":
            node = self.binary(0)
            self.take(")")
            return node
        if kind != "id":
            raise ValueError(f"unexpected {value!r}")
        if value in ("true", "false", "null"):
            return ("lit", {"true": True, "false": False, "null": None}[value])
        if self.peek() == ("op", "("):
            self.take()
            args = []
            while self.peek() != ("op", ")"):
                if args:
                    self.take(",")
                args.append(self.binary(0))
            self.take(")")
            return ("call", value, args)
        path = [value]
        while self.peek() == ("op", "."):
            self.take()
            path.append(self.take()[1])
        return ("ref", path)


def split_template(text):
    """Splits a string into literal parts and ${{ }} expression sources."""
    parts, pos = [], 0
    while True:
        start = text.find("${{", pos)
        if start < 0:
            parts.append(("text", text[pos:]))
            return parts
        parts.append(("text", text[pos:start]))
        i, quoted = start + 3, False
        while i < len(text):
            if text[i] == "'":
                quoted = not quoted
            elif not quoted and text.startswith("}}", i):
                break
            i += 1
        else:
            raise ValueError("unterminated ${{")
        parts.append(("expr", text[start + 3 : i]))
        pos = i + 2


def condition_source(value):
    text = str(value).strip()
    if text.startswith("${{") and text.endswith("}}"):
        return text[3:-2]
    return text


class Scope:
    """What an evaluation site may reference."""

    def __init__(self, needs=(), matrix_keys=(), step_ids=()):
        self.needs = dict(needs)
        self.matrix_keys = set(matrix_keys)
        self.step_ids = set(step_ids)


def validate(node, scope):
    kind = node[0]
    if kind == "lit":
        return
    if kind == "not":
        validate(node[1], scope)
    elif kind == "bin":
        validate(node[2], scope)
        validate(node[3], scope)
    elif kind == "call":
        name, args = node[1], node[2]
        if name not in FUNCTIONS:
            raise ValueError(f"function {name}() is not one the runner evaluates")
        arity = FUNCTIONS[name].__code__.co_argcount
        if len(args) != arity:
            raise ValueError(f"{name}() takes {arity} argument(s)")
        for arg in args:
            validate(arg, scope)
    elif kind == "ref":
        validate_ref(node[1], scope)


def validate_ref(path, scope):
    root, rest = path[0], tuple(path[1:])
    dotted = ".".join(path)
    if root == "github":
        if rest not in GITHUB_FIELDS:
            raise ValueError(f"{dotted} has no local value")
    elif root == "env":
        if len(rest) != 1:
            raise ValueError(f"{dotted} is not an env variable reference")
    elif root == "matrix":
        if len(rest) != 1 or rest[0] not in scope.matrix_keys:
            raise ValueError(f"{dotted} names no key of this job's matrix")
    elif root == "needs":
        if len(rest) != 3 or rest[1] != "outputs":
            raise ValueError(f"{dotted} is not a needs.<job>.outputs.<name> reference")
        if rest[0] not in scope.needs:
            raise ValueError(f"{dotted} names a job this job does not need")
        if rest[2] not in scope.needs[rest[0]]:
            raise ValueError(f"{dotted} names an output job {rest[0]} does not declare")
    elif root == "steps":
        if len(rest) != 3 or rest[1] != "outputs":
            raise ValueError(f"{dotted} is not a steps.<id>.outputs.<name> reference")
        if rest[0] not in scope.step_ids:
            raise ValueError(f"{dotted} names no earlier run step this runner executes")
    else:
        raise ValueError(f"the {root} context is not one the runner evaluates")


def as_text(v):
    return v if isinstance(v, str) else stringify(v)


def check_template(text, scope):
    for kind, part in split_template(as_text(text)):
        if kind == "expr":
            validate(Parser(part).parse(), scope)


def check_condition(value, scope):
    validate(Parser(condition_source(value)).parse(), scope)


def truthy(v):
    return v not in (False, None, "", 0) and v == v


def number(v):
    if v is None:
        return 0.0
    if isinstance(v, bool):
        return 1.0 if v else 0.0
    if isinstance(v, float):
        return v
    try:
        return float(str(v).strip() or "0")
    except ValueError:
        return float("nan")


def loose_equal(a, b):
    # GitHub compares strings case-insensitively and coerces mismatched
    # scalar types to numbers.
    if isinstance(a, str) and isinstance(b, str):
        return a.lower() == b.lower()
    if isinstance(a, (list, dict)) or isinstance(b, (list, dict)):
        return a is b
    return number(a) == number(b)


def stringify(v):
    if v is None:
        return ""
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, float):
        return str(int(v)) if v.is_integer() else str(v)
    if isinstance(v, (list, dict)):
        return json.dumps(v)
    return v


def fn_contains(search, item):
    if isinstance(search, list):
        return any(loose_equal(x, item) for x in search)
    return stringify(item).lower() in stringify(search).lower()


def fn_starts_with(text, prefix):
    return stringify(text).lower().startswith(stringify(prefix).lower())


FUNCTIONS = {
    "contains": fn_contains,
    "startsWith": fn_starts_with,
    "fromJson": lambda text: json.loads(stringify(text)),
    "toJson": lambda value: json.dumps(value, indent=2),
}


def evaluate(node, ctx):
    kind = node[0]
    if kind == "lit":
        return node[1]
    if kind == "not":
        return not truthy(evaluate(node[1], ctx))
    if kind == "bin":
        op, left = node[1], evaluate(node[2], ctx)
        if op == "&&":
            return evaluate(node[3], ctx) if truthy(left) else left
        if op == "||":
            return left if truthy(left) else evaluate(node[3], ctx)
        right = evaluate(node[3], ctx)
        return loose_equal(left, right) == (op == "==")
    if kind == "call":
        return FUNCTIONS[node[1]](*[evaluate(a, ctx) for a in node[2]])
    value = ctx
    for key in node[1]:
        value = value.get(key) if isinstance(value, dict) else None
    return value


def render(text, ctx):
    out = []
    for kind, part in split_template(as_text(text)):
        out.append(stringify(evaluate(Parser(part).parse(), ctx)) if kind == "expr" else part)
    return "".join(out)


def condition(value, ctx):
    return truthy(evaluate(Parser(condition_source(value)).parse(), ctx))


# ---------------------------------------------------------------------------
# Workflow model


def yq(args, path):
    proc = subprocess.run(["yq"] + args + [path], capture_output=True, text=True)
    if proc.returncode != 0:
        sys.exit(f"ci_steps: yq could not decode {path}: {proc.stderr.strip()}")
    return json.loads(proc.stdout)


def as_list(v):
    if v is None:
        return []
    return v if isinstance(v, list) else [v]


def public(mapping):
    return {k: v for k, v in mapping.items() if not k.startswith("__")}


def step_label(step):
    if "name" in step:
        return str(step["name"])
    if "run" in step:
        return str(step["run"]).strip().splitlines()[0]
    return str(step.get("uses", "step"))


class Workflow:
    def __init__(self, path, required):
        self.path = path
        self.doc = yq(["-o=json", YQ_ANNOTATE], path)
        self.marker_lines = set(yq(["-o=json", YQ_MARKERS], path))
        self.required = required
        self.jobs = self.doc.get("jobs") or {}
        self.job_lines = self.jobs.get("__keylines", {})
        self.findings = []
        self.where = ""
        self.order = []
        self.skips = {}

    def find(self, line, message):
        self.findings.append((line, message))

    def check(self):
        for key in public(self.doc):
            if key not in WORKFLOW_KEYS:
                self.find(self.doc["__keylines"][key], f"workflow key {key!r} is not one the runner models")
        env = self.doc.get("env") or {}
        for key, value in public(env).items():
            self.check_value(env["__keylines"][key], lambda v=value: check_template(v, Scope()), True)
        if self.required not in self.jobs:
            self.find(1, f"the aggregator job {self.required!r} does not exist")
            return
        self.order = self.required_order()
        consumed = set()
        for job_id in self.order:
            self.check_job(job_id, consumed)
        for line in sorted(self.marker_lines - consumed):
            self.find(
                line,
                f"a {MARKER} exception here annotates no step of a job "
                f"{self.required} requires, so it suppresses nothing; delete it",
            )

    def required_order(self):
        agg = self.jobs[self.required]
        wanted, stack = set(), list(as_list(agg.get("needs")))
        while stack:
            job_id = stack.pop()
            if job_id in wanted:
                continue
            if job_id not in self.jobs or job_id.startswith("__"):
                self.find(
                    agg["__keylines"].get("needs", agg["__line"]),
                    f"{self.required} needs job {job_id!r}, which does not exist",
                )
                continue
            wanted.add(job_id)
            stack.extend(as_list(self.jobs[job_id].get("needs")))
        ids = [j for j in public(self.jobs) if j in wanted]
        seen = set()
        for job_id in ids:
            job = self.jobs[job_id]
            for need in as_list(job.get("needs")):
                if need not in seen:
                    self.find(
                        job["__keylines"].get("needs", job["__line"]),
                        f"job {job_id} needs {need!r}, which is not declared before it; "
                        "the runner runs jobs in file order",
                    )
            seen.add(job_id)
        return ids

    def check_job(self, job_id, consumed):
        job = self.jobs[job_id]
        lines = job["__keylines"]
        self.where = f"job {job_id}: "
        for key in public(job):
            if key not in JOB_KEYS:
                self.find(lines[key], f"job {job_id}: key {key!r} is not one the runner models")
        needs = {
            n: set(public(self.jobs[n].get("outputs") or {}))
            for n in as_list(job.get("needs"))
            if n in self.jobs
        }
        strategy = job.get("strategy") or {}
        matrix = strategy.get("matrix") or {}
        for key in public(strategy):
            if key not in STRATEGY_KEYS:
                self.find(strategy["__keylines"][key], f"job {job_id}: strategy key {key!r} is not one the runner models")
        if isinstance(matrix, str):
            self.find(lines["strategy"], f"job {job_id}: a whole-matrix expression is not one the runner models")
            matrix = {}
        for key in ("include", "exclude"):
            if key in matrix:
                self.find(matrix["__keylines"][key], f"job {job_id}: matrix {key!r} is not one the runner models")
        scope = Scope(needs, [k for k in public(matrix) if k not in ("include", "exclude")])
        self.check_value(lines.get("if"), lambda: check_condition(job["if"], Scope(needs)), "if" in job)
        for key, value in public(matrix).items():
            self.check_value(matrix["__keylines"][key], lambda v=value: check_template(v, Scope(needs)), True)
        self.check_value(lines.get("name"), lambda: check_template(job["name"], scope), "name" in job)
        env = job.get("env") or {}
        for key, value in public(env).items():
            self.check_value(env["__keylines"][key], lambda v=value: check_template(v, scope), True)

        ids = []
        for index, step in enumerate(job.get("steps") or []):
            consumed.update(self.check_step(job_id, index, step, scope, ids))
            # Only a run step this runner executes writes GITHUB_OUTPUT; an
            # action's or a skipped step's outputs would read as empty here.
            if "id" in step and "run" in step and (job_id, index) not in self.skips:
                ids.append(step["id"])
        outputs = job.get("outputs") or {}
        for key, value in public(outputs).items():
            self.check_value(
                outputs["__keylines"][key],
                lambda v=value: check_template(v, Scope(needs, scope.matrix_keys, ids)),
                True,
            )

    def check_step(self, job_id, index, step, scope, ids):
        line = step["__line"]
        markers = [l.strip() for l in step["__head"].splitlines() if l.strip().startswith(MARKER)]
        consumed = {line} if markers else set()
        if markers:
            if len(markers) > 1:
                self.find(line, f"job {job_id}: a step takes at most one {MARKER} exception")
            m = EXCEPTION_RE.match(markers[0])
            body = str(step.get("run", step.get("uses", "")))
            if not m:
                self.find(
                    line,
                    f'job {job_id}: malformed exception; the form is `# {MARKER}: while "<text>": <reason>`',
                )
            elif m.group(1) not in body:
                self.find(
                    line,
                    f'job {job_id}: the exception\'s removal condition holds: the step no longer '
                    f'contains "{m.group(1)}"; delete the exception so check-all.sh runs the step',
                )
            else:
                self.skips[(job_id, index)] = m.group(2)
            return consumed
        for key in public(step):
            if key not in STEP_KEYS:
                self.find(step["__keylines"][key], f"job {job_id}: step key {key!r} is not one the runner models")
        if ("run" in step) == ("uses" in step):
            self.find(line, f"job {job_id}: a step needs exactly one of run and uses")
        if "uses" in step:
            action = str(step["uses"]).split("@")[0]
            if action not in ENVIRONMENT_ACTIONS:
                self.find(
                    step["__keylines"]["uses"],
                    f"job {job_id}: action {action} is not environment setup, so check-all.sh cannot run it",
                )
            return consumed
        step_scope = Scope(scope.needs, scope.matrix_keys, ids)
        for key in ("if", "run", "working-directory"):
            if key in step:
                check = check_condition if key == "if" else check_template
                self.check_value(step["__keylines"][key], lambda c=check, v=step[key]: c(v, step_scope), True)
        env = step.get("env") or {}
        for key, value in public(env).items():
            self.check_value(env["__keylines"][key], lambda v=value: check_template(v, step_scope), True)
        return consumed

    def check_value(self, line, fn, present):
        if not present:
            return
        try:
            fn()
        except ValueError as err:
            self.find(line, f"{self.where}{err}, so check-all.sh cannot evaluate this expression")

    def report(self):
        for line, message in sorted(self.findings):
            print(f"{self.path}:{line}: {message}", file=sys.stderr)
        if self.findings:
            print(
                f"\nEvery step of every job {self.required} requires must be one scripts/ci_steps.py\n"
                "runs as CI does. Fix: express the step with constructs the runner already\n"
                "models, or teach scripts/ci_steps.py the new construct. A step that cannot run\n"
                "on a developer machine takes, with the owner's approval, a comment line\n"
                f'directly above it: # {MARKER}: while "<text in its run>": <reason>',
                file=sys.stderr,
            )
        return not self.findings


# ---------------------------------------------------------------------------
# Execution


def git(*args):
    return subprocess.run(["git"] + list(args), capture_output=True, text=True, check=True).stdout.strip()


def read_outputs(path):
    outputs = {}
    with open(path) as f:
        for line in f.read().splitlines():
            if not line:
                continue
            if "=" not in line or "<<" in line.split("=", 1)[0]:
                raise RuntimeError(f"unsupported GITHUB_OUTPUT line {line!r}")
            key, value = line.split("=", 1)
            outputs[key] = value
    return outputs


def run(wf, base_ref):
    head = git("rev-parse", "HEAD")
    github = github_context(git("rev-parse", base_ref), head, git("branch", "--show-current"))
    workflow_env = {k: render(v, {"github": github}) for k, v in public(wf.doc.get("env") or {}).items()}
    results, root = {}, os.getcwd()
    for job_id in wf.order:
        job = wf.jobs[job_id]
        needs = as_list(job.get("needs"))
        ctx = {"github": github, "env": dict(workflow_env), "needs": {n: results[n] for n in needs if results[n]}}
        if any(results[n] is None for n in needs):
            print(f"--- skip {job_id}: a job it needs was skipped")
            results[job_id] = None
            continue
        if "if" in job and not condition(job["if"], ctx):
            print(f"--- skip {job_id}: if {condition_source(job['if']).strip()}")
            results[job_id] = None
            continue
        matrix = public((job.get("strategy") or {}).get("matrix") or {})
        axes = []
        for key, value in matrix.items():
            values = evaluate(Parser(condition_source(value)).parse(), ctx) if isinstance(value, str) else value
            axes.append([(key, v) for v in values])
        instances = [dict(combo) for combo in itertools.product(*axes)]
        outputs = {}
        for instance in instances:
            outputs = run_job(wf, job_id, dict(ctx, matrix=instance, env=dict(ctx["env"])), root)
        results[job_id] = {"outputs": outputs} if instances else None


def run_job(wf, job_id, ctx, root):
    job = wf.jobs[job_id]
    title = render(job["name"], ctx) if "name" in job else job_id
    for key, value in public(job.get("env") or {}).items():
        ctx["env"][key] = render(value, ctx)
    ctx["steps"] = {}
    for index, step in enumerate(job.get("steps") or []):
        label = f"{title} / {step_label(step)}"
        if (job_id, index) in wf.skips:
            print(f"--- not run: {label}: {wf.skips[(job_id, index)]}")
            continue
        if "uses" in step:
            continue
        step_ctx = dict(ctx, env=dict(ctx["env"]))
        for key, value in public(step.get("env") or {}).items():
            step_ctx["env"][key] = render(value, step_ctx)
        if "if" in step and not condition(step["if"], step_ctx):
            print(f"--- skip {label}: if {condition_source(step['if']).strip()}")
            continue
        print(f"==> {label}", flush=True)
        cwd = os.path.join(root, render(step.get("working-directory", "."), step_ctx))
        with tempfile.TemporaryDirectory() as tmp:
            script = os.path.join(tmp, "step.sh")
            output_file = os.path.join(tmp, "output")
            with open(script, "w") as f:
                f.write(render(step["run"], step_ctx))
            open(output_file, "w").close()
            env = dict(os.environ, **step_ctx["env"], GITHUB_OUTPUT=output_file)
            code = subprocess.call(["bash", "--noprofile", "--norc", "-eo", "pipefail", script], cwd=cwd, env=env)
            if code != 0:
                print(f"ci_steps: FAILED: {label} ({wf.path}:{step['__line']})", file=sys.stderr)
                sys.exit(1)
            if "id" in step:
                ctx["steps"][step["id"]] = {"outputs": read_outputs(output_file)}
    return {
        key: render(value, ctx)
        for key, value in public(job.get("outputs") or {}).items()
    }


def main(argv):
    if len(argv) < 3 or argv[0] not in ("check", "run") or (argv[0] == "run" and len(argv) != 4):
        sys.exit("usage: ci_steps.py check <workflow> <aggregator-job>\n"
                 "       ci_steps.py run <workflow> <aggregator-job> <base-ref>")
    wf = Workflow(argv[1], argv[2])
    wf.check()
    if not wf.report():
        return 1
    if argv[0] == "run":
        run(wf, argv[3])
    else:
        print(f"ci-parity check passed ({len(wf.order)} jobs required by {wf.required})")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
