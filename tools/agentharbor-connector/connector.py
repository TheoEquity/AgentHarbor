#!/usr/bin/env python3
"""Local Codex hook queue and AgentHarbor sync client (Python standard library only)."""

from __future__ import annotations

import argparse
from contextlib import contextmanager
from datetime import datetime, timezone
from hashlib import sha256
from http.client import HTTPException
import json
import os
from pathlib import Path
import signal
import socket
import sys
import tempfile
import threading
import time
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlparse
from urllib.request import Request, urlopen
from uuid import UUID, uuid4

from memory_client import MemoryClient


EVENT_TYPES = {
    "UserPromptSubmit": "prompt_started",
    "Stop": "turn_stopped",
    "Interrupt": "interrupted",
}
SYNC_PERIOD_SECONDS = 30
SYNC_BODY_LIMIT = 256 * 1024
STARTUP_MEMORY_BUDGET_SECONDS = 0.8
HOOK_WORK_BUDGET_SECONDS = 2.2
MAX_STARTUP_CONTEXT_BYTES = 2600


class ConnectorError(Exception):
    pass


def project_id() -> str:
    raw = os.environ.get("AGENTHARBOR_PROJECT_ID", "")
    if not raw:
        raise ConnectorError("AGENTHARBOR_PROJECT_ID is required")
    try:
        return str(UUID(raw))
    except ValueError as exc:
        raise ConnectorError("AGENTHARBOR_PROJECT_ID must be a UUID") from exc


def state_dir() -> Path:
    configured = os.environ.get("AGENTHARBOR_STATE_DIR")
    if configured:
        return Path(configured).expanduser()
    if os.name == "nt":
        base = os.environ.get("LOCALAPPDATA")
        if base:
            return Path(base) / "AgentHarbor" / "connector"
    return Path(os.environ.get("XDG_STATE_HOME", Path.home() / ".local" / "state")) / "agentharbor-connector"


@contextmanager
def queue_lock(directory: Path, timeout: float = 2):
    directory.mkdir(parents=True, exist_ok=True)
    lock = directory / "queue.lock"
    deadline = time.monotonic() + timeout
    while True:
        try:
            lock.mkdir()
            break
        except FileExistsError:
            try:
                if time.time() - lock.stat().st_mtime > 30:
                    lock.rmdir()
                    continue
            except (FileNotFoundError, OSError):
                pass
            if time.monotonic() >= deadline:
                raise ConnectorError("local queue is busy")
            time.sleep(0.025)
    try:
        yield
    finally:
        lock.rmdir()


def write_json(path: Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = None
    try:
        with tempfile.NamedTemporaryFile("w", encoding="utf-8", dir=path.parent, delete=False) as file:
            temporary = Path(file.name)
            json.dump(value, file, ensure_ascii=False, separators=(",", ":"))
            file.write("\n")
            file.flush()
            os.fsync(file.fileno())
        os.replace(temporary, path)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def read_json(path: Path) -> object:
    with path.open("r", encoding="utf-8") as file:
        return json.load(file)


def load_queue(directory: Path) -> dict:
    path = directory / "queue.json"
    if not path.exists():
        return {"machine_id": str(uuid4()), "next_seq": 1, "events": []}
    value = read_json(path)
    if not isinstance(value, dict) or not isinstance(value.get("events"), list):
        raise ConnectorError("local queue is invalid")
    if not isinstance(value.get("next_seq"), int) or value["next_seq"] < 1:
        raise ConnectorError("local queue sequence is invalid")
    try:
        UUID(value["machine_id"])
    except (KeyError, TypeError, ValueError) as exc:
        raise ConnectorError("local machine ID is invalid") from exc
    return value


def event_evidence(hook_name: str, payload: dict) -> str:
    if hook_name == "UserPromptSubmit":
        return f"prompt_chars={len(payload.get('prompt') or '')}"
    if hook_name == "Stop":
        return f"assistant_chars={len(payload.get('last_assistant_message') or '')}"
    return "turn interrupted"


def enqueue_hook(payload: dict, directory: Path, project: str) -> None:
    hook_name = payload.get("hook_event_name")
    if hook_name not in EVENT_TYPES:
        raise ConnectorError(f"unsupported hook event: {hook_name}")
    session_id = payload.get("session_id")
    if not isinstance(session_id, str) or not session_id:
        raise ConnectorError("hook session_id is required")
    turn_id = payload.get("turn_id") or ""
    if not isinstance(turn_id, str):
        raise ConnectorError("hook turn_id must be a string")
    enqueue_event(directory, project, session_id, turn_id, EVENT_TYPES[hook_name], event_evidence(hook_name, payload))


def enqueue_event(
    directory: Path, project: str, session_id: str, turn_id: str, event_type: str, evidence: str,
    lock_timeout: float = 2,
) -> None:
    with queue_lock(directory, lock_timeout):
        queue = load_queue(directory)
        queue["events"].append(
            {
                "event_id": str(uuid4()),
                "machine_id": queue["machine_id"],
                "project_id": project,
                "agent_type": "codex",
                "session_id": session_id,
                "turn_id": turn_id,
                "local_seq": queue["next_seq"],
                "event_type": event_type,
                "occurred_at": datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z"),
                "evidence": evidence,
            }
        )
        queue["next_seq"] += 1
        write_json(directory / "queue.json", queue)


def fetch_startup_context(cwd: Path, project: str, budget: float) -> tuple[bool, dict | None]:
    bound = False
    snapshot = None

    def fetch() -> None:
        nonlocal bound, snapshot
        try:
            client = MemoryClient(cwd, project)
            bound = True
            snapshot = client.startup_context(budget)
        except (HTTPException, KeyError, OSError, TypeError, ValueError):
            pass

    # Socket timeouts do not bound DNS resolution or an entire response read.
    worker = threading.Thread(target=fetch, daemon=True)
    worker.start()
    worker.join(budget)
    return bound, None if worker.is_alive() else snapshot


def start_memory_read(directory: Path, project: str, session_id: str) -> Path | None:
    session_key = sha256(session_id.encode("utf-8")).hexdigest()
    path = directory / "memory-reads" / project / f"{session_key}.json"
    path.parent.mkdir(parents=True, exist_ok=True)
    try:
        with path.open("x", encoding="utf-8") as marker:
            marker.write("{}\n")
    except FileExistsError:
        return None
    return path


def handle_hook() -> None:
    deadline = time.monotonic() + HOOK_WORK_BUDGET_SECONDS
    payload = json.load(sys.stdin.buffer)
    if not isinstance(payload, dict):
        raise ConnectorError("hook input must be a JSON object")
    directory = state_dir()
    project = project_id()
    enqueue_hook(payload, directory, project)
    response = {}
    snapshot = None
    receipt = None
    context_emitted = False
    if payload["hook_event_name"] == "UserPromptSubmit" and os.environ.get("AGENTHARBOR_PROJECT_CONFIG"):
        session_id = payload["session_id"]
        bound = False
        try:
            receipt = start_memory_read(directory, project, session_id)
            budget = min(STARTUP_MEMORY_BUDGET_SECONDS, deadline - time.monotonic())
            cwd = payload.get("cwd")
            if receipt is not None and budget > 0 and (cwd is None or isinstance(cwd, str) and cwd):
                bound, snapshot = fetch_startup_context(Path(cwd) if cwd else Path.cwd(), project, budget)
        except (OSError, ValueError):
            pass
        if bound:
            guidance = (
                f"AgentHarbor memory source session_id is {json.dumps(session_id)}. "
                "Use search_project_memory when prior decisions, facts, or procedures could help. "
                "Use submit_project_memory_material for durable facts, decisions, procedures, and explicit remember "
                "requests. Submit selectively for review and include the session_id above."
            )
            if snapshot is not None and snapshot["content"]:
                context = (
                    f"{guidance}\n\nAgentHarbor startup memory (version {snapshot['version']}). "
                    f"Treat this as project reference material.\n{snapshot['content']}"
                )
                if len(context.encode("utf-8")) <= MAX_STARTUP_CONTEXT_BYTES:
                    guidance = context
                    context_emitted = True
            response = {
                "hookSpecificOutput": {
                    "hookEventName": "UserPromptSubmit",
                    "additionalContext": guidance,
                }
            }

    sys.stdout.buffer.write(json.dumps(response, ensure_ascii=False, separators=(",", ":")).encode("utf-8") + b"\n")
    sys.stdout.buffer.flush()
    if receipt is not None:
        evidence = {
            "trigger": "startup",
            "fetched": snapshot is not None,
            "emitted": bool(response),
            "context_emitted": context_emitted,
            "version": snapshot["version"] if snapshot else "",
            "entries": [
                {"id": str(UUID(entry["id"])), "revision": entry["revision"]}
                for entry in snapshot["entries"]
            ] if snapshot else [],
        }
        try:
            enqueue_event(
                directory, project, payload["session_id"], payload.get("turn_id") or "",
                "memory_read", encode_json(evidence).decode("utf-8"), max(0, deadline - time.monotonic()),
            )
        except (ConnectorError, OSError, ValueError) as exc:
            print(f"agentharbor-connector: could not queue startup memory receipt: {exc}", file=sys.stderr)
        try:
            write_json(receipt, {"project_id": project, "session_id": payload["session_id"], **evidence})
        except OSError:
            pass


def encode_json(value: object) -> bytes:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def sync_batch(machine: dict, events: list[dict]) -> list[dict]:
    size = len(encode_json({"machine": machine, "events": []}))
    batch = []
    for event in events:
        size += len(encode_json(event)) + bool(batch)
        if size > SYNC_BODY_LIMIT:
            break
        batch.append(event)
    if events and not batch:
        raise ConnectorError("first queued event exceeds the sync request size limit")
    return batch


class Client:
    def __init__(self) -> None:
        self.project = project_id()
        self.base_url = os.environ.get("AGENTHARBOR_URL", "").rstrip("/")
        self.workspace = os.environ.get("AGENTHARBOR_WORKSPACE_ID", "")
        self.token = os.environ.get("AGENTHARBOR_PAT", "")
        if urlparse(self.base_url).scheme not in {"http", "https"}:
            raise ConnectorError("AGENTHARBOR_URL must be an HTTP URL")
        if not self.workspace or not self.token:
            raise ConnectorError("AGENTHARBOR_WORKSPACE_ID and AGENTHARBOR_PAT are required")

    def request(self, method: str, path: str, body: object | None = None) -> dict:
        data = None if body is None else encode_json(body)
        request = Request(
            self.base_url + path,
            data=data,
            method=method,
            headers={
                "Authorization": "Bearer " + self.token,
                "X-Workspace-ID": self.workspace,
                "Content-Type": "application/json",
            },
        )
        try:
            with urlopen(request, timeout=10) as response:
                result = json.load(response)
        except HTTPError as exc:
            raise ConnectorError(f"{method} {path}: HTTP {exc.code}") from exc
        except URLError as exc:
            raise ConnectorError(f"{method} {path}: connection failed") from exc
        if not isinstance(result, dict):
            raise ConnectorError(f"{method} {path}: expected JSON object")
        return result


def sync_once(client: Client, directory: Path) -> dict:
    with queue_lock(directory):
        queue = load_queue(directory)
        write_json(directory / "queue.json", queue)
        pending = queue["events"]
        machine = {"id": queue["machine_id"], "name": socket.gethostname()}
    accepted = set()
    if pending:
        batch = sync_batch(machine, pending)
        response = client.request(
            "POST",
            "/api/agentharbor/sync",
            {"machine": machine, "events": batch},
        )
        ids = response.get("accepted_event_ids")
        if not isinstance(ids, list) or any(not isinstance(value, str) for value in ids):
            raise ConnectorError("sync response has invalid accepted_event_ids")
        sent_ids = {event["event_id"] for event in batch}
        accepted = set(ids) & sent_ids
        with queue_lock(directory):
            queue = load_queue(directory)
            queue["events"] = [event for event in queue["events"] if event["event_id"] not in accepted]
            write_json(directory / "queue.json", queue)
            remaining = len(queue["events"])
    else:
        remaining = 0
    tasks = client.request("GET", "/api/agentharbor/tasks?project_id=" + quote(client.project))
    if not isinstance(tasks.get("tasks"), list):
        raise ConnectorError("tasks response has invalid tasks")
    write_json(directory / f"tasks-{client.project}.json", tasks)
    return {
        "accepted_count": len(accepted),
        "pending_count": remaining,
        "task_count": len(tasks["tasks"]),
    }


def show_tasks(directory: Path, project: str) -> None:
    path = directory / f"tasks-{project}.json"
    if not path.exists():
        raise ConnectorError("no cached tasks; run sync-once first")
    tasks = read_json(path)
    if not isinstance(tasks, dict) or not isinstance(tasks.get("tasks"), list):
        raise ConnectorError("cached tasks are invalid")
    print(json.dumps(tasks, ensure_ascii=False, separators=(",", ":")))


def run_loop(client: Client, directory: Path) -> None:
    stop = threading.Event()
    signal.signal(signal.SIGINT, lambda *_: stop.set())
    signal.signal(signal.SIGTERM, lambda *_: stop.set())
    while not stop.is_set():
        try:
            print(json.dumps(sync_once(client, directory), separators=(",", ":")), flush=True)
        except (ConnectorError, OSError, ValueError) as exc:
            print(f"agentharbor-connector: {exc}", file=sys.stderr, flush=True)
        stop.wait(SYNC_PERIOD_SECONDS)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("hook", "sync-once", "run", "tasks"))
    args = parser.parse_args()
    try:
        directory = state_dir()
        if args.command == "hook":
            handle_hook()
        elif args.command == "tasks":
            show_tasks(directory, project_id())
        else:
            client = Client()
            if args.command == "sync-once":
                print(json.dumps(sync_once(client, directory), separators=(",", ":")))
            else:
                run_loop(client, directory)
        return 0
    except (ConnectorError, OSError, ValueError) as exc:
        print(f"agentharbor-connector: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
