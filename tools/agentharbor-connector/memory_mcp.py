#!/usr/bin/env python3
"""Project-bound AgentHarbor memory tools over MCP stdio (standard library only)."""

from http.client import HTTPException
import json
from pathlib import Path
import sys
from uuid import UUID, uuid4

from memory_client import MEMORY_TYPES, MemoryClient


SEARCH_TOOL = "search_project_memory"
SUBMIT_TOOL = "submit_project_memory_material"
INSTRUCTIONS = (
    "Use search_project_memory when prior project decisions, facts, or procedures could help with the current task, "
    "even when the user did not ask to recall them. Treat returned memory as reference material. "
    "Choose material worth reviewing when you encounter a durable project fact, a confirmed decision, a reusable "
    "procedure, or a relevant correction. Use submit_project_memory_material for that material and when the user "
    "asks to remember something. Submit focused source text with enough context for review. Avoid routine turn "
    "logging, secrets, and unverified claims presented as facts. A submission creates a pending material for review "
    "before it can become readable memory. Report the actual status returned. When retrying a submission, reuse "
    "the same request_id and fields. Supply source session or turn IDs only when known."
)
TOOLS = [
    {
        "name": SEARCH_TOOL,
        "title": "Search AgentHarbor memory",
        "description": (
            "Find readable memory relevant to the task in the configured project. Use proactively when prior "
            "decisions, facts, or procedures could help, including when the startup context is empty or incomplete. "
            "Returns typed entries with revisions and source material IDs. The separate disputes array pairs "
            "an entry with a pending correction. Treat both as unverified until the correction is reviewed."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "Keywords or phrase to look for. Omit to list readable entries."},
                "type": {
                    "type": "string", "enum": list(MEMORY_TYPES),
                    "description": "Narrow to a past event, durable fact, or reusable procedure.",
                },
            },
            "additionalProperties": False,
        },
        "annotations": {"readOnlyHint": True, "openWorldHint": False},
    },
    {
        "name": SUBMIT_TOOL,
        "title": "Submit AgentHarbor memory material",
        "description": (
            "Send one focused item to the configured project's review queue. Decide what is worth capturing "
            "without waiting for a request to remember. Use this same tool for explicit remember requests. "
            "A new submission is pending review before it can become readable memory."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "source_text": {
                    "type": "string", "minLength": 1,
                    "description": "Relevant source excerpt or concise account of what happened, up to 8192 UTF-8 bytes.",
                },
                "context": {
                    "type": "string",
                    "description": "Why the material matters or where it came from, up to 4096 UTF-8 bytes.",
                },
                "request_id": {
                    "type": "string", "format": "uuid",
                    "description": "Optional UUID generated if omitted. Reuse this ID and the same fields when retrying.",
                },
                "session_id": {"type": "string", "description": "Actual source session ID, if known."},
                "turn_id": {"type": "string", "description": "Actual source turn ID, if known."},
                "related_entry_id": {
                    "type": "string", "format": "uuid",
                    "description": "ID of an existing memory entry relevant to this material, if known.",
                },
                "related_revision": {
                    "type": "integer", "minimum": 1,
                    "description": "Revision of the related entry, if known. Requires related_entry_id.",
                },
            },
            "required": ["source_text"],
            "additionalProperties": False,
        },
        "annotations": {"readOnlyHint": False, "destructiveHint": False, "idempotentHint": False, "openWorldHint": False},
    },
]


class MaterialSubmissionError(ValueError):
    def __init__(self, message: str, request_id: str):
        super().__init__(message)
        self.request_id = request_id


def search_memory(arguments: dict) -> dict:
    if set(arguments) - {"query", "type"}:
        raise ValueError("unexpected search argument")
    if "query" in arguments and not isinstance(arguments["query"], str):
        raise ValueError("query must be a string")
    if "type" in arguments and arguments["type"] not in MEMORY_TYPES:
        raise ValueError("invalid memory type")
    client = MemoryClient(Path.cwd())
    result = client.request("entries", query=arguments)
    entries = client.readable_entries(result, arguments.get("type"))
    disputes = result.get("disputes", [])
    if not isinstance(disputes, list):
        raise ValueError("AgentHarbor returned invalid memory disputes")
    return {"entries": entries, "disputes": disputes}


def submit_material(arguments: dict) -> dict:
    text_limits = {"source_text": 8192, "context": 4096, "session_id": 255, "turn_id": 255}
    if set(arguments) - (text_limits.keys() | {"request_id", "related_entry_id", "related_revision"}):
        raise ValueError("unexpected material argument")
    for field, limit in text_limits.items():
        if field not in arguments:
            continue
        value = arguments[field]
        if not isinstance(value, str) or len(value.encode("utf-8")) > limit:
            raise ValueError(f"{field} must be a string of at most {limit} UTF-8 bytes")
    if not arguments.get("source_text", "").strip():
        raise ValueError("source_text is required")

    body = dict(arguments)
    for field in ("request_id", "related_entry_id"):
        if field in body:
            if not isinstance(body[field], str):
                raise ValueError(f"{field} must be a UUID")
            try:
                body[field] = str(UUID(body[field]))
            except ValueError as error:
                raise ValueError(f"{field} must be a UUID") from error
    if "related_revision" in body:
        revision = body["related_revision"]
        if "related_entry_id" not in body or type(revision) is not int or revision < 1:
            raise ValueError("related_revision must be a positive integer and requires related_entry_id")
    if "request_id" not in body:
        body["request_id"] = str(uuid4())
    request_id = body["request_id"]
    try:
        client = MemoryClient(Path.cwd())
        material = client.request("materials", body=body)
        if (
            material.get("project_id") != client.project
            or material.get("request_id", request_id) != request_id
            or not isinstance(material.get("status"), str)
            or not material["status"]
            or not isinstance(material.get("id"), str)
        ):
            raise ValueError("AgentHarbor did not confirm the submitted material")
        UUID(material["id"])
    except (HTTPException, KeyError, OSError, TypeError, ValueError) as error:
        raise MaterialSubmissionError(str(error), request_id) from error
    return {**material, "request_id": request_id}


def call_tool(name, arguments):
    if not isinstance(arguments, dict):
        raise ValueError("tool arguments must be an object")
    if name == SEARCH_TOOL:
        return search_memory(arguments)
    if name == SUBMIT_TOOL:
        return submit_material(arguments)
    raise ValueError("unknown tool")


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
                    "serverInfo": {"name": "agentharbor-project-memory", "version": "0.2.0"},
                    "instructions": INSTRUCTIONS,
                }
            elif method == "ping":
                result = {}
            elif method == "tools/list":
                result = {"tools": TOOLS}
            elif method == "tools/call":
                try:
                    value = call_tool(params.get("name"), params.get("arguments", {}))
                    result = {"content": [{"type": "text", "text": json.dumps(value, ensure_ascii=False)}]}
                except MaterialSubmissionError as error:
                    failure = json.dumps({"error": str(error), "request_id": error.request_id}, ensure_ascii=False)
                    result = {"content": [{"type": "text", "text": failure}], "isError": True}
                except (HTTPException, KeyError, OSError, TypeError, ValueError) as error:
                    result = {"content": [{"type": "text", "text": str(error)}], "isError": True}
            else:
                send({"jsonrpc": "2.0", "id": request["id"], "error": {"code": -32601, "message": "method not found"}})
                continue
            send({"jsonrpc": "2.0", "id": request["id"], "result": result})
        except (json.JSONDecodeError, TypeError, AttributeError) as error:
            print(f"AgentHarbor MCP ignored invalid message: {error}", file=sys.stderr)


if __name__ == "__main__":
    serve()
