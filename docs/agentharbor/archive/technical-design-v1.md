# AgentHarbor 技术方案：两机 Codex 第一版

> 历史方案：保留首版设计及当时的状态描述，不作为当前全部实现状态。2026-11-07 确认的新记忆架构见[下一阶段技术方案 v2](technical-design-v2.md)；下文的整份 L3 管理、定时缓存和按提示注入路径不再是后续目标。原有验收证据不因此升级为 v2 已通过。

日期：2026-11-07
状态：原始两机实施建议。A 机阶段已实现并通过本机同步与导入验收，实际结果见[本机验收记录](local-acceptance-a.md)；B 机与完整 M01–M05 仍未验证，代码尚未推仓。下文是两机目标设计，不应把未落地的部分视为已实现。

A 机阶段的最小实现与下文建议有几处差别：连接程序使用 Python 标准库和本机 JSON 持久队列，30 秒同步一次；服务端暂存一张事件表，机器名随事件保存；项目到 MemoryCore 的隔离标识由项目 UUID 确定性生成，暂不建映射表；任务通过 API/CLI 回读，网页任务菜单尚未完成。这些选择只证明本机闭环，双机与完整界面仍按验收文档继续。

## 1. 实现边界

第一版让 A 机运行网页、API、PostgreSQL 和腾讯 MemoryCore Gateway，模拟云端；A、B 两机各运行原来的 Codex 和一个本地连接程序。用户仍在 Codex 中开始工作。连接程序只观察、保存、定时同步事件，并从 A 获取同项目记忆提供给 Codex；网页没有派发或控制 Agent 的能力。项目代码留在各机器现有工作目录和 Git 仓库中，云端只保存可选仓库链接，不同步整个目录。

```text
A/B Codex → 本机 Hook → 本机 SQLite 待传队列 → 定时批量上报 → A 的 API/PostgreSQL → 网页
网页写记忆 → A 的 API → MemoryCore Gateway（独立数据卷）
MemoryCore → A 的 API → 定时更新 A/B 本机缓存 → UserPromptSubmit Hook → Codex 本轮上下文
```

建议在选定版本的 [Multica](https://github.com/multica-ai/multica) 源码上扩展：复用现有工作区、项目、登录、API 和网页骨架；新增被动采集的数据与页面。其现有 daemon 负责领取平台安排的 run，不能直接当作用户自行启动的 Codex 任务记录；两者的数据不混写。[运行机制依据](https://multica.ai/docs/daemon-runtimes)

## 2. 先验证 Codex 入口

动底座前，在 **A、B 实际使用的 Codex 入口和版本**上各做一次小实验：

1. 启用并信任 Hook，验证真实提示是否触发 `UserPromptSubmit`，正常停止是否触发 `Stop`，中断是否触发 `Interrupt`；记录实际取得的 `cwd`、`session_id`、`turn_id`。
2. 用 `UserPromptSubmit` 返回一条只有 A 项目记忆中存在的测试约定，检查 Hook 输出与随后 Codex 对该约定的使用。仅查到缓存不能算记忆进入上下文。
3. 检查 `Stop.last_assistant_message` 能否提供明确的“Agent 汇报完成”证据。`Stop` 本身只表示本轮停止；如果只有不稳定的自然语言表述，建议增加一个由 Codex 在完成时主动调用的本机完成报告入口，由它写入结构化完成事件。未调用时状态保持“本轮结束、完成未知”，不能靠关键词猜测通过验收。这个入口只报告结果，不派发或验收任务。
4. 启动选定版本的 MemoryCore Gateway，验证 L3 首次写入、版本读取和重启后的持久性。不同项目的隔离仍需单独验证，但不扩张本轮“一个项目”的 M04 最小验收条件。

[Codex 官方 Hook 文档](https://learn.chatgpt.com/docs/hooks)给出了上述事件、字段和 `additionalContext` 入口；两台机器上的可用性仍需实测。`transcript_path` 的文件格式不是稳定接口，首版不依赖解析完整会话文件。若实际入口不支持这些 Hook，先记录受影响的 M02–M04，再决定是否改采集入口，不先大规模写页面。

## 3. 本机连接程序

建议用一个可在两机运行的 Go 程序，提供 Hook 回调和后台同步两种运行方式；Codex 适配只做事件映射，SQLite 队列、项目关联、重试和记忆缓存由公共部分处理。将来增加另一种 Agent 时，先复用公共部分，只补该 Agent 的采集与上下文提供入口；这不是首版实现范围。

| 本机数据 | 用途 |
| --- | --- |
| 稳定 `machine_id` 与显示名 | 区分 A、B；改显示名不产生新机器 |
| 明确的“目录根 → 项目 ID”绑定 | 用 Hook 的 `cwd` 匹配已绑定目录，未命中就不猜项目 |
| SQLite `outbox` | Hook 先落盘事件；每条有稳定事件 ID 和本机递增序号 |
| 项目记忆缓存 | 保存最近一次已确认获取的记忆内容、版本和时间，供 Hook 本地读取 |

Hook 不访问云端：`UserPromptSubmit` 写入“本轮开始”，从**当前项目**缓存选取少量记忆，返回合法的 `additionalContext`；`Stop` 写入“本轮停止”；`Interrupt` 写入“本轮中断”。Hook 错误写本机日志，不让网络等待卡住 Codex。初始以 **60 秒一次**作为待实测的同步周期；后台程序每周期发送一批待传事件和轻量连接信息，没新事件时不重传历史正文。同一后台周期另向 A 查询当前项目的记忆版本，变化时更新缓存；任务上传和记忆读取分别处理错误，MemoryCore 暂不可用时仍可上报任务。B 刚连接时需要先完成一次记忆更新，才有可用缓存。

首版按“**一个 Codex 会话是一张任务卡，多轮提示是该卡的执行轮次**”组织。用户若在同一会话里做两个互不相关的工作，首版不会自动拆成两张卡；实践中应为新工作开新会话。此规则是实现假设，需用 A、B 的真实使用方式确认。

## 4. 事件、状态和服务器存储

每条事件建议包含 `event_id`、`machine_id`、`project_id`、`agent_type=codex`、`session_id`、`turn_id`、`local_seq`、`event_type`、`occurred_at` 和短证据；A 端另记不可由客户端覆盖的 `received_at`。首版事件种类只需“提示开始、轮次停止、中断、明确完成汇报、记忆上下文返回、采集错误”。没有明确完成证据时不产生“完成”事件。标题可取首轮提示的有限字数，并标明它是原文片段；不能可靠取得时显示会话标识，不上传整段提示、完整对话、工具结果或仓库文件。

建议只增加四组数据，不把被动任务写入原有的 `issue/run`，也不在 PostgreSQL 复制记忆正文：

| 数据 | 最少内容 |
| --- | --- |
| `ah_machine` | 工作区、机器稳定 ID、显示名、最后一次服务端接收时间 |
| `ah_binding` | 工作区、机器、项目、Agent 类型、本机目录展示值 |
| `ah_event` | 上述来源事件、证据、发生时间和接收时间；对 `(machine_id, event_id)` 做唯一约束 |
| `ah_memory_scope` | AgentHarbor 项目 ID 与 MemoryCore 的 `team_id`、共享逻辑 `agent_id`、`user_id` 的映射；记忆正文和层级版本保存在 MemoryCore |

首版只在任务详情呈现实际采集到的原始事件及短证据；尚未决定是否把完整会话原文写入 MemoryCore L0。记忆页的 L0 只显示 MemoryCore 已实际保存的内容，没有就显示空状态，不把任务事件冒充完整对话。任务列表可从同一 `machine_id + session_id` 的事件生成，不急于增加任务投影表。M03 的状态只由状态事件计算：新一轮开始为“进行中”；`Stop` 为“本轮结束、完成未知”；`Interrupt` 为“中断”；只有明确来源证据才显示“Agent 上报完成”。收到较小 `local_seq` 的补传事件仍保存进时间线，但不把当前状态倒退。页面同时显示来源发生时间、A 的接收时间和信息截至时间。

## 5. API 与页面落点

新增 API 均放在现有登录鉴权、工作区成员检查之下；每次写入再校验 `project_id` 属于请求工作区。建议的最小接口是：

| 接口 | 作用 |
| --- | --- |
| `PUT /api/agentharbor/machines/{id}` | 幂等登记机器及其已确认的目录绑定 |
| `POST /api/agentharbor/sync` | 批量接收任务/读取事件并逐条返回确认 ID；不依赖 MemoryCore 健康状态 |
| `GET /api/agentharbor/tasks` | 跨项目任务列表与详情所需事件，可按项目、机器、状态筛选 |
| `GET /api/agentharbor/machines` | 机器、绑定与最后接收时间 |
| `GET /api/projects/{id}/memories` | A 后端按项目映射读取 MemoryCore 实际存在的 L0–L3 内容 |
| `PUT /api/projects/{id}/memory/core` | A 后端将手工确认的项目约定写入 MemoryCore L3 |
| `GET /api/projects/{id}/memory-snapshot` | 连接程序按版本/摘要取得可给 Codex 使用的少量项目记忆 |

上传时先在 A 的事务内校验归属和写入，重复事件按唯一键返回已确认；B 收到确认后才从本机待传队列移除。若 A 已写入而确认丢失，B 重传同一 `event_id`，A 不生成第二条。按 `local_seq` 计算状态，不以网络到达顺序覆盖。同步接口每次可限量取待传项，断网恢复后继续分批补齐。

网页复用现有项目查询与项目详情，在 `packages/core/` 放新接口类型和查询，在 `packages/views/` 放共用业务视图，由 `apps/web/` 接路由与导航。新增「任务」「机器」「同步」入口；项目详情补「任务记录」「参与者」「项目记忆」。首版只接 Web，不以桌面端发布为验收条件。所有页面从真实 API 读取，保留“最后已知”时间，不能把未上报当成实时空闲、失败或完成。

## 6. 记忆选择与提供

**用户已确定：首版使用腾讯 MemoryCore。** A 上单独运行选定版本的 Gateway；它是记忆正文与版本的唯一保存位置。AgentHarbor 后端持有 MemoryCore 私有凭据，浏览器和 B 连接程序只访问 AgentHarbor API，不直接获得 MemoryCore 凭据。建议一个 AgentHarbor 项目对应一个 MemoryCore team 和一个**共享逻辑 agent**，A/B 两机的 Codex 都读这一组项目记忆；真实来源机器仍由 AgentHarbor 任务事件记录。A 在 PostgreSQL 只保存项目与 MemoryCore 身份映射。该映射和跨项目隔离必须实测，当前是设计建议，不是已验证行为。[MemoryCore 接口与隔离字段](https://github.com/TencentCloud/TencentDB-Agent-Memory/blob/feat/server_team/MemoryCore/v3-api-memorycore-doc.md)

M04 的最小路径是：网页为当前项目手动保存一条 L3 约定；A 调用 MemoryCore `/v3/core/write`，用 `/v3/core/read` 取回内容与版本；B 通过 A 的 `memory-snapshot` 更新缓存，在下一次 `UserPromptSubmit` 提供给 Codex。MemoryCore L3 是一个核心记忆文档，首版按文档管理这条约定，不把它伪装成任意多条独立原子记忆。注入只取少量必要内容，并限制文本长度。一次任务的完成说明保留在任务事件里，不自动升级成长期记忆。

网页保存成功后，B 在下一次记忆更新取得当前版本；`UserPromptSubmit` 只读 B 本地缓存并返回所选内容。记忆读取记录作为事件进入同一 `outbox`，区分“本机缓存命中”“Hook 已返回内容”和“Codex 实际得到并使用内容”的证据；没有后者证据时页面显示未确认。M04 要用一条 B 原先不知道的约定，在真实 Codex 轮次中验证。网络断开时只能使用已有缓存，并标注缓存时间。

四层页面读取 MemoryCore 的实际数据。本地核对的源码公开 API 有 L0 `conversation/add`、L1 `atomic/query/search/update`、L2 `scenario/ls/read/write`、L3 `core/read/write`；L1 `update` 只更新已有条目，没有直接新增入口。该源码的 L2 `scenario/write` 也会对不存在的文件返回 404，与接口文档的写入描述不一致。因此首版 M04 先用可首次写入的 L3；L0/L1/L2 有数据才展示，不能用模拟数据补齐。完整 PRD 中“手动新增 L1/L2”仍需额外实现和验证，不能把首版 M04 通过说成完整记忆管理已完成；自动提炼和向量检索也不属于 M01–M05。上述限制以本地核对的 MemoryCore 源码提交 `2002196f` 为依据，最终实施版本仍需重新核对。[MemoryCore 接口文档](https://github.com/TencentCloud/TencentDB-Agent-Memory/blob/feat/server_team/MemoryCore/v3-api-memorycore-doc.md)、[L2 写入源码](https://github.com/TencentCloud/TencentDB-Agent-Memory/blob/2002196f2d7c22ea267cfcaf67c5a6fa861df681/MemoryCore/src/gateway/v2-router.ts#L1933)

腾讯附带的 Codex Proxy 是另一种接法，会改变 Codex 的模型请求路径；其文档还说明 Default 模式的会话初始化失败后会直接透传，跳过记忆注入。首版沿用已规划的 Codex Hook，把 MemoryCore 放在后端，避免要求用户每次改用代理入口或切换模式；两台实际 Codex 入口仍须验证 Hook。[腾讯 Codex 接入说明](https://github.com/TencentCloud/TencentDB-Agent-Memory/blob/feat/server_team/agents/codex/README.md)

## 7. A/B 部署、顺序与验证

1. 固定底座和 MemoryCore 各自的源码/镜像版本；从改动后的底座源码构建 A 的网页/API 镜像，不混用不同版本的预构建镜像。A 的 PostgreSQL 与 MemoryCore 数据卷都要持久化。当前两套本地预览只验证过原版界面，不能直接作为第一版集成验收。
2. A 启动 MemoryCore Gateway 后先检查健康状态、L3 写入读取、项目隔离和服务重启持久性。Gateway 只供 A 后端访问；B 仅连接 A 的局域网网页/API 入口。MemoryCore standalone 配置需要兼容的 LLM 服务地址、模型和密钥，具体是否用 DeepSeek 尚未选定；首次验收只依赖手工 L3 写入与读取，不以自动提炼结果作为通过条件。部署时还要确认搜索所需 FTS5 是否可用。[MemoryCore 运行要求](https://github.com/TencentCloud/TencentDB-Agent-Memory/blob/feat/server_team/MemoryCore/README.md)
3. A、B 各配置自己的本机连接程序、机器 ID、工作区、目录绑定和凭据。浏览器用原有登录；连接程序使用已有 PAT 鉴权，凭据保存在机器的受保护配置中，不进公开仓库。PAT 代表整个账号，首版按个人局域网使用处理；扩展到第三方用户前须重评授权边界。[鉴权依据](https://multica.ai/docs/auth-tokens)
4. 先完成 Hook 与 MemoryCore 可行性实验，再实现机器/绑定、事件入库与补传，随后实现 MemoryCore 项目映射、记忆缓存与上下文提供，最后接真实页面。每完成一段用 A、B 真机证据对照 M01–M05，不以 HTML 原型或单机模拟通过验收。
5. 验证两机同项目、各自真实 Codex 任务、结束/中断/未知状态、MemoryCore 记忆跨机提供、B 断网恢复后去重和状态不倒退。关键检查需有实际日志或数据库/API 结果；未取得证据的项目记为未验证。

若把改动后的底座源码推到公开仓库，应保留完整许可证、通知和适用的界面标识；产品页面不需要“接入 Multica”的设置流程，但公开分发仍须满足其[许可证](https://github.com/multica-ai/multica/blob/main/LICENSE)。推仓前还需核对暂存区和历史中没有预览数据库、日志、凭据或本机会话。此次仅写技术方案，未实施、未部署、未执行 M01–M05。

## 8. 项目实际怎样拼起来

**一个主代码仓、一套 A 机服务、一份 A/B 通用连接程序。**以固定版本的 Multica 源码作为主代码仓底座，在干净分支上做改动；保留其原有 `server/`、`packages/`、`apps/web/` 结构。MemoryCore 以固定版本作为 A 机独立服务运行，AgentHarbor 主仓只保存无密钥的部署配置和调用代码。现有 `work/vendor/` 是调研用源码，`work/multica-preview/` 与 `work/tencent-memory-preview/` 是原版运行预览，`work/ui-prd/` 是模拟交互原型，都不能原样当成最终仓库或直接打包发布。现有 AgentHarbor 公开仓只有 README，导入代码应先在本地形成可审查的分支并保留上游来源，审查后再处理与该仓的历史合并；不直接覆盖远端。

| 来源 | 拼装方式 | 首版处理 |
| --- | --- | --- |
| Multica 主仓 | 复用工作区、登录、项目、PostgreSQL、API 和 Web 框架；在原结构内加功能 | 主底座。原有 `issue/run` 和 daemon 不承担被动任务采集；其“运行时”页也不充当本项目的机器页 |
| Codex | 两机各配置 Hook，并由同一个连接程序处理 SQLite 队列、同步和记忆缓存 | 唯一实际接入的 Agent；先在真实入口验证 Hook |
| 现有 HTML 原型 | 参考导航、信息层级和交互文案，在底座组件里实现真实页面 | 不把 HTML 作为 iframe 嵌入；模拟按钮和原型中已不属于首版的验收操作不进入成品 |
| codex-task-monitor | 参考本地任务观察与状态显示；若实际 Codex 入口不支持 Hook，再评估其只读本地状态方案 | 不直接合并 Swift、.NET 界面或把它当云端同步程序 |
| 腾讯 MemoryCore | A 机上的独立 Gateway，保存项目记忆正文和版本；A 后端负责身份映射与受控调用 | 首版必需；先用 L3 完成 M04，自动提炼和 L1/L2 新增能力另行验证 |

改动落点尽量沿用底座边界：

| 位置 | 只放什么 |
| --- | --- |
| `server/migrations/`、`server/pkg/db/queries/`、`server/internal/handler/`、`server/cmd/server/router.go` | 机器/绑定/事件/记忆身份映射、工作区校验、同步/任务接口，以及访问 MemoryCore 的后端适配 |
| `server/cmd/agentharbor-connector/` | 同一 Go 模块下的 A/B 通用本机程序；Hook 回调与后台同步共用队列代码 |
| `packages/core/` | 新 API 客户端、响应校验和项目范围查询 |
| `packages/views/` | 项目详情补充与任务、机器、同步、记忆视图 |
| `apps/web/` 与现有路径/导航定义 | Web 路由、主菜单入口；首版主菜单按 PRD 收敛 |

先完成“一个真实 Codex 事件 → 本机队列 → A 接口 → 任务页”的纵向闭环，再加 B 断网补传；随后完成“A 后端写入 MemoryCore L3 → B 从 A 缓存 → Codex Hook 提供”的闭环。最后才调整整个导航和页面样式。这样每一步都有真实数据可核对。A 部署的是从同一份改动源码构建的服务及固定版本的 MemoryCore；B 只需 Codex 与连接程序，不需要为观察本地任务而启动底座的派发 daemon。
