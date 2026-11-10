import importlib.util
import json
import sqlite3
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("migrate-core-subscriptions.py")
spec = importlib.util.spec_from_file_location("subscription_migration", SCRIPT)
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)


class MigrationTest(unittest.TestCase):
    def test_import_preserves_live_membership_and_backs_up_first(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            db_path = root / "storage.db"
            with sqlite3.connect(db_path) as db:
                db.executescript("""
                    CREATE TABLE sessions (id TEXT PRIMARY KEY, name TEXT, status TEXT, resource_id TEXT);
                    CREATE TABLE resources (id TEXT PRIMARY KEY, type TEXT, name TEXT UNIQUE, locator TEXT, created_at TEXT, max_subscribers INTEGER);
                    CREATE TABLE task_instances (session_id TEXT, resource_id TEXT, status TEXT);
                    CREATE TABLE node_executions (session_id TEXT, subscription_id TEXT, status TEXT);
                    CREATE TABLE subscriptions (session_id TEXT, resource_id TEXT, source TEXT, inputs_json TEXT, created_at TEXT,
                        PRIMARY KEY (session_id, resource_id, source));
                    INSERT INTO sessions VALUES ('s1', 'one', 'down', 'r1');
                    INSERT INTO sessions VALUES ('s2', 'two', 'down', NULL);
                    INSERT INTO sessions VALUES ('s3', 'gone', 'destroyed', NULL);
                    INSERT INTO resources VALUES ('r1', 'issue', 'issue-1', 'https://github.com/o/r/issues/1', '2026-01-01T00:00:00.000000000Z', NULL);
                    INSERT INTO subscriptions VALUES ('s1', 'r1', 'dispatch', '{}', '2026-01-01T00:00:00.000000000Z');
                """)
            github_path = root / "subscriptions.json"
            github_path.write_text(json.dumps({"version": 2, "subscriptions": {
                "one": {"session_name": "one", "resource": "https://github.com/o/r/issues/1"},
                "two": {"session_name": "two", "resource": "https://github.com/o/r/pull/2"},
                "gone": {"session_name": "gone", "resource": "https://github.com/o/r/pull/3"},
            }}))
            slack_path = root / "subscribers.json"
            slack_path.write_text(json.dumps({"version": 2, "subscribers": [
                {"session_name": "two", "channel_id": "C123", "thread_ts": "1234567890.123456"},
                {"session_name": "one", "thread_ts": "mentions"},
                {"session_name": "two", "thread_ts": "mentions"},
            ]}))
            manifest = root / "manifest.json"
            manifest.write_text(json.dumps({"github_ledgers": [str(github_path)], "slack_ledgers": [{
                "path": str(slack_path), "team_id": "T123", "bot_user_id": "U123", "workspace_host": "example.slack.com"
            }]}))
            github, slack = migration.read_manifest(manifest)
            backup_dir = migration.backup(root, manifest, github, slack)
            self.assertTrue((backup_dir / "storage.db").is_file())
            self.assertEqual(len(list((backup_dir / "legacy").iterdir())), 2)
            with sqlite3.connect(db_path) as db:
                db.execute("BEGIN IMMEDIATE")
                inserted, dropped = migration.import_rows(db, github, slack)
            self.assertEqual(inserted, 3)
            self.assertEqual(len(dropped), 2)
            with sqlite3.connect(db_path) as db:
                rows = db.execute("SELECT s.name, r.locator, sub.source FROM subscriptions sub JOIN sessions s ON s.id = sub.session_id JOIN resources r ON r.id = sub.resource_id ORDER BY s.name, r.locator").fetchall()
            self.assertEqual(rows, [
                ("one", "https://github.com/o/r/issues/1", "dispatch"),
                ("one", "slack://T123/U123", "runtime"),
                ("two", "https://example.slack.com/archives/C123/p1234567890123456", "runtime"),
                ("two", "https://github.com/o/r/pull/2", "runtime"),
            ])


if __name__ == "__main__":
    unittest.main()
