# AgentHarbor 前端架构与规约（简版）

本文件描述当前前端代码的主要边界。具体工程规则以 [`AGENTS.md`](../AGENTS.md) 为准；后端部分见[后端规约](backend.md)。

## 架构

```text
Web（Next.js，经同源代理）/ Desktop（Electron）
    → 共享业务页面 packages/views
    → API、查询与类型 packages/core
    → Go HTTP API
```

| 位置 | 职责 |
| --- | --- |
| `apps/web/` | Next.js 路由、布局和 Web 平台适配。 |
| `apps/desktop/` | Electron 主进程、桌面路由和平台适配。 |
| `packages/views/` | Web/Desktop 共用的业务页面与组件；依赖 `core` 和 `ui`。 |
| `packages/core/` | 平台无关逻辑、API 客户端、响应 schema、TanStack Query 和共享客户端状态。 |
| `packages/ui/` | 基础控件、图标和样式；不放业务逻辑。 |

当前移动端是独立的 Expo/React Native 客户端，尚未接入上述 AgentHarbor 共享页面。

## 大致规约

- 共享页面通过导航适配器跳转；`next/*`、`react-router-dom` 和 Electron API 留在各自 app。`views` 不定义 Zustand store。
- TanStack Query 管服务端数据；Zustand 管筛选、草稿等客户端状态。工作区内的 Query key 包含 `wsId`，不把服务端数据另存一份到 Zustand。
- Web/Desktop 共用行为放共享包；移动端只复用适合 React Native 的类型和纯逻辑。命名、组件和中文文案遵循[开发规范](../apps/docs/content/docs/developers/conventions.zh.mdx)。

## 接口对接

- 沿用 REST/JSON 和 `packages/core/api/client.ts` 的 `fetch` 封装；Web 的 `/api/*` 经 Next.js 同源代理到 Go 服务。业务页面通过 `core` 的 API 与 Query 调用接口。
- 请求的方法、路径、参数和 JSON 正文与后端约定；凭据、CSRF 和工作区标识交给现有 API 客户端处理，不由页面逐个拼装。
- 响应在 `core` 边界用 Zod schema 与 `parseWithFallback` 校验，并为可选字段和未知枚举提供兼容处理。网络 JSON 使用 `snake_case`；AgentHarbor 的任务、事件和记忆快照在边界转换为 TypeScript 的 `camelCase` 字段。
- 非 2xx 响应由 API 客户端抛出带状态码和响应体的 `ApiError`；调用方按实际错误处理。新增或修改接口时同步更新 schema 和异常响应测试，并考虑已安装桌面端与较新服务端共存。

从仓库根目录运行与改动范围相称的 `pnpm typecheck`、`pnpm lint`、`pnpm test`；具体要求以 [`AGENTS.md`](../AGENTS.md) 为准。
