#!/usr/bin/env python3
"""Project-bound AgentHarbor memory tools over MCP stdio (standard library only)."""

import json
import os
from pathlib import Path
import sys
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


TOOLS = [
    {
        "name": "read_project_memory",
        "description": "Read the current MemoryCore L3 memory of this project directly from AgentHarbor. Use when the user asks to recall, look up, or retrieve project memory.",
        "inputSchema": {"type": "object", "properties": {}, "additionalProperties": False},
        "annotations": {"readOnlyHint": True, "openWorldHint": False},
    },
    {
        "name": "write_project_memory",
        "description": "Append one verified fact or session note to this project's MemoryCore L3 memory. Use only when the user explicitly asks to remember or save project memory. This is not an automatic per-turn save.",
        "inputSchema": {
            "type": "object",
            "properties": {"content": {"type": "string", "description": "One concise, verified project memory note."}},
            "required": ["content"],
            "additionalProperties": False,
        },
        "annotations": {"readOnlyHint": False, "destructiveHint": False, "idempotentHint": False, "openWorldHint": False},
    },
]


def project_settings():
    project = json.loads(Path(os.environ["AGENTHARBOR_PROJECT_CONFIG"]).read_text(encoding="utf-8"))
    if not Path.cwd().resolve().is_relative_to(Path(project["local_root"]).resolve()):
        raise ValueError("memory server is outside its configured project directory")
    credential = json.loads(Path(os.environ["AGENTHARBOR_MEMORY_TOKEN_FILE"]).read_text(encoding="utf-8"))
    if credential.get("project_id") != project["project_id"] or not credential.get("token", "").startswith("ahm_"):
        raise ValueError("project memory token is missing or mismatched")
    return project, credential


def call_memory_api(content=None):
    project, credential = project_settings()
    base = os.environ["AGENTHARBOR_API_URL"].rstrip("/")
    if not base.startswith(("http://", "https://")):
        raise ValueError("AgentHarbor API URL must use HTTP or HTTPS")
    payload = json.dumps({"content": content}, ensure_ascii=False).encode("utf-8") if content is not None else None
    request = Request(
        base + "/api/agentharbor/project-memory",
        data=payload,
        method="POST" if content is not None else "GET",
        headers={"Authorization": "Bearer " + credential["token"], "Content-Type": "application/json"},
    )
    try:
        with urlopen(request, timeout=15) as response:
            snapshot = json.load(response)
    except HTTPError as error:
        raise ValueError(f"AgentHarbor memory request failed: HTTP {error.code}") from error
    except URLError as error:
        raise ValueError("AgentHarbor memory service is unavailable") from error
    if snapshot.get("project_id") != project["project_id"] or not isinstance(snapshot.get("content"), str):
        raise ValueError("AgentHarbor returned a mismatched project memory snapshot")
    if content is not None and content not in snapshot["content"]:
        raise ValueError("AgentHarbor did not confirm the written memory")
    if content is None:
        return {"project_id": snapshot["project_id"], "version": snapshot.get("version", ""), "content": snapshot["content"]}
    return {"project_id": snapshot["project_id"], "version": snapshot.get("version", ""), "stored_note": content}


def call_tool(name, arguments):
    if not isinstance(arguments, dict):
        raise ValueError("tool arguments must be an object")
    if name == "read_project_memory" and not arguments:
        return call_memory_api()
    if name == "write_project_memory" and set(arguments) == {"content"}:
        content = arguments["content"]
        if not isinstance(content, str) or not content.strip() or len(content.encode("utf-8")) > 2048:
            raise ValueError("content must be 1-2048 UTF-8 bytes")
        return call_memory_api(content.strip())
    raise ValueError("unknown tool or invalid arguments")


def send(message):
    sys.stdout.buffer.write(json.dumps(message, ensure_ascii=False, separators=(",", ":")).encode("utf-8") + b"\n")
    sys.stdout.buffer.flush()


def serve():
    for line in sys.stdin.buffer:
        try:
            request = json.loads(line)
            if not isinstance(request, dict) or "id" not in request:
                continue
            method = request.get("method")
            params = request.get("params") or {}
            if method == "initialize":
                proposed = params.get("protocolVersion")
                version = proposed if proposed in {"2025-11-25", "2025-06-18", "2025-03-26"} else "2025-11-25"
                result = {
                    "protocolVersion": version,
                    "capabilities": {"tools": {}},
                    "serverInfo": {"name": "agentharbor-project-memory", "version": "0.1.0"},
                    "instructions": "These tools access only the project bound to this MCP server. Read directly from AgentHarbor when the user requests memory retrieval. Write one verified note only when the user explicitly asks to remember or save project memory. Never save every turn automatically. No tool accepts a project selector.",
                }
            elif method == "ping":
                result = {}
            elif method == "tools/list":
                result = {"tools": TOOLS}
            elif method == "tools/call":
                try:
                    value = call_tool(params.get("name"), params.get("arguments") or {})
                    result = {"content": [{"type": "text", "text": json.dumps(value, ensure_ascii=False)}]}
                except (KeyError, OSError, ValueError, TypeError) as error:
                    result = {"content": [{"type": "text", "text": str(error)}], "isError": True}
            else:
                send({"jsonrpc": "2.0", "id": request["id"], "error": {"code": -32601, "message": "method not found"}})
                continue
            send({"jsonrpc": "2.0", "id": request["id"], "result": result})
        except (json.JSONDecodeError, TypeError, AttributeError) as error:
            print(f"AgentHarbor MCP ignored invalid message: {error}", file=sys.stderr)


if __name__ == "__main__":
    serve()
