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
    return db.execute("SELECT id FROM sessions WHERE name = ? AND status <> 'destroyed'", (name,)).fetchone()


def resource_for(db, locator):
    row = db.execute("SELECT id FROM resources WHERE locator = ? ORDER BY created_at LIMIT 1", (locator,)).fetchone()
    if row:
        return row[0]
    identity = uuid.uuid4().hex
    now = dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%f000Z")
    db.execute("INSERT INTO resources (id, type, name, locator, created_at) VALUES (?, '', ?, ?, ?)",
               (identity, identity, locator, now))
    return identity


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


def import_rows(db, github, slack):
    inserted = 0
    dropped = []
    entries = []
    for path, rows in github:
        for row in rows:
            if not isinstance(row, dict):
                dropped.append(f"{path}: malformed subscription")
                continue
            entries.append((path, row.get("session_name"), row.get("resource"), False))
    for path, info, rows in slack:
        for row in rows:
            if not isinstance(row, dict):
                dropped.append(f"{path}: malformed subscriber")
                continue
            thread = row.get("thread_ts")
            if thread == "mentions":
                locator = f"slack://{info['team_id']}/{info['bot_user_id']}"
                limited = True
            else:
                locator = thread_locator(db, row.get("channel_id", ""), thread or "", info["workspace_host"])
                limited = False
            entries.append((path, row.get("session_name"), locator, limited))
    for path, name, locator, limited in entries:
        if not isinstance(name, str) or not name or not isinstance(locator, str) or not locator:
            dropped.append(f"{path}: missing session or resource identity")
            continue
        session = live_session(db, name)
        if not session:
            dropped.append(f"{path}: session {name!r} is absent or destroyed")
            continue
        resource_id = resource_for(db, locator)
        if limited:
            db.execute("UPDATE resources SET max_subscribers = 1 WHERE id = ?", (resource_id,))
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
        inserted, dropped = import_rows(db, github, slack)
    for reason in dropped:
        print(f"Dropped: {reason}", file=sys.stderr)
    print(f"Imported {inserted} subscription rows; dropped {len(dropped)}")


if __name__ == "__main__":
    main()
