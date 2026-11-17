"""Scan and sync turns for an explicitly bound AgentHarbor project."""

from datetime import datetime, timezone
import os
from pathlib import Path
import plistlib
import socket
import sqlite3
import sys
from uuid import UUID

from codex_rollout import RolloutIndex, project_threads, read_rollout, utc_text
from connector_queue import ConnectorError, load_queue, queue_lock, read_json, write_json


MONITOR_SYNC = "/api/agentharbor/monitor/sync"
MAX_BATCH = 100


def _bound_root(directory: Path, project: str) -> Path:
    try:
        root = directory.resolve(strict=True)
        binding = read_json(root / ".codex" / "agentharbor-project.json")
        if str(UUID(binding["project_id"])) == project and Path(binding["local_root"]).resolve() == root:
            return root
    except (KeyError, TypeError, AttributeError, OSError, ValueError):
        pass
    raise ConnectorError("monitor requires a matching local project binding")


def _legacy_preferences_path() -> Path | None:
    if os.name == "nt" and (base := os.environ.get("LOCALAPPDATA")):
        return Path(base) / "CodexTaskMonitor" / "settings.json"
    if sys.platform == "darwin":
        return Path.home() / "Library" / "Preferences" / "local.codex.task-monitor.plist"
    return None


def _known_handled(path: Path | None) -> set[str]:
    if path is None:
        return set()
    try:
        if path.suffix == ".plist":
            with path.open("rb") as stream:
                value = plistlib.load(stream)
            key = "dismissedItemIDs"
        else:
            value = read_json(path)
            key = "dismissedItemIds"
        items = value.get(key) if isinstance(value, dict) else None
        return {item for item in items if isinstance(item, str)} if isinstance(items, list) else set()
    except (OSError, ValueError, plistlib.InvalidFileException):
        return set()


def _save_state(path: Path, state: dict) -> None:
    state["revision"] += 1
    write_json(path, state)


def _start_state(path: Path, connections: set[str], connected_at: str) -> dict:
    with queue_lock(path.parent):
        state = read_json(path) if path.exists() else {
            "revision": 0, "connections": {}, "cursors": {}, "pending": [], "scan_status": "degraded",
        }
        new_roots = connections - state["connections"].keys()
        if new_roots:
            state["connections"].update(dict.fromkeys(new_roots, connected_at))
            _save_state(path, state)
        return state


def _snapshot(turn, thread_id, title, connected_at, known_handled):
    ended_at = turn["ended_at"]
    historical = ended_at is not None and ended_at < connected_at
    handled = None
    if historical and f"{thread_id}:{turn['turn_id']}" in known_handled:
        handled = True
    elif ended_at is not None and not historical:
        handled = False
    return {**turn, "session_id": thread_id, "title": title, "historical": historical, "handled": handled}


def _scan(state: dict, files: RolloutIndex, threads: list | None, known_handled: set[str]) -> int:
    degraded = int(threads is None)
    for root, thread_id, file, archived, title in threads or ():
        try:
            path, path_archived = files.locate(file)
            cursor, turns, incompatible = read_rollout(path, state["cursors"].get(thread_id, {}))
        except (OSError, ValueError):
            degraded += 1
            continue
        state["cursors"][thread_id] = cursor
        degraded += int(incompatible)
        state["pending"].extend(
            _snapshot(turn, thread_id, title, state["connections"][str(root)], known_handled)
            for turn in turns if not (archived or path_archived) or turn["ended_at"] is not None
        )
    state["scan_status"] = "degraded" if degraded else "ok"
    return degraded


def _read_threads(codex_home: Path, roots: tuple[Path, ...], include_unbound: bool = False) -> list | None:
    try:
        return list(project_threads(codex_home, roots, include_unbound))
    except (OSError, ValueError, TypeError, sqlite3.Error):
        return None


def _capture(path, connections, files, threads, known_handled, connected_at) -> int:
    state = _start_state(path, connections, connected_at)
    previous_revision = state["revision"]
    degraded_count = _scan(state, files, threads, known_handled)
    # Do not replace cursors or acknowledgements written by another invocation.
    with queue_lock(path.parent):
        if read_json(path)["revision"] == previous_revision:
            _save_state(path, state)
    return degraded_count


def _machine(directory: Path) -> dict:
    with queue_lock(directory):
        queue = load_queue(directory)
        if not (directory / "queue.json").exists():
            write_json(directory / "queue.json", queue)
    return {"id": queue["machine_id"], "name": socket.gethostname()}


def _flush(client, path: Path, machine: dict) -> tuple[int, int]:
    accepted = 0
    with queue_lock(path.parent):
        state = read_json(path)
    pending = state["pending"]
    try:
        for offset in range(0, max(1, len(pending)), MAX_BATCH):
            batch = pending[offset:offset + MAX_BATCH]
            result = client.request("POST", MONITOR_SYNC, {
                "project_id": client.project, "machine": machine, "scan_status": state["scan_status"], "turns": batch,
            })
            if type(result.get("accepted_count")) is not int or result["accepted_count"] != len(batch):
                raise ConnectorError("monitor sync did not confirm the full batch")
            accepted += len(batch)
    finally:
        if accepted:
            with queue_lock(path.parent):
                current = read_json(path)
                if current["pending"][:accepted] == pending[:accepted]:
                    del current["pending"][:accepted]
                    _save_state(path, current)
    with queue_lock(path.parent):
        return accepted, len(read_json(path)["pending"])


def monitor_once(
    client, directory: Path, project_root: Path | tuple[Path, ...], *,
    codex_home: Path | None = None, legacy_preferences: Path | None = None,
) -> dict:
    try:
        return _monitor_once(client, directory, project_root, codex_home, legacy_preferences)
    except (OSError, KeyError, TypeError, ValueError) as error:
        raise ConnectorError("monitor could not read or save its local state") from error


def _monitor_once(client, directory, project_root, codex_home, legacy_preferences) -> dict:
    connected_at = utc_text(datetime.now(timezone.utc))
    selected = (project_root,) if isinstance(project_root, Path) else project_root
    roots = tuple(sorted({_bound_root(root, client.project) for root in selected},
                         key=lambda root: (-len(root.parts), str(root))))
    source = _codex_home(codex_home)
    path = directory / "monitor" / client.project / "state.json"
    degraded_count = _capture(
        path, {str(root) for root in roots}, RolloutIndex(source), _read_threads(source, roots),
        _known_handled(legacy_preferences or _legacy_preferences_path()), connected_at,
    )
    accepted, pending = _flush(client, path, _machine(directory))
    return {"accepted_count": accepted, "pending_count": pending, "degraded_count": degraded_count}


def _codex_home(codex_home: Path | None) -> Path:
    return (codex_home or Path(os.environ.get("CODEX_HOME") or Path.home() / ".codex")).expanduser().resolve()


def _project_map(path: Path) -> tuple[dict[Path, str], str, tuple[Path, ...]]:
    try:
        value = read_json(path)
        unassigned = str(UUID(value["unassigned_project_id"]))
        assignments = {}
        for entry in value["projects"]:
            project = str(UUID(entry["project_id"]))
            if project == unassigned:
                raise ValueError("catchall is also assigned to a root")
            for raw in entry["roots"]:
                root = Path(raw).expanduser()
                if not root.is_absolute():
                    raise ValueError("project root must be absolute")
                root = root.resolve()
                if assignments.setdefault(root, project) != project:
                    raise ValueError("conflicting project roots")
        archives = tuple(Path(root).expanduser() for root in value.get("rollout_archives", ()))
        if any(not root.is_absolute() or not root.is_dir() for root in archives):
            raise ValueError("invalid rollout archive")
        return assignments, unassigned, tuple(root.resolve() for root in archives)
    except (OSError, AttributeError, KeyError, TypeError, ValueError) as error:
        raise ConnectorError("monitor project map is invalid") from error


def monitor_projects(client_for, directory: Path, mapping: Path, *, codex_home: Path | None = None) -> dict:
    try:
        return _monitor_projects(client_for, directory, mapping, codex_home)
    except (OSError, KeyError, TypeError, ValueError) as error:
        raise ConnectorError("monitor could not read or save its local state") from error


def _monitor_projects(client_for, directory, mapping, codex_home) -> dict:
    connected_at = utc_text(datetime.now(timezone.utc))
    assignments, unassigned, archives = _project_map(mapping)
    connections = {unassigned: {"None"}}
    for root, project in assignments.items():
        connections.setdefault(project, set()).add(str(root))
    roots = tuple(sorted(assignments, key=lambda root: (-len(root.parts), str(root))))
    source = _codex_home(codex_home)
    threads = _read_threads(source, roots, include_unbound=True)
    files = RolloutIndex(source, archives)
    if threads is not None:
        known = {item[2].name.lower() for item in threads}
        known_ids = {item[1] for item in threads}
        threads.extend(thread for thread in files.unindexed_threads(roots, known) if thread[1] not in known_ids)
    groups = {project: [] for project in connections}
    for item in threads or ():
        groups[assignments.get(item[0], unassigned)].append(item)

    known_handled = _known_handled(_legacy_preferences_path())
    machine = _machine(directory)
    results = {}
    # Save every project's progress locally before doing any network I/O.
    for project, roots_for_project in connections.items():
        path = directory / "monitor" / project / "state.json"
        selected = groups[project] if threads is not None else None
        counts = _capture(path, roots_for_project, files, selected, known_handled, connected_at)
        results[project] = {"thread_count": len(groups[project]), "degraded_count": counts}

    for project, result in results.items():
        path = directory / "monitor" / project / "state.json"
        try:
            accepted, pending = _flush(client_for(project), path, machine)
            result.update(accepted_count=accepted, pending_count=pending)
        except (ConnectorError, OSError, ValueError) as error:
            result["error"] = str(error) if isinstance(error, ConnectorError) else "monitor sync failed"
    return {"projects": results}
