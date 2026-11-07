"""Local-only integration checks for the Codex connector."""

from concurrent.futures import ThreadPoolExecutor
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest
from uuid import UUID


SCRIPT = Path(__file__).with_name("connector.py")
PROJECT = "10000000-0000-4000-8000-000000000001"
WORKSPACE = "20000000-0000-4000-8000-000000000002"
TASKS = {"tasks": [{"project_id": PROJECT, "session_id": "session-one", "status": "stopped", "events": []}]}
MEMORY = {"project_id": PROJECT, "version": "sha256:test-version", "content": "Remember the local acceptance result."}


class FakeHandler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def respond(self, status, value):
        data = json.dumps(value).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        assert self.path == "/api/agentharbor/sync"
        length = int(self.headers["Content-Length"])
        body = json.loads(self.rfile.read(length))
        self.server.requests.append((self.headers, body))
        ids = [event["event_id"] for event in body["events"]]
        if len(self.server.requests) == 1:
            ids = ids[:1]
        self.respond(200, {"accepted_event_ids": ids})

    def do_GET(self):
        self.server.get_paths.append(self.path)
        if self.path == f"/api/agentharbor/tasks?project_id={PROJECT}":
            self.respond(200, TASKS)
        elif self.path == f"/api/projects/{PROJECT}/memory-snapshot":
            self.respond(200, self.server.memory)
        else:
            self.respond(404, {"error": "not found"})


class ConnectorTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), FakeHandler)
        self.server.requests = []
        self.server.get_paths = []
        self.server.memory = MEMORY
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.env = os.environ.copy()
        self.env.update({
            "AGENTHARBOR_STATE_DIR": self.temp.name,
            "AGENTHARBOR_PROJECT_ID": PROJECT,
            "AGENTHARBOR_WORKSPACE_ID": WORKSPACE,
            "AGENTHARBOR_PAT": "fake-local-token",
            "AGENTHARBOR_URL": f"http://127.0.0.1:{self.server.server_port}",
        })

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
        self.temp.cleanup()

    def call(self, command, payload=None):
        return subprocess.run(
            [sys.executable, str(SCRIPT), command],
            input=None if payload is None else json.dumps(payload),
            capture_output=True,
            text=True,
            env=self.env,
            timeout=10,
        )

    def test_hook_queue_partial_confirmation_and_readback(self):
        prompt = self.call("hook", {
            "hook_event_name": "UserPromptSubmit", "session_id": "session-one",
            "turn_id": "turn-one", "prompt": "private prompt contents",
        })
        stop = self.call("hook", {
            "hook_event_name": "Stop", "session_id": "session-one",
            "turn_id": "turn-one", "last_assistant_message": "private answer contents",
        })
        self.assertEqual(prompt.returncode, 0, prompt.stderr)
        self.assertEqual(stop.returncode, 0, stop.stderr)
        self.assertEqual(json.loads(prompt.stdout), {})
        queue_path = Path(self.temp.name) / "queue.json"
        queue = json.loads(queue_path.read_text(encoding="utf-8"))
        self.assertEqual([event["local_seq"] for event in queue["events"]], [1, 2])
        self.assertEqual([event["event_type"] for event in queue["events"]], ["prompt_started", "turn_stopped"])
        self.assertEqual({event["agent_type"] for event in queue["events"]}, {"codex"})
        self.assertTrue(all(event["project_id"] == PROJECT for event in queue["events"]))
        UUID(queue["machine_id"])
        for event in queue["events"]:
            UUID(event["event_id"])
            self.assertEqual(event["machine_id"], queue["machine_id"])
            self.assertLessEqual(len(event["evidence"]), 512)
        self.assertNotIn("private", queue_path.read_text(encoding="utf-8"))
        self.assertNotIn("fake-local-token", queue_path.read_text(encoding="utf-8"))

        first = self.call("sync-once")
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertEqual(json.loads(first.stdout)["accepted_count"], 1)
        self.assertEqual(json.loads(queue_path.read_text(encoding="utf-8"))["events"][0]["local_seq"], 2)
        self.assertEqual(self.server.requests[0][0]["Authorization"], "Bearer fake-local-token")
        self.assertEqual(self.server.requests[0][0]["X-Workspace-ID"], WORKSPACE)
        self.assertEqual(self.server.requests[0][1]["machine"]["id"], queue["machine_id"])
        self.assertEqual(self.server.get_paths[:2], [
            f"/api/agentharbor/tasks?project_id={PROJECT}",
            f"/api/projects/{PROJECT}/memory-snapshot",
        ])
        tasks = self.call("tasks")
        self.assertEqual(tasks.returncode, 0, tasks.stderr)
        self.assertEqual(json.loads(tasks.stdout), TASKS)

        second = self.call("sync-once")
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(json.loads(queue_path.read_text(encoding="utf-8"))["events"], [])
        self.assertEqual(self.server.requests[1][1]["events"][0]["event_id"], queue["events"][1]["event_id"])

        next_prompt = self.call("hook", {
            "hook_event_name": "UserPromptSubmit", "session_id": "session-two",
            "turn_id": "turn-two", "prompt": "next prompt",
        })
        self.assertEqual(next_prompt.returncode, 0, next_prompt.stderr)
        context = json.loads(next_prompt.stdout)["hookSpecificOutput"]
        self.assertEqual(context["hookEventName"], "UserPromptSubmit")
        self.assertIn(MEMORY["content"], context["additionalContext"])
        self.assertIn(MEMORY["version"], context["additionalContext"])

    def test_concurrent_hooks_keep_unique_sequences_and_null_memory_is_empty(self):
        self.server.memory = {"project_id": PROJECT, "version": "empty", "content": None}
        synced = self.call("sync-once")
        self.assertEqual(synced.returncode, 0, synced.stderr)
        def submit(index):
            return self.call("hook", {
                "hook_event_name": "UserPromptSubmit", "session_id": f"session-{index}",
                "turn_id": f"turn-{index}", "prompt": "test",
            })
        with ThreadPoolExecutor(max_workers=8) as pool:
            results = list(pool.map(submit, range(8)))
        self.assertTrue(all(result.returncode == 0 for result in results), [result.stderr for result in results])
        self.assertTrue(all(json.loads(result.stdout) == {} for result in results))
        queue = json.loads((Path(self.temp.name) / "queue.json").read_text(encoding="utf-8"))
        self.assertEqual(sorted(event["local_seq"] for event in queue["events"]), list(range(1, 9)))

    def test_interrupt_hook_records_event(self):
        interrupted = self.call("hook", {
            "hook_event_name": "Interrupt", "session_id": "session-one", "turn_id": "turn-one",
        })
        self.assertEqual(interrupted.returncode, 0, interrupted.stderr)
        self.assertEqual(json.loads(interrupted.stdout), {})
        queue = json.loads((Path(self.temp.name) / "queue.json").read_text(encoding="utf-8"))
        self.assertEqual(queue["events"][0]["event_type"], "interrupted")


if __name__ == "__main__":
    unittest.main()
