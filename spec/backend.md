# AgentHarbor 后端架构与规约（简版）

本文件描述当前后端及本机接入代码的主要边界。具体工程规则以 [`AGENTS.md`](../AGENTS.md) 为准；前端部分见[前端规约](frontend.md)。

## 架构

```text
本机 Codex → Python 连接程序 → 定时 HTTP 同步 → Go HTTP API → PostgreSQL 任务事件
本机 Codex → 启动 Hook / 记忆 MCP → Go HTTP API → PostgreSQL 记忆条目与材料
Web（经 Next.js 同源代理）/ Desktop → Go HTTP API → 审核材料与管理条目
```

AgentHarbor 的任务采集由用户本机发起，和底座原有的云端派发、daemon 执行链路分开。

| 位置 | 职责 |
| --- | --- |
| `server/cmd/server/` | 装配 Chi 路由与服务。 |
| `server/internal/handler/` | HTTP 入参、鉴权、工作区及项目归属校验、响应。 |
| `server/pkg/db/queries/`、`server/pkg/db/generated/` | SQL 查询及 sqlc 生成代码。 |
| `server/migrations/` | PostgreSQL 表结构迁移。 |
| `server/internal/agentharbormemory/` | 服务端读取 MemoryCore 旧 L3，按项目导入待审核材料。 |
| `tools/agentharbor-connector/` | 本机 Codex Hook、持久队列、定时同步和记忆 MCP。 |

## 大致规约

- 数据库访问沿用 sqlc。SQL 改动后运行 `make sqlc`；迁移提供 up/down 文件，不加外键或级联；新索引用独立迁移执行 `CREATE [UNIQUE] INDEX CONCURRENTLY`。
- 任务同步按事件 ID 幂等入库，只有服务端确认的事件才从本机队列清理。任务上报与记忆读写分开处理，记忆服务异常不应阻断任务上报。
- Codex 可提交待审核材料。人工批准后，独立条目按项目或本人全局范围生效，并保留来源和版本。启动读取只提供项目允许的有效、无争议条目；读取回执随任务事件同步。
- 用实际事件与接口结果区分“已上报”“已接收”“本轮结束”“任务完成”。

## 接口对接

- 沿用 Chi 的 REST/JSON 路由，JSON 字段使用 `snake_case`，由前端 `core` 在边界转换为 `camelCase`。Handler 负责解析方法对应的路径、查询参数和 JSON 正文，校验输入后调用业务与数据层；外部 UUID 在边界解析并处理错误。
- 成功响应沿用 `writeJSON` 和现有 HTTP 状态码；错误沿用 `writeError` 或带稳定 `code` 的 `writeErrorCode`，返回非 2xx 状态及 JSON `error` 字段。不为 AgentHarbor 另造一套响应格式。
- 普通工作区接口使用现有登录或 PAT 鉴权，并检查成员和项目归属；查询按 `workspace_id` 限定范围。项目记忆 MCP 接口使用绑定项目的专用 Bearer token，不能凭客户端传入的项目 ID 改变作用域。
- 新增或修改接口时，明确请求、成功响应、错误状态和鉴权范围；同步更新前端 Zod schema 及对应测试。保留已安装桌面端可连接较新后端的响应兼容性。

从仓库根目录运行与改动范围相称的 `make test`；具体要求以 [`AGENTS.md`](../AGENTS.md) 为准。
