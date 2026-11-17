# AgentHarbor

AgentHarbor collects task records and reviewed project memory from local agents. Codex runs in the project code directory; the connector syncs task events to the Go API and provides tools to search memory or submit material for review.

## Current implementation

- The Python Codex connector queues task events locally and syncs them every 30 seconds.
- The Go API stores task events, pending memory material, approved entries, and revisions in PostgreSQL.
- Web and Desktop share the task and memory pages. New sessions receive a small set of approved semantic entries; other memory is available through search.
- Tencent MemoryCore L3 is no longer a runtime dependency. Earlier L3 plans and acceptance results remain as historical records.

## Code map

| Path | Role |
| --- | --- |
| `apps/` | Web and Desktop entry points; independent Mobile client and documentation site |
| `packages/` | Shared API and state, business pages, UI primitives, and workspace tooling |
| `server/` | Go API, SQL queries, and migrations |
| `tools/agentharbor-connector/` | Local Codex connector and memory tools |
| `e2e/`, `scripts/`, `examples/` | End-to-end tests, development scripts, and examples |
| `deploy/`, `docker/` | Deployment configuration |
| `spec/` | Brief frontend and backend conventions |
| `docs/agentharbor/` | Requirements, terminology, and historical plans and acceptance records |

Start with the [development guide](agent.md) for current code entry points, [repository rules](AGENTS.md) for engineering constraints, and the [requirements](docs/agentharbor/requirements.md) for product scope. The [frontend](spec/frontend.md) and [backend](spec/backend.md) specs cover the main boundaries; the [connector guide](tools/agentharbor-connector/README.md) covers local setup. Earlier [acceptance results](docs/agentharbor/archive/local-acceptance-a.md) describe the previous MemoryCore-based version.

The [AgentHarbor archive](docs/agentharbor/archive/) preserves earlier designs and acceptance records. Upstream Multica introductions and screenshots are in the [Multica archive](docs/archive/multica/).

See [LICENSE](LICENSE) and [NOTICE](NOTICE) for source license and attribution.
