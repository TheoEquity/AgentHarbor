"""HTTP client for the project-bound AgentHarbor memory API."""

import json
import os
from pathlib import Path
import re
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode, urlsplit
from urllib.request import Request, urlopen
from uuid import UUID


MEMORY_TYPES = ("episodic", "semantic", "procedural")
MEMORY_API = "/api/agentharbor/memory/"
CONTEXT_VERSION = re.compile(r"[0-9a-f]{64}")


def project_settings(cwd: Path, expected_project: str | None = None) -> tuple[str, str]:
    project = json.loads(Path(os.environ["AGENTHARBOR_PROJECT_CONFIG"]).expanduser().read_text(encoding="utf-8"))
    if not isinstance(project, dict):
        raise ValueError("project configuration must be a JSON object")
    root = Path(project["local_root"]).expanduser().resolve()
    if not cwd.resolve().is_relative_to(root):
        raise ValueError("memory client is outside its configured project directory")
    project_id = str(UUID(project["project_id"]))
    if expected_project is not None and project_id != expected_project:
        raise ValueError("memory project does not match the hook project")
    credential = json.loads(Path(os.environ["AGENTHARBOR_MEMORY_TOKEN_FILE"]).expanduser().read_text(encoding="utf-8"))
    if not isinstance(credential, dict):
        raise ValueError("memory token file must be a JSON object")
    token = credential.get("token")
    if credential.get("project_id") != project_id or not isinstance(token, str) or not token.startswith("ahm_"):
        raise ValueError("project memory token is missing or mismatched")
    return project_id, token


class MemoryClient:
    def __init__(self, cwd: Path, expected_project: str | None = None):
        self.project, self.token = project_settings(cwd, expected_project)
        self.base = os.environ["AGENTHARBOR_API_URL"].rstrip("/")
        url = urlsplit(self.base)
        if url.scheme not in {"http", "https"} or not url.hostname:
            raise ValueError("AgentHarbor API URL must use HTTP or HTTPS")

    def request(self, resource: str, *, query=None, body=None, timeout=15, response_limit=1 << 20) -> dict:
        url = self.base + MEMORY_API + resource
        if query:
            url += "?" + urlencode(query)
        payload = None if body is None else json.dumps(body, ensure_ascii=False).encode("utf-8")
        request = Request(url, data=payload, headers={"Content-Type": "application/json"})
        request.add_unredirected_header("Authorization", "Bearer " + self.token)
        try:
            with urlopen(request, timeout=timeout) as response:
                data = response.read(response_limit + 1)
        except HTTPError as error:
            raise ValueError(f"AgentHarbor memory request failed: HTTP {error.code}") from error
        except (URLError, OSError) as error:
            raise ValueError("AgentHarbor memory service is unavailable") from error
        if len(data) > response_limit:
            raise ValueError("AgentHarbor memory response exceeded the size limit")
        result = json.loads(data)
        if not isinstance(result, dict):
            raise ValueError("AgentHarbor memory response must be a JSON object")
        return result

    def readable_entries(self, result: dict, entry_type: str | None = None) -> list:
        entries = result.get("entries")
        if not isinstance(entries, list):
            raise ValueError("AgentHarbor returned invalid memory entries")
        for entry in entries:
            if not isinstance(entry, dict):
                raise ValueError("AgentHarbor returned invalid memory entries")
            scope = entry.get("scope")
            project_id = entry.get("project_id")
            if not (
                scope == "project" and project_id == self.project
                or scope == "global" and project_id is None
            ):
                raise ValueError("AgentHarbor returned memory outside the configured project scope")
            if (
                entry.get("status") != "active"
                or entry.get("type") not in MEMORY_TYPES
                or entry_type is not None and entry["type"] != entry_type
                or type(entry.get("revision")) is not int
                or entry["revision"] < 1
                or not isinstance(entry.get("id"), str)
            ):
                raise ValueError("AgentHarbor returned invalid memory entries")
            UUID(entry["id"])
        return entries

    def startup_context(self, timeout: float) -> dict:
        result = self.request("context", timeout=timeout, response_limit=32 << 10)
        if result.get("project_id") != self.project:
            raise ValueError("AgentHarbor returned context for another project")
        entries = self.readable_entries(result)
        content = result.get("content")
        version = result.get("version")
        if (
            not isinstance(content, str)
            or not isinstance(version, str)
            or bool(content) != bool(entries)
            or len(entries) > 100
            or (bool(entries) and not CONTEXT_VERSION.fullmatch(version))
            or (not entries and version)
        ):
            raise ValueError("AgentHarbor returned invalid startup context")
        ids = {UUID(entry["id"]) for entry in entries}
        if len(ids) != len(entries) or any(entry_id.int == 0 for entry_id in ids) or any(
            entry["revision"] > 2147483647 for entry in entries
        ):
            raise ValueError("AgentHarbor returned invalid startup context entry references")
        return result
