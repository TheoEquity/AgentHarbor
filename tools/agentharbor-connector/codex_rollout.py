"""Read Codex task metadata without retaining prompt or tool content."""

from contextlib import closing
from datetime import datetime, timezone
from functools import lru_cache
from hashlib import sha256
import json
import os
from pathlib import Path
import re
import sqlite3
from uuid import UUID


STATUSES = {"task_started": "running", "task_complete": "completed", "turn_aborted": "aborted"}
LIFECYCLE_MARKERS = tuple(name.encode("ascii") for name in STATUSES)
TURN_ID = re.compile(r"[A-Za-z0-9_.:-]{1,128}")
LOCAL_PATH = re.compile(r"(?:[A-Za-z]:[/\\]|\\\\|(?:^|[^\w/])/\S)")


def utc_text(value: datetime) -> str:
    return value.astimezone(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def _event_time(value: object) -> str | None:
    try:
        if type(value) in (float, int):
            return utc_text(datetime.fromtimestamp(value, timezone.utc))
        if isinstance(value, str):
            parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
            return utc_text(parsed) if parsed.tzinfo is not None else None
    except (OSError, OverflowError, ValueError):
        pass
    return None


def _title(name: str | None) -> str:
    if not name:
        return "Codex task"
    text = " ".join("".join(char for char in name if char.isprintable()).split())
    return "Codex task" if not text or LOCAL_PATH.search(text) else text[:120]


def project_threads(codex_home: Path, project_roots: tuple[Path, ...], include_unbound: bool = False):
    database = codex_home / "state_5.sqlite"
    with closing(sqlite3.connect(database.as_uri() + "?mode=ro", uri=True, timeout=0.5)) as connection:
        columns = {row[1] for row in connection.execute("PRAGMA table_info(threads)")}
        # Codex reserves name for explicit names. title, preview and session_index
        # may contain the first prompt, so they are never used as a fallback.
        name = "NULL"
        if {"name", "first_user_message"} <= columns:
            name = """CASE WHEN instr(trim(first_user_message, char(9,10,13,32)),
                trim(name, char(9,10,13,32))) = 1 THEN NULL ELSE name END"""
        source = ""
        if not include_unbound and {"thread_source", "source"} <= columns:
            source = """WHERE coalesce(thread_source, 'user') = 'user' OR
                (source = 'vscode' AND thread_source IN ('subagent', 'agent_forked_thread', 'agent_created_thread'))"""
        rows = connection.execute(f"SELECT id, cwd, rollout_path, archived, {name} FROM threads {source}")
        roots_by_cwd = {}
        for thread_id, cwd, rollout, archived, explicit_name in rows:
            if cwd not in roots_by_cwd:
                roots_by_cwd[cwd] = _matching_root(cwd, project_roots)
            project = roots_by_cwd[cwd]
            if project is not None or include_unbound:
                yield project, str(UUID(thread_id)), Path(rollout or "."), bool(archived), _title(explicit_name)


def _matching_root(cwd: str | None, project_roots: tuple[Path, ...]) -> Path | None:
    if not cwd or not Path(cwd).is_absolute():
        return None
    # Old Windows rows can refer to drives that are currently disconnected.
    try:
        local_root = Path(cwd).resolve()
    except OSError:
        local_root = Path(cwd)
    return next((root for root in project_roots if local_root.is_relative_to(root)), None)


def _find_rollouts(roots: tuple[Path, ...]) -> dict[str, Path]:
    files = {}
    for root in roots:
        for file in sorted(root.rglob("rollout-*.jsonl")):
            resolved = file.resolve()
            if resolved.is_relative_to(root):
                files.setdefault(file.name.lower(), resolved)
    return files


@lru_cache(maxsize=4)
def _backup_rollouts(roots: tuple[Path, ...]) -> dict[str, Path]:
    return _find_rollouts(roots)


class RolloutIndex:
    def __init__(self, codex_home: Path, backups: tuple[Path, ...] = ()):
        self.sessions = (codex_home / "sessions").resolve()
        self.archived = (codex_home / "archived_sessions").resolve()
        self.live = _find_rollouts((self.sessions, self.archived))
        self.backups = _backup_rollouts(backups) if backups else {}

    def locate(self, path: Path) -> tuple[Path, bool]:
        if (moved := self.live.get(path.name.lower())) and moved.is_file():
            resolved = path.resolve()
            in_live = resolved.is_relative_to(self.sessions) or resolved.is_relative_to(self.archived)
            selected = resolved if in_live and resolved.is_file() else moved
            return selected, selected.is_relative_to(self.archived)
        if (saved := self.backups.get(path.name.lower())) and saved.is_file():
            return saved, True
        raise OSError("rollout unavailable")

    def unindexed_threads(self, project_roots: tuple[Path, ...], known: set[str]):
        for name, path in self.live.items():
            if name in known:
                continue
            try:
                with path.open("rb") as stream:
                    line = stream.readline()
                if not line.endswith(b"\n"):
                    continue
                item = json.loads(line)
                if item.get("type") != "session_meta":
                    continue
                meta = item["payload"]
                thread = str(UUID(meta["id"]))
                if not name.endswith(f"-{thread}.jsonl"):
                    continue
                root = _matching_root(meta.get("cwd"), project_roots)
            except (OSError, ValueError, AttributeError, KeyError, TypeError):
                continue
            yield root, thread, path, path.is_relative_to(self.archived), "Codex task"


def _lifecycle(line: bytes, starts: dict) -> dict | None:
    if not any(marker in line for marker in LIFECYCLE_MARKERS) and b"\\u" not in line:
        return None
    try:
        event = json.loads(line)
    except (UnicodeDecodeError, json.JSONDecodeError):
        return None
    if not isinstance(event, dict) or event.get("type") != "event_msg":
        return None
    payload = event.get("payload")
    if not isinstance(payload, dict) or not isinstance(payload.get("type"), str):
        return None
    status = STATUSES.get(payload["type"])
    if not status:
        return None
    turn_id = payload.get("turn_id")
    if not isinstance(turn_id, str) or not TURN_ID.fullmatch(turn_id):
        raise ValueError("invalid lifecycle turn ID")
    started_at = _event_time(payload.get("started_at")) or starts.get(turn_id)
    if status == "running":
        if started_at is None:
            raise ValueError("start time missing")
        starts[turn_id] = started_at
        return {"turn_id": turn_id, "started_at": started_at, "ended_at": None, "status": status}
    ended_at = _event_time(payload.get("completed_at"))
    if ended_at is None and status == "aborted":
        ended_at = _event_time(event.get("timestamp"))
    if ended_at is None or (started_at is not None and started_at > ended_at):
        raise ValueError("invalid terminal time")
    starts.pop(turn_id, None)
    return {"turn_id": turn_id, "started_at": started_at, "ended_at": ended_at, "status": status}


def _checkpoint(stream, offset: int) -> str:
    stream.seek(max(0, offset - 64))
    return sha256(stream.read(min(64, offset))).hexdigest()


def read_rollout(path: Path, cursor: dict) -> tuple[dict, list[dict], bool]:
    """Consume complete lines within the initial file size."""
    observed = path.stat()
    if cursor.get("signature") == _signature(observed):
        return cursor, [], cursor.get("degraded", False)
    with path.open("rb") as stream:
        file = os.fstat(stream.fileno())
        identity = [file.st_dev, file.st_ino]
        offset = cursor.get("offset", 0)
        if (cursor.get("identity") != identity or offset > file.st_size
                or _checkpoint(stream, offset) != cursor.get("checkpoint")):
            offset = 0
        starts = dict(cursor.get("starts", {})) if offset else {}
        stream.seek(offset)
        turns = {}
        degraded = False
        while offset < file.st_size:
            line = stream.readline(file.st_size - offset)
            if not line.endswith(b"\n"):
                break
            offset += len(line)
            try:
                turn = _lifecycle(line, starts)
            except ValueError:
                degraded = True
                continue
            if turn is not None:
                key = turn["turn_id"]
                if turns.get(key, {}).get("ended_at") is None:
                    turns[key] = turn
        saved = {
            "offset": offset, "identity": identity, "checkpoint": _checkpoint(stream, offset), "starts": starts,
            "signature": _signature(file), "degraded": degraded,
        }
    return saved, list(turns.values()), degraded


def _signature(file: os.stat_result) -> list[int]:
    return [file.st_dev, file.st_ino, file.st_size, file.st_mtime_ns]
