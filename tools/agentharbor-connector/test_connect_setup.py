"""Local installation checks using fake project memory credentials."""

import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import patch

import connector
from connect_setup import connect


PROJECT_ID = "ba9128a4-51b5-4f8a-8449-c9696e3a9def"
OTHER_PROJECT_ID = "10d94c28-a2a0-4ffd-9100-9c875e87f2a8"


class ContextHandler(BaseHTTPRequestHandler):
    project_id = PROJECT_ID

    def do_GET(self):
        if self.path != "/api/agentharbor/memory/context" or self.headers.get("Authorization") != "Bearer ahm_fake":
            self.send_error(401)
            return
        body = json.dumps({"project_id": self.project_id, "entries": [], "content": "", "version": ""}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args):
        pass


class ConnectSetupTests(unittest.TestCase):
    def setUp(self):
        ContextHandler.project_id = PROJECT_ID
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name) / "repo"
        self.root.mkdir()
        self.home = Path(self.temporary.name) / "home"
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), ContextHandler)
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def bundle(self, project_id=PROJECT_ID):
        return json.dumps({
            "version": 1,
            "api_url": f"http://127.0.0.1:{self.server.server_port}",
            "project_id": project_id,
            "token": "ahm_fake",
            "expires_at": "2099-01-01T00:00:00Z",
        })

    def test_installs_and_replaces_only_owned_mcp_sections(self):
        codex = self.root / ".codex"
        codex.mkdir()
        (codex / "config.toml").write_text(
            '[mcp_servers.other]\ncommand = "other"\n\n'
            '[mcp_servers.agentharbor_memory]\ncommand = "py"\nargs = ["memory_mcp.py"]\n\n'
            '[mcp_servers.agentharbor_memory.env]\nAGENTHARBOR_API_URL = "old"\n', encoding="utf-8",
        )
        with patch.object(Path, "home", return_value=self.home), patch("connect_setup.clipboard_text", return_value=self.bundle()):
            self.assertEqual(connect(self.root), PROJECT_ID)
            self.assertEqual(connect(self.root), PROJECT_ID)
        config = (codex / "config.toml").read_text(encoding="utf-8")
        self.assertIn('[mcp_servers.other]\ncommand = "other"', config)
        self.assertEqual(config.count("[mcp_servers.agentharbor_memory]"), 1)
        self.assertIn("memory_mcp.py", config)
        self.assertNotIn("ahm_fake", config)
        self.assertEqual(json.loads((codex / "agentharbor-project.json").read_text(encoding="utf-8"))["project_id"], PROJECT_ID)
        token_file = self.home / ".codex" / "agentharbor-memory-tokens" / (PROJECT_ID + ".json")
        self.assertEqual(json.loads(token_file.read_text(encoding="utf-8"))["token"], "ahm_fake")

    def test_rejects_wrong_project_before_writing_files(self):
        self.server.RequestHandlerClass.project_id = OTHER_PROJECT_ID
        with patch.object(Path, "home", return_value=self.home):
            with self.assertRaisesRegex(ValueError, "does not match"):
                connect(self.root, self.bundle())
        self.assertFalse((self.root / ".codex" / "config.toml").exists())
        self.assertFalse((self.home / ".codex").exists())

    def test_rejects_existing_binding_for_another_project(self):
        codex = self.root / ".codex"
        codex.mkdir()
        (codex / "agentharbor-project.json").write_text(json.dumps({
            "project_id": OTHER_PROJECT_ID, "local_root": str(self.root),
        }), encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "already bound"):
            connect(self.root, self.bundle())

    def test_cli_connects_explicit_project_root_not_checkout(self):
        checkout = Path(self.temporary.name) / "agentharbor-checkout"
        checkout.mkdir()
        with patch("sys.argv", ["connector.py", "connect", "--project-root", str(self.root)]), \
             patch.object(Path, "home", return_value=self.home), \
             patch("connect_setup.clipboard_text", return_value=self.bundle()), \
             patch("os.getcwd", return_value=str(checkout)):
            self.assertEqual(connector.main(), 0)
        self.assertTrue((self.root / ".codex" / "config.toml").exists())
        self.assertFalse((checkout / ".codex").exists())

    def test_rejects_missing_project_root(self):
        missing = self.root / "missing"
        with self.assertRaisesRegex(ValueError, "existing directory"):
            connect(missing, self.bundle())
        self.assertFalse(missing.exists())


if __name__ == "__main__":
    unittest.main()
