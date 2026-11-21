import importlib.util
import json
import sqlite3
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("migrate-session-inbox.py")
spec = importlib.util.spec_from_file_location("session_inbox_migration", SCRIPT)
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)

SCHEMA = """
CREATE TABLE sessions (id TEXT PRIMARY KEY, name TEXT, status TEXT, workflow_revision TEXT);
CREATE TABLE workflow_revisions (digest TEXT PRIMARY KEY, body TEXT);
CREATE TABLE node_executions (session_id TEXT, sequence INTEGER, outputs_json TEXT);
CREATE TABLE events (id TEXT PRIMARY KEY, session_id TEXT, sequence INTEGER);
CREATE TABLE inbox_entries (
    session_id TEXT NOT NULL, sequence INTEGER NOT NULL, channel TEXT NOT NULL,
    batch_id TEXT, deliveries INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (session_id, sequence)
);
"""


def revision(channel_name):
    return json.dumps({"workflow": {"Event": {"Channel": [
        {"Name": "mirror", "Uses": "post", "Inputs": {"thread": {}}},
        {"Name": channel_name, "Uses": "exec_delivery", "Inputs": {"queue_dir": {}}},
    ]}}})


class SessionInboxMigrationTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.queue = self.root / "state" / "queue"
        (self.queue / ".inflight").mkdir(parents=True)
        self.db = sqlite3.connect(":memory:")
        self.addCleanup(self.db.close)
        self.db.executescript(SCHEMA)
        self.db.execute("INSERT INTO sessions VALUES ('s1', 'review-1', 'down', 'rev1')")
        self.db.execute("INSERT INTO workflow_revisions VALUES ('rev1', ?)", (revision("runtime"),))
        self.db.execute(
            "INSERT INTO node_executions VALUES ('s1', 1, ?)", (json.dumps({"queue_dir": str(self.queue)}),)
        )
        for sequence, event_id in enumerate(["e1", "e2", "e3", "e4"], start=1):
            self.db.execute("INSERT INTO events VALUES (?, 's1', ?)", (event_id, sequence))
        self.report = []

    def write(self, relative, payload):
        path = self.queue / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(payload if isinstance(payload, str) else json.dumps(payload))
        return path

    def entries(self):
        return self.db.execute("SELECT session_id, sequence, channel, batch_id FROM inbox_entries ORDER BY sequence").fetchall()

    def test_pending_and_claimed_files_become_unclaimed_entries_of_the_queue_binding(self):
        pending = self.write("2.json", {"id": "e3", "type": "user.emit", "text": "later"})
        claimed = self.write(".inflight/1.json", {"id": "e2", "type": "user.emit", "text": "claimed"})

        migrated = migration.migrate(self.db, self.report.append)

        self.assertEqual(self.entries(), [("s1", 2, "runtime", None), ("s1", 3, "runtime", None)])
        self.assertEqual(sorted(migrated), sorted([pending, claimed]))

    def test_files_core_cannot_place_are_reported_and_left_out(self):
        self.write("1.json", {"type": "channel.message", "text": "from the bridge"})
        self.write("2.json", "{not json")
        self.write("3.json", {"id": "gone", "type": "user.emit"})
        self.write(".failed-7/1.json", {"id": "e1"})
        quarantine = Path(str(self.queue) + ".quarantine")
        quarantine.mkdir()

        migrated = migration.migrate(self.db, self.report.append)

        self.assertEqual(migrated, [])
        self.assertEqual(self.entries(), [])
        joined = "\n".join(self.report)
        self.assertIn("carries no event id", joined)
        self.assertIn("unparsable", joined)
        self.assertIn("event gone is not in the log", joined)
        self.assertIn(".failed-7", joined)
        self.assertIn(str(quarantine), joined)

    def test_destroyed_sessions_are_skipped(self):
        self.db.execute("UPDATE sessions SET status = 'destroyed'")
        self.write("1.json", {"id": "e1"})

        self.assertEqual(migration.migrate(self.db, self.report.append), [])
        self.assertEqual(self.entries(), [])

    def test_a_session_without_a_queue_binding_is_reported(self):
        self.db.execute("UPDATE workflow_revisions SET body = ?", (json.dumps({"workflow": {"Event": {"Channel": []}}}),))
        self.write("1.json", {"id": "e1"})

        self.assertEqual(migration.migrate(self.db, self.report.append), [])
        self.assertIn("no channel binding names a queue directory", "\n".join(self.report))

    def test_an_event_already_in_the_inbox_is_not_duplicated(self):
        self.db.execute("INSERT INTO inbox_entries (session_id, sequence, channel, batch_id) VALUES ('s1', 1, 'runtime', 'b1')")
        self.write("1.json", {"id": "e1"})

        migration.migrate(self.db, self.report.append)

        self.assertEqual(self.entries(), [("s1", 1, "runtime", "b1")])

    def test_backup_copies_the_database_and_every_queue_directory(self):
        data_home = self.root / "data"
        data_home.mkdir()
        with sqlite3.connect(data_home / "storage.db") as db:
            db.execute("CREATE TABLE marker (v TEXT)")
            db.execute("INSERT INTO marker VALUES ('kept')")
        self.write("1.json", {"id": "e1"})

        dest = migration.backup(data_home, {self.queue})

        with sqlite3.connect(dest / "storage.db") as copy:
            self.assertEqual(copy.execute("SELECT v FROM marker").fetchone(), ("kept",))
        self.assertTrue((dest / "queues" / "0" / "queue" / "1.json").is_file())
        self.assertEqual((dest / "queues" / "0" / "source").read_text().strip(), str(self.queue))


if __name__ == "__main__":
    unittest.main()
