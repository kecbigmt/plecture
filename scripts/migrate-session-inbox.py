#!/usr/bin/env python3
"""Back up storage and move events held in plugin queue files into the session inbox."""

import argparse
import datetime as dt
import json
import shutil
import sqlite3
import subprocess
import sys
from pathlib import Path


def backup(data_home, queue_dirs):
    root = data_home / "session-inbox-migration-backups"
    root.mkdir(mode=0o700, exist_ok=True)
    dest = root / dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    dest.mkdir(mode=0o700)
    with sqlite3.connect(data_home / "storage.db") as source, sqlite3.connect(dest / "storage.db") as target:
        source.backup(target)
    for index, queue in enumerate(sorted(queue_dirs)):
        for path in (queue, Path(str(queue) + ".quarantine")):
            if path.is_dir():
                shutil.copytree(path, dest / "queues" / str(index) / path.name)
        (dest / "queues" / str(index)).mkdir(parents=True, exist_ok=True)
        (dest / "queues" / str(index) / "source").write_text(str(queue) + "\n")
    return dest


def queue_dirs_by_session(db):
    """Every queue directory a live session's nodes ever reported."""
    rows = db.execute(
        """
        SELECT s.id, s.name, s.workflow_revision, json_extract(n.outputs_json, '$.queue_dir')
        FROM node_executions n JOIN sessions s ON s.id = n.session_id
        WHERE s.status <> 'destroyed' AND json_extract(n.outputs_json, '$.queue_dir') IS NOT NULL
        ORDER BY s.name, n.sequence
        """
    ).fetchall()
    sessions = {}
    for session_id, name, revision, queue_dir in rows:
        entry = sessions.setdefault(session_id, {"name": name, "revision": revision, "queues": []})
        if queue_dir and Path(queue_dir) not in entry["queues"]:
            entry["queues"].append(Path(queue_dir))
    return sessions


def queue_channel(db, revision):
    """The binding that fed the queue: the one whose inputs named a queue directory."""
    row = db.execute("SELECT body FROM workflow_revisions WHERE digest = ?", (revision,)).fetchone()
    if row is None:
        return None
    workflow = json.loads(row[0]).get("workflow") or {}
    for channel in (workflow.get("Event") or {}).get("Channel") or []:
        if "queue_dir" in (channel.get("Inputs") or {}):
            return channel.get("Name")
    return None


def pending_files(queue):
    """Pending and claimed entries, oldest first. Parked, completed and quarantined ones are left out."""
    files = sorted(queue.glob("*.json"))
    files += sorted((queue / ".inflight").glob("*.json"))
    return files


def leftovers(queue):
    found = [str(p) for p in sorted(queue.glob(".failed-*")) + sorted(queue.glob(".completed-*"))]
    quarantine = Path(str(queue) + ".quarantine")
    if quarantine.is_dir():
        found.append(str(quarantine))
    return found


def migrate(db, report):
    migrated = []
    for session_id, entry in queue_dirs_by_session(db).items():
        channel = queue_channel(db, entry["revision"]) if entry["revision"] else None
        for queue in entry["queues"]:
            if not queue.is_dir():
                continue
            for name in leftovers(queue):
                report(f"Left in place: {name} (session {entry['name']}; parked, completed or quarantined)")
            for path in pending_files(queue):
                try:
                    event_id = json.loads(path.read_text()).get("id") or ""
                except (ValueError, AttributeError) as err:
                    report(f"Not migrated: {path} (session {entry['name']}; unparsable: {err})")
                    continue
                if not event_id:
                    report(f"Not migrated: {path} (session {entry['name']}; carries no event id)")
                    continue
                if channel is None:
                    report(f"Not migrated: {path} (session {entry['name']}; no channel binding names a queue directory)")
                    continue
                row = db.execute(
                    "SELECT sequence FROM events WHERE session_id = ? AND id = ?", (session_id, event_id)
                ).fetchone()
                if row is None:
                    report(f"Not migrated: {path} (session {entry['name']}; event {event_id} is not in the log)")
                    continue
                db.execute(
                    "INSERT OR IGNORE INTO inbox_entries (session_id, sequence, channel) VALUES (?, ?, ?)",
                    (session_id, row[0], channel),
                )
                migrated.append(path)
                report(f"Migrated: {path} (session {entry['name']}; event {event_id})")
    return migrated


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data-home", required=True, type=Path)
    parser.add_argument("--plect", required=True, help="absolute path to the new plect binary")
    args = parser.parse_args()

    db_path = args.data_home / "storage.db"
    with sqlite3.connect(db_path) as db:
        queues = {q for entry in queue_dirs_by_session(db).values() for q in entry["queues"]}
    dest = backup(args.data_home, queues)
    print(f"Backup: {dest}")

    subprocess.run([args.plect, "--data-home", str(args.data_home), "storage", "migrate"], check=True)

    db = sqlite3.connect(db_path, isolation_level=None)
    try:
        db.execute("BEGIN IMMEDIATE")
        migrated = migrate(db, print)
        db.execute("COMMIT")
    except Exception:
        db.execute("ROLLBACK")
        raise
    finally:
        db.close()
    # Renamed only after the commit, so a second run cannot hand over an entry
    # the runtime has acknowledged in between.
    for path in migrated:
        path.rename(path.with_name(path.name + ".migrated"))
    print(f"Migrated {len(migrated)} queue entries")
    return 0


if __name__ == "__main__":
    sys.exit(main())
