"""Local queue locking and atomic state persistence."""

from contextlib import contextmanager
import json
import os
from pathlib import Path
import tempfile
import time
from uuid import UUID, uuid4


class ConnectorError(Exception):
    pass


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
