# AgentHarbor
Personal cloud dashboard for multi-machine agent tasks and shared project memory.

## First version

- A local Codex connector records task events and syncs them in batches every 30 seconds.
- The server stores project, machine, agent, and session task records and supports readback to the connector.
- The web task menu shows which machine and agent worked on each project.
- Project memory can be maintained in Tencent MemoryCore and retrieved by the connector.

The local A-machine flow has been checked. Connecting a second machine is the next acceptance step.

## Start here

- [Requirements](docs/agentharbor/requirements.md)
- [Minimum acceptance and two-machine deployment plan](docs/agentharbor/acceptance.md)
- [Local A-machine acceptance record](docs/agentharbor/local-acceptance-a.md)
- [Codex connector setup](tools/agentharbor-connector/README.md)

See [LICENSE](LICENSE) and [NOTICE](NOTICE) for the source license and attribution.
