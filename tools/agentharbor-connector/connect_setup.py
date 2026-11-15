"""Install a project-scoped Codex memory connection from clipboard data."""

import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
from datetime import datetime, timezone
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import Request, urlopen
from uuid import UUID


SERVER = "agentharbor_memory"
SECTION = f"mcp_servers.{SERVER}"
HEADINGS = re.compile(r"(?m)^\[([^\]\r\n]+)\][ \t]*(?:#.*)?$")


def clipboard_text() -> str:
    if sys.platform == "win32":
        command = ["powershell", "-NoProfile", "-Command", "Get-Clipboard -Raw"]
    elif sys.platform == "darwin":
        command = ["pbpaste"]
    elif shutil.which("wl-paste"):
        command = ["wl-paste", "--no-newline"]
    elif shutil.which("xclip"):
        command = ["xclip", "-selection", "clipboard", "-o"]
    else:
        raise ValueError("No clipboard reader is available")
    try:
        result = subprocess.run(command, capture_output=True, text=True, check=True, timeout=5)
    except (OSError, subprocess.CalledProcessError, subprocess.TimeoutExpired) as exc:
        raise ValueError("Could not read the clipboard") from exc
    return result.stdout


def parse_bundle(raw: str) -> dict[str, str]:
    try:
        bundle = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise ValueError("Clipboard does not contain AgentHarbor connection info") from exc
    if not isinstance(bundle, dict) or bundle.get("version") != 1:
        raise ValueError("Unsupported AgentHarbor connection info")
    try:
        project_id = str(UUID(bundle["project_id"]))
        api_url = bundle["api_url"].rstrip("/")
        token = bundle["token"]
        expires_at = datetime.fromisoformat(bundle["expires_at"].replace("Z", "+00:00"))
    except (KeyError, TypeError, AttributeError, ValueError) as exc:
        raise ValueError("AgentHarbor connection info is incomplete") from exc
    url = urlsplit(api_url)
    if (
        url.scheme not in {"http", "https"} or not url.hostname
        or url.username or url.password or url.query or url.fragment
        or not isinstance(token, str) or not token.startswith("ahm_")
        or expires_at.tzinfo is None or expires_at <= datetime.now(timezone.utc)
    ):
        raise ValueError("AgentHarbor connection info is invalid or expired")
    return {"project_id": project_id, "api_url": api_url, "token": token}


def verify_bundle(bundle: dict[str, str]) -> None:
    request = Request(bundle["api_url"] + "/api/agentharbor/memory/context")
    request.add_unredirected_header("Authorization", "Bearer " + bundle["token"])
    try:
        with urlopen(request, timeout=10) as response:
            result = json.loads(response.read(32769))
    except HTTPError as exc:
        raise ValueError(f"Project memory connection failed: HTTP {exc.code}") from exc
    except (URLError, OSError, ValueError) as exc:
        raise ValueError("Project memory service is unavailable or returned invalid data") from exc
    if not isinstance(result, dict) or result.get("project_id") != bundle["project_id"]:
        raise ValueError("Project memory response does not match the selected project")


def toml_string(value: str) -> str:
    return json.dumps(value, ensure_ascii=False)


def config_text(existing: str, script: Path, project_file: Path, token_file: Path, api_url: str) -> str:
    headings = list(HEADINGS.finditer(existing))
    spans = []
    main_found = False
    for index, heading in enumerate(headings):
        name = heading.group(1)
        if name not in {SECTION, SECTION + ".env"}:
            if name.startswith(SECTION + "."):
                raise ValueError("Existing AgentHarbor MCP configuration has unsupported sections")
            continue
        end = headings[index + 1].start() if index + 1 < len(headings) else len(existing)
        if name == SECTION:
            main_found = True
            if "memory_mcp.py" not in existing[heading.start():end]:
                raise ValueError("Existing AgentHarbor MCP server uses a different command")
        spans.append((heading.start(), end))
    if spans and not main_found:
        raise ValueError("Existing AgentHarbor MCP configuration is incomplete")
    for start, end in reversed(spans):
        existing = existing[:start] + existing[end:]
    command = "py" if sys.platform == "win32" else "python3"
    args = (["-3"] if sys.platform == "win32" else []) + [str(script)]
    block = (
        f"[{SECTION}]\n"
        f"command = {toml_string(command)}\n"
        f"args = {json.dumps(args, ensure_ascii=False)}\n"
        'default_tools_approval_mode = "auto"\n\n'
        f"[{SECTION}.env]\n"
        f"AGENTHARBOR_API_URL = {toml_string(api_url)}\n"
        f"AGENTHARBOR_PROJECT_CONFIG = {toml_string(str(project_file))}\n"
        f"AGENTHARBOR_MEMORY_TOKEN_FILE = {toml_string(str(token_file))}\n"
    )
    return existing.rstrip() + ("\n\n" if existing.strip() else "") + block


def write_text(path: Path, content: str, private: bool = False) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", dir=path.parent, delete=False) as stream:
        temporary = Path(stream.name)
        stream.write(content)
    try:
        if private:
            os.chmod(temporary, 0o600)
        temporary.replace(path)
    finally:
        temporary.unlink(missing_ok=True)


def connect(project_root: Path, raw: str | None = None) -> str:
    project_root = project_root.resolve()
    script = Path(__file__).resolve().with_name("memory_mcp.py")
    if not script.is_file():
        raise ValueError("AgentHarbor memory MCP script is missing")
    bundle = parse_bundle(clipboard_text() if raw is None else raw)
    project_file = project_root / ".codex" / "agentharbor-project.json"
    config_file = project_root / ".codex" / "config.toml"
    token_file = Path.home() / ".codex" / "agentharbor-memory-tokens" / (bundle["project_id"] + ".json")
    if project_file.exists():
        try:
            current_project = json.loads(project_file.read_text(encoding="utf-8"))
        except (OSError, ValueError) as exc:
            raise ValueError("Existing AgentHarbor project binding is invalid") from exc
        existing_root = current_project.get("local_root") if isinstance(current_project, dict) else None
        if (
            not isinstance(existing_root, str)
            or current_project.get("project_id") != bundle["project_id"]
            or Path(existing_root).resolve() != project_root
        ):
            raise ValueError("This directory is already bound to another AgentHarbor project")
    existing = config_file.read_text(encoding="utf-8") if config_file.exists() else ""
    updated = config_text(existing, script, project_file, token_file, bundle["api_url"])
    verify_bundle(bundle)
    write_text(token_file, json.dumps({"project_id": bundle["project_id"], "token": bundle["token"]}) + "\n", private=True)
    write_text(project_file, json.dumps({"project_id": bundle["project_id"], "local_root": str(project_root)}, indent=2) + "\n")
    write_text(config_file, updated)
    return bundle["project_id"]
