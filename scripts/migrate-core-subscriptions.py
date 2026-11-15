#!/usr/bin/env python3
"""Back up and import legacy plugin memberships into core subscriptions."""

import argparse
import datetime as dt
import hashlib
import json
import re
import shutil
import sqlite3
import subprocess
import sys
import uuid
from pathlib import Path
from urllib.parse import urlparse


def read_manifest(path):
    data = json.loads(path.read_text())
    github = []
    slack = []
    for name in data.get("github_ledgers", []):
        ledger = Path(name)
        state = json.loads(ledger.read_text())
        if state.get("version") != 2 or not isinstance(state.get("subscriptions"), dict):
            raise ValueError(f"{ledger}: expected version 2 GitHub subscriptions")
        github.append((ledger, list(state["subscriptions"].values())))
    for entry in data.get("slack_ledgers", []):
        ledger = Path(entry["path"])
        state = json.loads(ledger.read_text())
        if state.get("version") != 2 or not isinstance(state.get("subscribers"), list):
            raise ValueError(f"{ledger}: expected version 2 Slack subscribers")
        for key in ("team_id", "bot_user_id", "workspace_host"):
            if not entry.get(key):
                raise ValueError(f"{ledger}: {key} is required")
        slack.append((ledger, entry, state["subscribers"]))
    return github, slack


def backup(data_home, manifest_path, github, slack):
    root = data_home / "subscription-migration-backups"
    root.mkdir(mode=0o700, exist_ok=True)
    stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    dest = root / stamp
    dest.mkdir(mode=0o700)
    db_path = data_home / "storage.db"
    with sqlite3.connect(db_path) as source, sqlite3.connect(dest / "storage.db") as target:
        source.backup(target)
    shutil.copy2(manifest_path, dest / "manifest.json")
    legacy = dest / "legacy"
    legacy.mkdir(mode=0o700)
    paths = [path for path, _ in github] + [path for path, _, _ in slack]
    paths += [data_home / "pending_delivery.json"]
    for path in paths:
        if path.is_file():
            digest = hashlib.sha256(str(path).encode()).hexdigest()[:12]
            shutil.copy2(path, legacy / f"{digest}-{path.name}")
    return dest


def live_session(db, name):
    return db.execute("SELECT id, project_root FROM sessions WHERE name = ? AND status <> 'destroyed'", (name,)).fetchone()


def resource_for(db, locator, identity):
    name = identity["name"]
    kind = identity["type"]
    cap = identity["max_subscribers"] or None
    row = db.execute("SELECT id, type, name FROM resources WHERE name = ?", (name,)).fetchone()
    if row:
        if row[1] != kind:
            raise ValueError(f"resource name {name!r} already belongs to type {row[1]!r}")
        duplicate = db.execute("SELECT id FROM resources WHERE locator = ? AND type = ''", (locator,)).fetchone()
        if duplicate and duplicate[0] != row[0]:
            raise ValueError(f"resource {locator!r} has separate typed and untyped rows")
        db.execute("UPDATE resources SET max_subscribers = ? WHERE id = ?", (cap, row[0]))
        return row[0]
    row = db.execute("SELECT id FROM resources WHERE locator = ? AND type = '' ORDER BY created_at LIMIT 1", (locator,)).fetchone()
    if row:
        db.execute("UPDATE resources SET type = ?, name = ?, max_subscribers = ? WHERE id = ?", (kind, name, cap, row[0]))
        return row[0]
    identity = uuid.uuid4().hex
    now = dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%f000Z")
    db.execute("INSERT INTO resources (id, type, name, locator, created_at, max_subscribers) VALUES (?, ?, ?, ?, ?, ?)",
               (identity, kind, name, locator, now, cap))
    return identity


def read_pending(path):
    if not path.is_file():
        return []
    state = json.loads(path.read_text())
    if not isinstance(state, dict) or not isinstance(state.get("subscribe", {}), dict):
        raise ValueError(f"{path}: invalid pending delivery")
    entries = []
    for name, locators in state.get("subscribe", {}).items():
        if not isinstance(locators, list):
            raise ValueError(f"{path}: invalid pending subscriptions for {name!r}")
        entries.extend((path, name, locator) for locator in locators)
    return entries


def plect_resolver(binary, data_home):
    def resolve(project_root, locator):
        command = [binary, "--data-home", str(data_home), "resource", "resolve", locator]
        if project_root:
            command += ["--project-root", project_root]
        result = subprocess.run(command, check=True, capture_output=True, text=True)
        return json.loads(result.stdout)
    return resolve


def validated_identity(resolve, project_root, locator):
    identity = resolve(project_root, locator)
    if not isinstance(identity, dict) or not isinstance(identity.get("type"), str) or not identity["type"] or not isinstance(identity.get("name"), str) or not identity["name"] or not isinstance(identity.get("max_subscribers", 0), int) or identity.get("max_subscribers", 0) < 0:
        raise ValueError("invalid resource declaration identity")
    identity.setdefault("max_subscribers", 0)
    return identity


def normalize_existing(db, resolve):
    rows = db.execute("""SELECT DISTINCT r.id, r.locator, s.project_root
                         FROM resources r JOIN subscriptions sub ON sub.resource_id = r.id
                         JOIN sessions s ON s.id = sub.session_id
                         WHERE r.type = '' AND s.status <> 'destroyed'""").fetchall()
    identities = {}
    for resource_id, locator, project_root in rows:
        identity = validated_identity(resolve, project_root, locator)
        previous = identities.get(resource_id)
        if previous is not None and previous != identity:
            raise ValueError(f"resource {locator!r} resolves differently across sessions")
        identities[resource_id] = identity
    for resource_id, locator, _ in rows:
        if resource_for(db, locator, identities[resource_id]) != resource_id:
            raise ValueError(f"resource {locator!r} has a conflicting stored identity")


def bound_source(db, session_id, resource_id):
    checks = (
        ("dispatch", "SELECT 1 FROM sessions WHERE id = ? AND resource_id = ?"),
        ("task", "SELECT 1 FROM task_instances WHERE session_id = ? AND resource_id = ? AND status <> 'cleaned'"),
        ("node", "SELECT 1 FROM node_executions WHERE session_id = ? AND subscription_id = ? AND status <> 'cleaned'"),
    )
    for source, query in checks:
        if db.execute(query, (session_id, resource_id)).fetchone():
            return source
    return "runtime"


def thread_locator(db, channel, timestamp, host):
    digits = timestamp.replace(".", "")
    if not re.fullmatch(r"\d{16}", digits) or not re.fullmatch(r"[A-Z0-9]+", channel):
        return None
    for (locator,) in db.execute("SELECT locator FROM resources WHERE locator LIKE ?", (f"%/archives/{channel}/p%",)):
        parsed = urlparse(locator)
        match = re.fullmatch(rf"/archives/{re.escape(channel)}/p(\d{{16}})", parsed.path)
        if match and (match.group(1) == digits or f"thread_ts={timestamp}" in parsed.query):
            return locator
    if not re.fullmatch(r"[A-Za-z0-9.-]+\.slack\.com", host):
        return None
    return f"https://{host}/archives/{channel}/p{digits}"


def import_rows(db, github, slack, pending, resolve):
    inserted = 0
    dropped = []
    entries = []
    for path, rows in github:
        for row in rows:
            if not isinstance(row, dict):
                dropped.append(f"{path}: malformed subscription")
                continue
            entries.append((path, row.get("session_name"), row.get("resource")))
    for path, info, rows in slack:
        for row in rows:
            if not isinstance(row, dict):
                dropped.append(f"{path}: malformed subscriber")
                continue
            thread = row.get("thread_ts")
            if thread == "mentions":
                locator = f"slack://{info['team_id']}/{info['bot_user_id']}"
            else:
                locator = thread_locator(db, row.get("channel_id", ""), thread or "", info["workspace_host"])
            entries.append((path, row.get("session_name"), locator))
    entries.extend(pending)
    for path, name, locator in entries:
        if not isinstance(name, str) or not name or not isinstance(locator, str) or not locator:
            dropped.append(f"{path}: missing session or resource identity")
            continue
        session = live_session(db, name)
        if not session:
            dropped.append(f"{path}: session {name!r} is absent or destroyed")
            continue
        try:
            identity = validated_identity(resolve, session[1], locator)
            resource_id = resource_for(db, locator, identity)
        except (ValueError, subprocess.CalledProcessError) as exc:
            if isinstance(exc, subprocess.CalledProcessError):
                exc = exc.stderr.strip() or str(exc)
            dropped.append(f"{path}: resource {locator!r}: {exc}")
            continue
        source = bound_source(db, session[0], resource_id)
        cap = db.execute("SELECT max_subscribers FROM resources WHERE id = ?", (resource_id,)).fetchone()[0]
        already = db.execute("SELECT 1 FROM subscriptions WHERE session_id = ? AND resource_id = ?", (session[0], resource_id)).fetchone()
        if cap and not already:
            count = db.execute("SELECT COUNT(DISTINCT session_id) FROM subscriptions WHERE resource_id = ?", (resource_id,)).fetchone()[0]
            if count >= cap:
                dropped.append(f"{path}: resource {locator!r} has reached max_subscribers ({cap})")
                continue
        if source != "runtime" and already:
            continue
        now = dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%f000Z")
        cursor = db.execute("INSERT INTO subscriptions (session_id, resource_id, source, inputs_json, created_at) VALUES (?, ?, ?, '{}', ?) ON CONFLICT DO NOTHING",
                            (session[0], resource_id, source, now))
        inserted += cursor.rowcount
    return inserted, dropped


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data-home", required=True, type=Path)
    parser.add_argument("--manifest", required=True, type=Path)
    parser.add_argument("--plect", default="plect")
    args = parser.parse_args(argv)
    github, slack = read_manifest(args.manifest)
    backed_up = backup(args.data_home, args.manifest, github, slack)
    print(f"Backup: {backed_up}")
    subprocess.run([args.plect, "--data-home", str(args.data_home), "storage", "migrate"], check=True)
    with sqlite3.connect(args.data_home / "storage.db") as db:
        db.execute("PRAGMA foreign_keys = ON")
        db.execute("BEGIN IMMEDIATE")
        normalize_existing(db, plect_resolver(args.plect, args.data_home))
        inserted, dropped = import_rows(db, github, slack, read_pending(args.data_home / "pending_delivery.json"), plect_resolver(args.plect, args.data_home))
    for reason in dropped:
        print(f"Dropped: {reason}", file=sys.stderr)
    print(f"Imported {inserted} subscription rows; dropped {len(dropped)}")


if __name__ == "__main__":
    main()
