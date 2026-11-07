# AgentHarbor Codex connector

This standalone Python 3.10+ program captures Codex lifecycle hooks in a local durable queue. `run` sends a batch every 30 seconds, removes only event IDs confirmed by the server, and caches task and project-memory responses. `UserPromptSubmit` adds the cached memory to Codex through `additionalContext`.

## Configure

Set these environment variables in the process that launches Codex and the connector:

| Variable | Purpose |
| --- | --- |
| `AGENTHARBOR_PROJECT_ID` | Multica project UUID (required for hooks and sync) |
| `AGENTHARBOR_URL` | AgentHarbor HTTP base URL (required for sync) |
| `AGENTHARBOR_WORKSPACE_ID` | Multica workspace ID for `X-Workspace-ID` (required for sync) |
| `AGENTHARBOR_PAT` | Multica personal access token for Bearer authentication (required for sync) |
| `AGENTHARBOR_STATE_DIR` | Optional local state directory; defaults to the user state directory |

Keep the PAT in the environment or a local secret manager. The queue and caches never store it. The memory cache contains the server's project-memory text, so use a private state directory.

For the tested Codex CLI `0.167.0-alpha.3.2`, add the entries from `hooks.example.json` to `$CODEX_HOME/hooks.json` (or `~/.codex/hooks.json` when `CODEX_HOME` is unset); preserve any hooks already there. Replace `<repo-root>` in both command variants with that machine's absolute repository path. Enable the `hooks` feature, then review and trust the hooks with `/hooks` in Codex. Set `AGENTHARBOR_PROJECT_ID` only in the environment of Codex sessions working on the bound project, and pass `AGENTHARBOR_STATE_DIR` when using a custom state directory. The three configured hooks are `UserPromptSubmit`, `Stop`, and `Interrupt`. See the [OpenAI Hooks documentation](https://learn.chatgpt.com/docs/hooks) for lifecycle behavior and trust requirements.

From the repository root, with the sync environment variables set:

```powershell
py -3 tools/agentharbor-connector/connector.py sync-once
py -3 tools/agentharbor-connector/connector.py tasks
py -3 tools/agentharbor-connector/connector.py run
```

On macOS or Linux, use `python3` in place of `py -3`. Run `sync-once` once before the first prompt to fill the memory cache. `tasks` prints the last fetched tasks JSON. `run` performs an immediate sync, then repeats every 30 seconds until interrupted.

The connector records only prompt/assistant character counts as event evidence. It does not send prompt or response text. The project ID in the hook environment selects the project and its memory cache. Hook writes are local and do not make HTTP requests; an unavailable server leaves events queued for retry.

## Local checks

```powershell
python -m unittest discover -s tools/agentharbor-connector -v
```

The tests use a loopback fake server and fake PAT. They check concurrent hook writes, partial server confirmation, task import, and memory context output without contacting a real AgentHarbor server or running an agent.
