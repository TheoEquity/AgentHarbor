# A 机本机同步与导入验收

日期：2026-11-07
范围：只验收 A 机上的 Codex → 本机连接程序 → 本机模拟云端 → 任务与项目记忆回读。B 机接入另行验收。

## 实际结果

| 检查 | 结果与证据 |
| --- | --- |
| 源码服务 | `agentharbor/local-v1`，提交 `5f0d481ead44cf610397de3e40a864deb16f5346`；当前源码构建的后端 `127.0.0.1:18243/health` 返回 `ok`，独立 PostgreSQL 健康，事件表与索引存在。 |
| MemoryCore | 固定源码 `2002196f` 的 Gateway 在 `127.0.0.1:18420` 返回 `ok`，FTS5 可用。通过 AgentHarbor API 写入 L3，再从 API 和 MemoryCore 原接口按同一项目隔离字段读回相同内容。另一项目隔离字段读回空内容。 |
| 记忆提供给 Codex | 连接程序先从 API 下载项目记忆到本机缓存。真实 Codex CLI `0.167.0-alpha.3.2` 的 `UserPromptSubmit` 与 `Stop` 均触发；提示中没有测试标记，Codex 从 Hook 提供的项目记忆准确回答测试标记。 |
| 任务上传与导入 | 真实 Codex 回合产生 `prompt_started`、`turn_stopped` 两条事件；第一次同步确认 2 条、待传 0 条；随后从 API 导入 1 张 `A · Codex` 任务卡，状态为 `turn_ended_unknown`，发生时间和服务端接收时间均可查。第二次同步确认 0 条新事件。 |
| 访问边界 | 不存在的项目记忆返回 404；未带认证读取项目记忆返回 401。 |

本机验收项目 ID：`f74640ae-fbaa-4c4f-9025-f38c5adcf035`。验收用 PAT 和 MemoryCore 密钥只保存在本机未跟踪的运行目录，没有写入代码仓库。初次验收时通过连接程序的 `tasks` 命令和 API 查看真实记录。

交付目录复核：从项目内的 `app/` 目录重新构建并启动后端，容器工作目录指向 `app/`，健康检查返回 `ok`。重启后 API 仍能读到 1 条任务、2 条事件和相同的项目记忆；连接程序再次执行 `sync-once` 后，返回 `accepted_count=0`、`pending_count=0`、`task_count=1`，本机 `tasks` 缓存含这 1 条任务及 2 条事件。

网页增补复核：从 `app/` 构建并启动前端，访问工作区的 `/agent-tasks` 页面。点击「智能体任务」菜单后，页面从真实 API 展示验收项目、机器 `Jacobin`、`Codex`、`本轮结束 · 完成未知`、事件发生时间和服务端接收时间；未使用页面模拟数据。前端生产构建及 TypeScript 检查通过，新增 API 边界与页面测试通过。网页每 30 秒重新读取已同步记录，页面明确说明不是实时状态。

项目记忆网页复核：从同一前端打开工作区的 `/memory` 页面，选择验收项目后读到 MemoryCore 中原有的 L3 文档，长度 133 个字符；L0–L2 明确显示尚未接入，未填入原型样例。页面提供 L3 整份文档的手动编辑入口；保存交互由组件测试覆盖，本次没有改写验收记忆。

## 使用方式

1. 在独立目录检出 TencentDB-Agent-Memory 的 `feat/server_team` 分支并固定到 `2002196f`。进入其 `MemoryCore` 目录，安装依赖，按 `tdai-gateway.standalone.yaml` 启动 Gateway。为 `TDAI_DATA_DIR`、`TDAI_METADATA_SQLITE_BASE_DIR` 指定持久化目录，设置 `TDAI_GATEWAY_API_KEY`。本机仅验收手工 L3 写读时可使用不连接外部模型的测试 LLM 地址；自动提炼需要真实可用的模型配置。
2. 在代码仓库复制 `.env.example` 为未跟踪的 `.env`，填写随机的 `JWT_SECRET`、PostgreSQL 密码、`AGENTHARBOR_MEMORYCORE_URL=http://host.docker.internal:18420`，以及与 Gateway 一致的 `AGENTHARBOR_MEMORYCORE_API_KEY`。运行 `docker compose -p agentharbor-v1 -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml up -d --build postgres backend`。
3. 在服务端创建工作区、项目和个人访问令牌。按 [连接程序说明](../../tools/agentharbor-connector/README.md)设置 `AGENTHARBOR_URL`、`AGENTHARBOR_WORKSPACE_ID`、`AGENTHARBOR_PROJECT_ID`、`AGENTHARBOR_PAT`。在当前 Codex CLI 版本，把示例 Hook 条目加入 `$CODEX_HOME/hooks.json`，并启用 `hooks` 功能；仅为对应项目启动的 Codex 进程设置 `AGENTHARBOR_PROJECT_ID`。
4. 用 `PUT /api/projects/{id}/memory/core` 保存项目约定，运行 `python tools/agentharbor-connector/connector.py sync-once` 把记忆下载到本机缓存。正常从 Codex 开始任务。再运行一次 `sync-once` 上传 Hook 事件并下载任务；用 `python tools/agentharbor-connector/connector.py tasks` 查看已导入任务。日常定时同步使用 `run` 命令，当前周期 30 秒。

## 仍需验收

- B 机连接 A、两个机器归属同一项目（M01、M02 的跨机部分）。当前服务端只绑定本机回环地址。
- 真实 `Interrupt`、明确完成证据、B 离线补传与乱序到达（M03、M05）。当前 `Stop` 只记“本轮结束、完成未知”。
- B 机真实 Codex 取回并使用 A 项目记忆（M04 的跨机部分）。
- 完整 L0–L3 记忆管理；本机阶段只实现 L3 手工写读。网页任务菜单已完成本机单项目记录展示，双机展示仍待 B 机验收。

本次另核对：连接程序 loopback 测试 3/3 通过；MemoryCore Go 客户端测试通过。服务端 worker 报告在全新 PostgreSQL 上迁移到 618，并通过 3 项数据库与 PAT 路由测试；主分支已通过上表的真实 HTTP 闭环，未独立重跑 worker 的全部数据库测试。前端生产构建、core/views/desktop 类型检查、相关 API 与页面测试通过；完整后端测试集和 B 机验收未运行。
