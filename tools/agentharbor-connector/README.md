# AgentHarbor Codex connector

The connector uses Python 3.10+ and the standard library. It queues Codex lifecycle events locally and syncs them to AgentHarbor every 30 seconds. The separate memory connection lets Codex search readable entries, submit materials for review, and load a small amount of current memory at the first prompt of a session.

## Project configuration

Set these variables in the environment that launches Codex for the project. Pass the first five to the process that runs task sync.

| Variable | Purpose |
| --- | --- |
| `AGENTHARBOR_PROJECT_ID` | Project UUID for lifecycle events and task sync |
| `AGENTHARBOR_URL` | AgentHarbor HTTP base URL for task sync |
| `AGENTHARBOR_WORKSPACE_ID` | Workspace UUID for task sync |
| `AGENTHARBOR_PAT` | Multica personal access token for task sync |
| `AGENTHARBOR_STATE_DIR` | Optional private directory for the local queue, task cache, and startup read receipts |
| `AGENTHARBOR_API_URL` | AgentHarbor HTTP base URL for the scoped memory API, usually the same as `AGENTHARBOR_URL` |
| `AGENTHARBOR_PROJECT_CONFIG` | Path to a local JSON file with `project_id` and `local_root`. Use the absolute local project directory for `local_root`. |
| `AGENTHARBOR_MEMORY_TOKEN_FILE` | Path to a private JSON file containing `project_id` and `token` |

The queue stores prompt and assistant character counts as event evidence. Keep the PAT and memory token out of version control. To obtain the memory token, send `POST /api/projects/{project_id}/memory/access-token` using a human user's authenticated session or PAT and the `X-Workspace-ID` header. Save the response to the token file. Replace that file when the token expires. The hook and MCP server use this memory token for memory calls. They check the configured project directory and token file before sending requests. Start Codex inside the bound project.

Merge [hooks.example.json](hooks.example.json) into the project’s `.codex/hooks.json` or your Codex home’s `hooks.json`. Replace `<repo-root>` with the absolute path to this checkout in both command variants so the hook can locate the script from a different working directory. Review and trust the hooks in Codex using `/hooks`. The example uses `UserPromptSubmit`, `Stop`, and `Interrupt`. [OpenAI Hooks documentation](https://learn.chatgpt.com/docs/hooks)

The first `UserPromptSubmit` for each project and session attempts a scoped context read with a network budget of at most 0.8 seconds within the hook’s 3-second timeout. When the local project binding is valid, the hook returns tool guidance and the actual session ID through `hookSpecificOutput.additionalContext`. Any available startup memory goes into the same output. The task event is queued before the read. After an empty or failed startup read, Codex can still search memory through MCP. The local `memory-reads/` directory records the fetched version and entry IDs and revisions. Its `emitted` field records whether the hook returned guidance, and `context_emitted` records whether that output included memory text. Task sync uploads those outcomes and entry references with the project, machine, session, turn, and timestamp. Receipts contain no memory text and appear in the task timeline. Confirm delivery to the model separately when testing live Codex.

## Memory tools

Add the stdio server to the trusted project’s `.codex/config.toml` and restart Codex. The example uses a path placeholder for the connector script. On Windows, use `command = "py"` and `args = ["-3", "<path-to-memory_mcp.py>"]`. Keep the scripts together in their connector directory and start Codex from the configured project. Use `/mcp` in Codex to check that the two tools appear. [OpenAI MCP documentation](https://learn.chatgpt.com/docs/extend/mcp)

```toml
[mcp_servers.agentharbor_memory]
command = "python3"
args = ["<path-to-memory_mcp.py>"]
env_vars = ["AGENTHARBOR_API_URL", "AGENTHARBOR_PROJECT_CONFIG", "AGENTHARBOR_MEMORY_TOKEN_FILE"]
default_tools_approval_mode = "auto"
```

- `search_project_memory` accepts optional `query` and `type` (`episodic`, `semantic`, or `procedural`). Codex can use it whenever prior work could help. Results include the entry revision and source material IDs.
- `submit_project_memory_material` takes one `source_text` and optional `context`. Codex decides when a confirmed decision, durable fact, reusable procedure, or correction is worth submitting. An explicit “记一下” request uses the same tool. A new submission enters the review queue as `pending`. Include actual `session_id` or `turn_id` when available, and the `related_entry_id` and known `related_revision` when proposing a correction. To retry a submission, reuse the returned `request_id` and the same fields.

## Task sync

From the repository root, with the task sync environment variables set, run:

```powershell
py -3 tools/agentharbor-connector/connector.py sync-once
py -3 tools/agentharbor-connector/connector.py tasks
py -3 tools/agentharbor-connector/connector.py run
```

On macOS or Linux, use `python3` in place of `py -3`. `run` performs an immediate sync, then repeats every 30 seconds until interrupted. Each sync sends a batch within the server’s request size limit. Events stay queued until the server confirms their IDs. `tasks` prints the last fetched task list. Upgraded connectors fetch startup memory directly and ignore any `memory-<project-id>.json` files left by earlier versions.

## Local checks

```powershell
python -m unittest discover -s tools/agentharbor-connector -v
```

The suite runs the hook and MCP processes against a loopback HTTP server using fake credentials. It covers event queueing and sync, startup context and read receipts, scoped search, and material submission retry.
