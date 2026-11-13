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
import time
import unittest
from urllib.parse import parse_qs, urlsplit
from uuid import UUID


SCRIPT = Path(__file__).with_name("connector.py")
MCP_SCRIPT = SCRIPT.with_name("memory_mcp.py")
PROJECT = "10000000-0000-4000-8000-000000000001"
WORKSPACE = "20000000-0000-4000-8000-000000000002"
ENTRY = "30000000-0000-4000-8000-000000000003"
MATERIAL = "40000000-0000-4000-8000-000000000004"
TASKS = {"tasks": [{"project_id": PROJECT, "session_id": "session-one", "status": "stopped", "events": []}]}
MEMORY_ENTRY = {
    "id": ENTRY, "project_id": PROJECT, "scope": "project", "type": "procedural",
    "title": "Local checks", "content": "运行本地测试", "status": "active",
    "revision": 2, "source_material_ids": [MATERIAL],
}
MEMORY = {
    "project_id": PROJECT, "version": "a" * 64,
    "content": "## Local checks\n运行本地测试", "entries": [MEMORY_ENTRY],
}
EMPTY_MEMORY = {"project_id": PROJECT, "version": "", "content": "", "entries": []}
DISPUTE = {
    "entry": MEMORY_ENTRY,
    "report": {
        "related_entry_id": ENTRY, "related_revision": 2, "project_id": PROJECT,
        "source_text": "Check only public types", "context": "",
    },
}
MEMORY_API = "/api/agentharbor/memory/"


class FakeHandler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def respond(self, status, value):
        data = json.dumps(value).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        try:
            self.end_headers()
            self.wfile.write(data)
        except ConnectionError:
            pass

    def do_POST(self):
        length = int(self.headers["Content-Length"])
        body = json.loads(self.rfile.read(length))
        if self.path == "/api/agentharbor/sync" and length > 256 * 1024:
            self.respond(400, {"error": "request body too large"})
            return
        if self.path == "/api/agentharbor/sync":
            self.server.requests.append((self.headers, body))
            ids = [event["event_id"] for event in body["events"]]
            if len(self.server.requests) == 1:
                ids = ids[:1]
            self.respond(200, {"accepted_event_ids": ids})
        elif self.path == MEMORY_API + "materials":
            self.server.material_requests.append((self.headers, body))
            if len(self.server.material_requests) == 1:
                self.respond(503, {"error": "simulated loss of submission confirmation"})
            else:
                self.respond(200, {"id": MATERIAL, "project_id": PROJECT, "status": "pending", **body})
        else:
            self.respond(404, {"error": "not found"})

    def do_GET(self):
        self.server.get_paths.append(self.path)
        if self.path == f"/api/agentharbor/tasks?project_id={PROJECT}":
            self.respond(200, TASKS)
        elif self.path == MEMORY_API + "context":
            self.server.context_headers.append(self.headers)
            time.sleep(self.server.context_delay)
            self.respond(200, self.server.memory)
        elif urlsplit(self.path).path == MEMORY_API + "entries":
            self.server.search_headers.append(self.headers)
            self.respond(200, {"entries": [MEMORY_ENTRY], "disputes": [DISPUTE]})
        else:
            self.respond(404, {"error": "not found"})


class ConnectorTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.directory = Path(self.temp.name)
        self.project_dir = self.directory / "project"
        self.project_dir.mkdir()
        config = self.directory / "project.json"
        config.write_text(json.dumps({"local_root": str(self.project_dir), "project_id": PROJECT}), encoding="utf-8")
        self.token_file = self.directory / "token.json"
        self.token_file.write_text(json.dumps({"project_id": PROJECT, "token": "ahm_fake-project-token"}), encoding="utf-8")
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), FakeHandler)
        self.server.requests = []
        self.server.get_paths = []
        self.server.context_headers = []
        self.server.search_headers = []
        self.server.material_requests = []
        self.server.context_delay = 0
        self.server.memory = MEMORY
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        server_url = f"http://127.0.0.1:{self.server.server_port}"
        self.env = os.environ.copy()
        self.env.update({
            "AGENTHARBOR_STATE_DIR": self.temp.name,
            "AGENTHARBOR_PROJECT_ID": PROJECT,
            "AGENTHARBOR_WORKSPACE_ID": WORKSPACE,
            "AGENTHARBOR_PAT": "fake-local-token",
            "AGENTHARBOR_URL": server_url,
            "AGENTHARBOR_API_URL": server_url,
            "AGENTHARBOR_PROJECT_CONFIG": str(config),
            "AGENTHARBOR_MEMORY_TOKEN_FILE": str(self.token_file),
        })

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
        self.temp.cleanup()

    def call(self, command, payload=None, timeout=10):
        return subprocess.run(
            [sys.executable, str(SCRIPT), command],
            input=None if payload is None else json.dumps(payload),
            capture_output=True, text=True, encoding="utf-8",
            cwd=self.project_dir, env=self.env, timeout=timeout,
        )

    def prompt(self, session="session-one", turn="turn-one", cwd=None, timeout=10):
        return self.call("hook", {
            "hook_event_name": "UserPromptSubmit", "session_id": session,
            "turn_id": turn, "cwd": str(cwd or self.project_dir), "prompt": "private prompt contents",
        }, timeout=timeout)

    def call_mcp(self, *messages, cwd=None):
        request_lines = [
            json.dumps({"jsonrpc": "2.0", "id": index, "method": method, "params": params})
            for index, (method, params) in enumerate(messages)
        ]
        result = subprocess.run(
            [sys.executable, str(MCP_SCRIPT)], input="\n".join(request_lines) + "\n",
            capture_output=True, text=True, encoding="utf-8", cwd=cwd or self.project_dir,
            env=self.env, timeout=10,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        replies = [json.loads(line) for line in result.stdout.splitlines()]
        self.assertEqual([reply["id"] for reply in replies], list(range(len(messages))))
        return [reply["result"] for reply in replies]

    def test_hook_queue_partial_confirmation_and_readback(self):
        del self.env["AGENTHARBOR_PROJECT_CONFIG"]
        prompt = self.prompt()
        stop = self.call("hook", {
            "hook_event_name": "Stop", "session_id": "session-one",
            "turn_id": "turn-one", "last_assistant_message": "private answer contents",
        })
        self.assertEqual(prompt.returncode, 0, prompt.stderr)
        self.assertEqual(stop.returncode, 0, stop.stderr)
        self.assertEqual(json.loads(prompt.stdout), {})
        queue_path = self.directory / "queue.json"
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
        self.assertEqual(self.server.get_paths, [f"/api/agentharbor/tasks?project_id={PROJECT}"])
        tasks = self.call("tasks")
        self.assertEqual(tasks.returncode, 0, tasks.stderr)
        self.assertEqual(json.loads(tasks.stdout), TASKS)

        second = self.call("sync-once")
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(json.loads(queue_path.read_text(encoding="utf-8"))["events"], [])
        self.assertEqual(self.server.requests[1][1]["events"][0]["event_id"], queue["events"][1]["event_id"])

    def test_startup_context_is_fetched_fresh_once_per_session(self):
        first = self.prompt()
        self.assertEqual(first.returncode, 0, first.stderr)
        context = json.loads(first.stdout).get("hookSpecificOutput", {})
        self.assertEqual(context.get("hookEventName"), "UserPromptSubmit")
        self.assertIn(MEMORY["content"], context["additionalContext"])
        self.assertIn(MEMORY["version"], context["additionalContext"])
        self.assertIn("session-one", context["additionalContext"])
        self.assertEqual(self.server.context_headers[0]["Authorization"], "Bearer ahm_fake-project-token")
        self.assertIsNone(self.server.context_headers[0]["X-Workspace-ID"])

        self.server.memory = {**MEMORY, "content": "## Local checks\nUpdated checks", "version": "b" * 64}
        repeated = self.prompt(turn="turn-two")
        self.assertEqual(repeated.returncode, 0, repeated.stderr)
        self.assertEqual(json.loads(repeated.stdout), {})
        self.assertEqual(self.server.get_paths, [MEMORY_API + "context"])

        new_session = self.prompt(session="session-two")
        self.assertEqual(new_session.returncode, 0, new_session.stderr)
        context = json.loads(new_session.stdout)["hookSpecificOutput"]["additionalContext"]
        self.assertIn(self.server.memory["content"], context)
        self.assertEqual(self.server.get_paths, [MEMORY_API + "context"] * 2)

        receipts = [
            json.loads(path.read_text(encoding="utf-8"))
            for path in (self.directory / "memory-reads").rglob("*.json")
        ]
        first_receipt = next(receipt for receipt in receipts if receipt["session_id"] == "session-one")
        self.assertEqual(first_receipt, {
            "project_id": PROJECT, "session_id": "session-one", "trigger": "startup",
            "fetched": True, "emitted": True, "context_emitted": True, "version": MEMORY["version"],
            "entries": [{"id": ENTRY, "revision": 2}],
        })

    def test_context_timeout_does_not_block_task_sync_or_read_old_snapshot(self):
        self.server.context_delay = 3.5
        (self.directory / f"memory-{PROJECT}.json").write_text(json.dumps(MEMORY), encoding="utf-8")
        prompt = self.prompt(timeout=3)
        self.assertEqual(prompt.returncode, 0, prompt.stderr)
        self.assertIn("session-one", json.loads(prompt.stdout)["hookSpecificOutput"]["additionalContext"])
        repeated = self.prompt(turn="turn-two", timeout=3)
        self.assertEqual(repeated.returncode, 0, repeated.stderr)
        self.assertEqual(json.loads(repeated.stdout), {})

        sync = self.call("sync-once")
        self.assertEqual(sync.returncode, 0, sync.stderr)
        self.assertEqual(json.loads(sync.stdout), {"accepted_count": 1, "pending_count": 2, "task_count": 1})
        event, = [event for event in self.server.requests[0][1]["events"] if event["event_type"] == "memory_read"]
        self.assertEqual(json.loads(event["evidence"]), {
            "trigger": "startup", "fetched": False, "emitted": True, "context_emitted": False,
            "version": "", "entries": [],
        })
        self.assertEqual(self.server.get_paths, [MEMORY_API + "context", f"/api/agentharbor/tasks?project_id={PROJECT}"])
        receipt_path, = (self.directory / "memory-reads").rglob("*.json")
        receipt = json.loads(receipt_path.read_text(encoding="utf-8"))
        self.assertFalse(receipt["fetched"])
        self.assertTrue(receipt["emitted"])
        self.assertFalse(receipt["context_emitted"])
        self.assertEqual(receipt["entries"], [])

    def test_context_is_not_emitted_for_a_different_project(self):
        self.server.memory = {**MEMORY, "project_id": WORKSPACE}
        prompt = self.prompt()
        self.assertEqual(prompt.returncode, 0, prompt.stderr)
        self.assertNotIn(MEMORY["content"], json.loads(prompt.stdout)["hookSpecificOutput"]["additionalContext"])
        self.assertEqual(self.server.get_paths, [MEMORY_API + "context"])

    def test_concurrent_hooks_keep_unique_sequences_with_empty_context(self):
        self.server.memory = EMPTY_MEMORY

        def submit(index):
            return self.prompt(session=f"session-{index}", turn=f"turn-{index}")

        with ThreadPoolExecutor(max_workers=8) as pool:
            results = list(pool.map(submit, range(8)))
        self.assertTrue(all(result.returncode == 0 for result in results), [result.stderr for result in results])
        for index, result in enumerate(results):
            guidance = json.loads(result.stdout)["hookSpecificOutput"]["additionalContext"]
            self.assertIn(f"session-{index}", guidance)
            self.assertIn("search_project_memory", guidance)
            self.assertIn("submit_project_memory_material", guidance)
        queue = json.loads((self.directory / "queue.json").read_text(encoding="utf-8"))
        self.assertEqual(sorted(event["local_seq"] for event in queue["events"]), list(range(1, 17)))
        receipts = [
            json.loads(path.read_text(encoding="utf-8"))
            for path in (self.directory / "memory-reads").rglob("*.json")
        ]
        self.assertEqual(len(receipts), 8)
        self.assertTrue(all(receipt["fetched"] and receipt["emitted"] and not receipt["context_emitted"] for receipt in receipts))

    def test_simultaneous_prompts_in_one_session_share_one_context_read(self):
        with ThreadPoolExecutor(max_workers=2) as pool:
            results = list(pool.map(lambda index: self.prompt(turn=f"turn-{index}"), range(2)))
        self.assertTrue(all(result.returncode == 0 for result in results), [result.stderr for result in results])
        self.assertEqual(sum("hookSpecificOutput" in json.loads(result.stdout) for result in results), 1)
        self.assertEqual(self.server.get_paths, [MEMORY_API + "context"])

    def test_interrupt_hook_records_event(self):
        interrupted = self.call("hook", {
            "hook_event_name": "Interrupt", "session_id": "session-one", "turn_id": "turn-one",
        })
        self.assertEqual(interrupted.returncode, 0, interrupted.stderr)
        self.assertEqual(json.loads(interrupted.stdout), {})
        queue = json.loads((self.directory / "queue.json").read_text(encoding="utf-8"))
        self.assertEqual(queue["events"][0]["event_type"], "interrupted")
        self.assertEqual(self.server.get_paths, [])

    def test_startup_receipt_syncs_with_exact_entry_refs_without_memory_body(self):
        entry_two = {**MEMORY_ENTRY, "id": "40000000-0000-4000-8000-000000000007", "revision": 3}
        self.server.memory = {**MEMORY, "entries": [MEMORY_ENTRY, entry_two]}
        prompt = self.prompt()
        self.assertEqual(prompt.returncode, 0, prompt.stderr)

        first = self.call("sync-once")
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertEqual(json.loads(first.stdout)["pending_count"], 1)
        events = self.server.requests[0][1]["events"]
        self.assertEqual([event["event_type"] for event in events], ["prompt_started", "memory_read"])
        receipt = events[1]
        self.assertEqual(receipt["project_id"], PROJECT)
        self.assertEqual(receipt["machine_id"], events[0]["machine_id"])
        self.assertEqual(receipt["session_id"], "session-one")
        self.assertEqual(receipt["turn_id"], "turn-one")
        self.assertGreaterEqual(receipt["occurred_at"], events[0]["occurred_at"])
        self.assertEqual(json.loads(receipt["evidence"]), {
            "trigger": "startup", "fetched": True, "emitted": True, "context_emitted": True,
            "version": self.server.memory["version"],
            "entries": [{"id": ENTRY, "revision": 2}, {"id": entry_two["id"], "revision": 3}],
        })

        second = self.call("sync-once")
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(json.loads(second.stdout)["pending_count"], 0)
        self.assertEqual(self.server.requests[1][1]["events"], [receipt])

    def test_receipt_backlog_is_sent_in_bounded_batches(self):
        self.assertEqual(self.prompt().returncode, 0)
        queue_path = self.directory / "queue.json"
        queue = json.loads(queue_path.read_text(encoding="utf-8"))
        receipt = {**queue["events"][0], "event_type": "memory_read"}
        receipt["evidence"] = json.dumps({
            "trigger": "startup", "fetched": True, "emitted": True, "context_emitted": True,
            "version": "a" * 64, "entries": [
                {"id": str(UUID(int=index)), "revision": 1} for index in range(1, 101)
            ],
        }, separators=(",", ":"))
        for seq in range(3, 55):
            queue["events"].append({
                **receipt, "event_id": str(UUID(int=seq)), "local_seq": seq, "session_id": f"session-{seq}",
            })
        queue["next_seq"] = 55
        queue_path.write_text(json.dumps(queue), encoding="utf-8")
        # Leave all confirmations enabled to test batch boundaries independently.
        self.server.requests.append(({}, {}))
        first = self.call("sync-once")
        self.assertEqual(first.returncode, 0, first.stderr)
        first_batch = self.server.requests[1][1]["events"]
        self.assertLess(len(first_batch), len(queue["events"]))
        self.assertEqual(first_batch, queue["events"][:len(first_batch)])

        second = self.call("sync-once")
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(self.server.requests[2][1]["events"], queue["events"][len(first_batch):])
        self.assertEqual(json.loads(second.stdout)["pending_count"], 0)

    def test_mcp_submits_pending_material_with_retry_id(self):
        arguments = {
            "source_text": "约定运行本地测试", "context": "Confirmed in the setup discussion",
            "session_id": "session-one", "turn_id": "turn-one", "related_entry_id": ENTRY, "related_revision": 2,
        }
        name = "submit_project_memory_material"
        failed, = self.call_mcp(("tools/call", {"name": name, "arguments": arguments}))
        self.assertTrue(failed.get("isError"), failed)
        self.assertEqual(len(self.server.material_requests), 1)
        request_id = json.loads(failed["content"][0]["text"])["request_id"]
        UUID(request_id)
        retried, = self.call_mcp(("tools/call", {"name": name, "arguments": {**arguments, "request_id": request_id}}))
        self.assertFalse(retried.get("isError", False), retried)
        material = json.loads(retried["content"][0]["text"])
        self.assertEqual(material["id"], MATERIAL)
        self.assertEqual(material["status"], "pending")
        self.assertEqual(material["request_id"], request_id)
        self.assertEqual([body for _, body in self.server.material_requests], [{**arguments, "request_id": request_id}] * 2)
        self.assertEqual(self.server.material_requests[0][0]["Authorization"], "Bearer ahm_fake-project-token")

    def test_mcp_search_returns_typed_entries_with_provenance(self):
        query = "本地 + checks"
        initialized, catalog, response = self.call_mcp(
            ("initialize", {"protocolVersion": "2025-11-25"}),
            ("tools/list", {}),
            ("tools/call", {"name": "search_project_memory", "arguments": {"query": query, "type": "procedural"}}),
        )
        self.assertEqual(initialized["protocolVersion"], "2025-11-25")
        self.assertEqual(
            {tool["name"]: tool["annotations"]["readOnlyHint"] for tool in catalog["tools"]},
            {"search_project_memory": True, "submit_project_memory_material": False},
        )
        self.assertFalse(response.get("isError", False), response)
        self.assertEqual(json.loads(response["content"][0]["text"]), {"entries": [MEMORY_ENTRY], "disputes": [DISPUTE]})
        path, = self.server.get_paths
        self.assertEqual(urlsplit(path).path, MEMORY_API + "entries")
        self.assertEqual(parse_qs(urlsplit(path).query), {"query": [query], "type": ["procedural"]})
        self.assertEqual(self.server.search_headers[0]["Authorization"], "Bearer ahm_fake-project-token")

    def test_mcp_rejects_project_selector_and_working_directory_mismatch(self):
        selector, = self.call_mcp(("tools/call", {"name": "search_project_memory", "arguments": {"project_id": WORKSPACE}}))
        self.assertTrue(selector.get("isError"), selector)
        outside, = self.call_mcp(("tools/call", {"name": "search_project_memory"}), cwd=self.directory)
        self.assertTrue(outside.get("isError"), outside)
        self.assertEqual(self.server.get_paths, [])


if __name__ == "__main__":
    unittest.main()
