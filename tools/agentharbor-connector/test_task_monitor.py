from contextlib import closing
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
from uuid import uuid4

from connector import ConnectorError
from task_monitor import monitor_once, monitor_projects


class CaptureServer:
    def __init__(self, project):
        self.project = project
        self.requests = []
        self.drop_ack = False

    def request(self, method, path, body):
        if (method, path) != ("POST", "/api/agentharbor/monitor/sync"):
            raise AssertionError("unexpected sync endpoint")
        self.requests.append(body)
        if self.drop_ack:
            raise ConnectorError("connection failed")
        return {"accepted_count": len(body["turns"])}


class MonitorTest(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self.project = self.root / "CIS 5480"
        self.project.mkdir()
        self.codex = self.root / "codex"
        (self.codex / "sessions").mkdir(parents=True)
        self.state = self.root / "connector"
        self.project_id = str(uuid4())
        binding = self.project / ".codex" / "agentharbor-project.json"
        binding.parent.mkdir()
        binding.write_text(json.dumps({"project_id": self.project_id, "local_root": str(self.project)}))
        self.server = CaptureServer(self.project_id)
        self.preferences = self.root / "monitor-settings.json"
        self.db = self.codex / "state_5.sqlite"
        with closing(sqlite3.connect(self.db)) as db, db:
            db.execute(
                "CREATE TABLE threads (id TEXT PRIMARY KEY, cwd TEXT, rollout_path TEXT,"
                " archived INTEGER, name TEXT, title TEXT, first_user_message TEXT)"
            )

    def thread(self, *, cwd=None, name=None, archived=False):
        thread = str(uuid4())
        path = self.codex / "sessions" / f"rollout-fixture-{thread}.jsonl"
        path.touch()
        with closing(sqlite3.connect(self.db)) as db, db:
            db.execute(
                "INSERT INTO threads VALUES (?, ?, ?, ?, ?, ?, ?)",
                (thread, str(cwd or self.project), str(path), int(archived), name,
                 "Private initial prompt", "Private initial prompt"),
            )
        return thread, path

    def event(self, file, kind, turn, *, start=None, end=None, timestamp=None, newline=True):
        payload = {"type": kind, "turn_id": turn}
        if start is not None:
            payload["started_at"] = start
        if end is not None:
            payload["completed_at"] = end
        with file.open("ab") as stream:
            stream.write(json.dumps({"type": "event_msg", "payload": payload, "timestamp": timestamp}).encode())
            if newline:
                stream.write(b"\n")

    def sync(self):
        return monitor_once(
            self.server, self.state, self.project, codex_home=self.codex,
            legacy_preferences=self.preferences,
        )

    def test_backfill_keeps_history_and_only_exact_known_handled_records(self):
        thread, file = self.thread()
        self.event(file, "task_complete", "turn-handled", start=100, end=102)
        self.event(file, "task_complete", "turn-unknown", start=103, end=104)
        self.event(file, "task_started", "turn-running", start=105)
        _, unrelated = self.thread(cwd=self.root / "CIS 5480-other")
        self.event(unrelated, "task_started", "turn-other", start=105)
        self.preferences.write_text(json.dumps({
            "dismissedItemIds": [f"{thread}:turn-handled"], "dismissedTurnIds": ["turn-unknown"],
        }))

        self.sync()
        body = self.server.requests[-1]
        turns = {turn["turn_id"]: turn for turn in body["turns"]}
        self.assertEqual(set(turns), {"turn-handled", "turn-unknown", "turn-running"})
        self.assertIs(turns["turn-handled"]["handled"], True)
        self.assertIsNone(turns["turn-unknown"]["handled"])
        self.assertIs(turns["turn-unknown"]["historical"], True)
        self.assertEqual(turns["turn-running"]["status"], "running")
        self.assertEqual(turns["turn-running"]["title"], "Codex task")
        self.assertNotIn("Private initial prompt", json.dumps(body))
        self.assertNotIn(str(self.project), json.dumps(body))
        self.sync()
        self.assertEqual(self.server.requests[-1]["turns"], [])

    def test_unterminated_then_archived_and_aborted_without_start(self):
        thread, file = self.thread(name="Explicit name")
        self.event(file, "task_started", "turn-running", start=100)
        self.event(file, "turn_aborted", "turn-unknown-start", timestamp="2099-01-01T00:00:00Z", newline=False)
        self.sync()
        self.assertEqual([turn["status"] for turn in self.server.requests[-1]["turns"]], ["running"])
        archived = self.codex / "archived_sessions" / file.name
        archived.parent.mkdir()
        file.rename(archived)
        with closing(sqlite3.connect(self.db)) as db, db:
            db.execute("UPDATE threads SET archived = 1 WHERE id = ?", (thread,))
        self.sync()
        self.assertEqual(self.server.requests[-1]["scan_status"], "ok")
        self.assertEqual(self.server.requests[-1]["turns"], [])
        with archived.open("ab") as stream:
            stream.write(b"\n")
        self.sync()
        [turn] = self.server.requests[-1]["turns"]
        self.assertEqual(turn["status"], "aborted")
        self.assertIsNone(turn["started_at"])
        self.assertEqual(turn["ended_at"], "2099-01-01T00:00:00.000Z")
        self.assertEqual(turn["title"], "Explicit name")

    def test_restart_resends_committed_terminal_with_original_occurrence_time(self):
        _, file = self.thread()
        self.event(file, "task_started", "turn-offline", start=100)
        self.sync()
        self.event(file, "task_complete", "turn-offline", end=4102444800)
        self.server.drop_ack = True
        with self.assertRaisesRegex(ConnectorError, "connection failed"):
            self.sync()
        first_attempt = self.server.requests[-1]
        self.server.drop_ack = False
        self.sync()
        self.assertEqual(self.server.requests[-1], first_attempt)
        [turn] = first_attempt["turns"]
        self.assertEqual(turn["status"], "completed")
        self.assertEqual(turn["started_at"], "1970-01-01T00:01:40.000Z")
        self.assertIs(turn["historical"], False)
        self.sync()
        self.assertEqual(self.server.requests[-1]["turns"], [])

    def test_missing_rollout_reports_degraded_without_emitting_a_terminal(self):
        _, file = self.thread()
        self.event(file, "task_started", "turn-running", start=100)
        self.sync()
        file.unlink()
        self.sync()
        self.assertEqual(self.server.requests[-1]["scan_status"], "degraded")
        self.assertEqual(self.server.requests[-1]["turns"], [])

    def test_wrong_binding_is_not_synced(self):
        self.server.project = str(uuid4())
        with self.assertRaisesRegex(ConnectorError, "project binding"):
            self.sync()
        self.assertEqual(self.server.requests, [])

    def test_all_bound_roots_are_imported_and_batches_are_bounded(self):
        second = self.root / "Moved project"
        (second / ".codex").mkdir(parents=True)
        (second / ".codex" / "agentharbor-project.json").write_text(
            json.dumps({"project_id": self.project_id, "local_root": str(second)}),
        )
        _, previous = self.thread(cwd=second)
        for index in range(101):
            self.event(previous, "task_complete", f"history-{index}", start=100, end=104)
        _, current = self.thread()
        self.event(current, "task_started", "current-task", start=105)

        result = monitor_once(
            self.server, self.state, (self.project, second), codex_home=self.codex,
            legacy_preferences=self.preferences,
        )
        self.assertEqual(result["accepted_count"], 102)
        self.assertEqual([len(request["turns"]) for request in self.server.requests], [100, 2])
        self.assertIn("current-task", {
            turn["turn_id"] for request in self.server.requests for turn in request["turns"]
        })

    def test_project_map_assigns_each_thread_to_one_project_and_catches_unbound(self):
        nested = self.project / "Separate repository"
        nested.mkdir()
        _, general = self.thread()
        _, separate = self.thread(cwd=nested)
        _, unknown = self.thread(cwd=self.root / "Other directory")
        self.event(general, "task_complete", "bound", start=10, end=11)
        self.event(separate, "task_complete", "nested", start=10, end=11)
        self.event(unknown, "task_complete", "unbound", start=10, end=11)
        nested_server = CaptureServer(str(uuid4()))
        catchall_server = CaptureServer(str(uuid4()))
        servers = {server.project: server for server in (self.server, nested_server, catchall_server)}
        mapping = self.root / "project-map.json"
        mapping.write_text(json.dumps({
            "projects": [
                {"project_id": self.project_id, "roots": [str(self.project)]},
                {"project_id": nested_server.project, "roots": [str(nested)]},
            ],
            "unassigned_project_id": catchall_server.project,
        }))

        monitor_projects(servers.__getitem__, self.state, mapping, codex_home=self.codex)
        for server, expected in zip(servers.values(), ("bound", "nested", "unbound")):
            [request] = server.requests
            [turn] = request["turns"]
            self.assertEqual(turn["turn_id"], expected)
            self.assertEqual(request["project_id"], server.project)
            self.assertNotIn(str(self.root), json.dumps(request))
        self.assertEqual(len({server.requests[0]["machine"]["id"] for server in servers.values()}), 1)

    def test_project_map_recovers_indexed_history_from_a_selected_archive(self):
        _, file = self.thread()
        self.event(file, "task_complete", "old-task", start=10, end=11)
        self.event(file, "task_started", "stale-running", start=12)
        archive = self.root / "Trusted Codex archive"
        archive.mkdir()
        file.rename(archive / file.name)
        unassigned = CaptureServer(str(uuid4()))
        mapping = self.root / "project-map.json"
        mapping.write_text(json.dumps({
            "projects": [{"project_id": self.project_id, "roots": [str(self.project)]}],
            "unassigned_project_id": unassigned.project,
            "rollout_archives": [str(archive)],
        }))

        clients = {self.project_id: self.server, unassigned.project: unassigned}
        monitor_projects(clients.__getitem__, self.state, mapping, codex_home=self.codex)
        [turn] = self.server.requests[-1]["turns"]
        self.assertEqual(turn["turn_id"], "old-task")
        self.assertIs(turn["historical"], True)

    def test_title_never_contains_prompt_fallbacks_or_paths(self):
        titles = {
            "Private initial prompt": "Codex task",
            "\tPrivate initial": "Codex task",
            "Review (/Users/alice/private)": "Codex task",
            "Review cwd=/home/alice/private": "Codex task",
            'Review "/tmp/private"': "Codex task",
            r"Review Q:\private": "Codex task",
            r"Review \\machine\private": "Codex task",
            "Review folder layout": "Review folder layout",
        }
        expected = {}
        for index, (name, title) in enumerate(titles.items()):
            _, file = self.thread(name=name)
            turn_id = f"title-{index}"
            self.event(file, "task_complete", turn_id, start=10, end=11)
            expected[turn_id] = title

        self.server.drop_ack = True
        with self.assertRaisesRegex(ConnectorError, "^connection failed$"):
            self.sync()
        pending = self.state / "monitor" / self.project_id / "state.json"
        queued = json.loads(pending.read_text(encoding="utf-8"))["pending"]
        posted = self.server.requests[-1]["turns"]
        for turns in (queued, posted):
            self.assertEqual({turn["turn_id"]: turn["title"] for turn in turns}, expected)

    def test_complete_unindexed_rollout_is_imported_before_sqlite_catches_up(self):
        thread = str(uuid4())
        rollout = self.codex / "sessions" / f"rollout-unindexed-{thread}.jsonl"
        rollout.write_text(json.dumps({
            "type": "session_meta", "payload": {"id": thread, "cwd": str(self.project)},
        }) + "\n", encoding="utf-8")
        self.event(rollout, "task_complete", "old-turn", start=10, end=11)
        unassigned = CaptureServer(str(uuid4()))
        mapping = self.root / "project-map.json"
        mapping.write_text(json.dumps({
            "projects": [{"project_id": self.project_id, "roots": [str(self.project)]}],
            "unassigned_project_id": unassigned.project,
        }))
        clients = {self.project_id: self.server, unassigned.project: unassigned}

        monitor_projects(clients.__getitem__, self.state, mapping, codex_home=self.codex)
        [turn] = self.server.requests[-1]["turns"]
        self.assertEqual(turn["session_id"], thread)
        self.assertEqual(turn["turn_id"], "old-turn")
        monitor_projects(clients.__getitem__, self.state, mapping, codex_home=self.codex)
        self.assertEqual(self.server.requests[-1]["turns"], [])
