# Agent 项目已实现模块与模块缺口分析

## 1. 分析说明

本文基于 2026-08-31 工作区中的实际代码继续分析，分析基线为：

- `acore`：`main` 分支，提交 `d836e1b`；
- `agent`：仍只有空入口 `agent/main/maig.go`，没有 `go.mod`；
- `acore`：共 15 个 Go 包（含 3 个 `internal` 包），根 README、Apache 2.0 LICENSE 和 CI 已存在；其目标是作为 GitHub 上可由外部项目导入的公开 Go 模块，必须独立构建、按版本发布；
- `doc`：现有 Agent、Strategy、Tool、Prompt、Session、Context Window、Event、Provider 和 Checkpoint 分析文档。

本次在上一版盘点基础上继续核对源码、公开 API、包依赖、测试、README、Git 状态和应用目录，重点补充“最小框架可运行之后，为形成可生产、可扩展 Agent 框架还缺什么”，并纠正上一版对 README 和分析基线的过时描述。

除仓库内已有的 Eino Checkpoint 分析外，本次没有新增查阅外部参考项目；不声称采用了 pi、DeepSeek Harness 或其他项目的新方案。

为避免把不同性质的问题都称为“缺少模块”，本文使用五种状态：

| 状态 | 含义 |
| --- | --- |
| 已实现并接入 | 公开契约、实现和主运行链路均已存在 |
| 契约已实现、尚未接入 | 数据类型或接口存在，但运行代码没有使用 |
| 扩展点已实现、缺具体适配 | 核心接口足够，缺面向某个 Provider、存储或部署环境的实现 |
| 候选公共能力 | 已发现跨实现需求，但公开边界尚需真实用例验证 |
| 尚未设计 | 当前没有稳定契约，应等真实需求出现后再设计 |

## 2. 总体结论

`acore` 已经具备最小可运行 Agent 框架，不再缺少 Agent Builder、运行循环、工具调用、Prompt、Session 或上下文窗口等基础模块。

当前闭环是：

```text
model.Provider
      │ Bind
      ▼
model.LLM
      │
      ├──────────────────────────────┐
      ▼                              │
Agent Builder                        │
  ├── LLM                            │
  ├── RunStrategy                    │
  ├── Prompt Renderer（可选）         │
  └── Model Options（可选）           │
      ▼                              │
不可变 Agent                         │
      ▼                              │
SingleTurn / ToolLoop                │
  ├── Session Load（可选）            │
  ├── Context Window Reduce（可选）   │
  ├── LLM Stream ◄───────────────────┘
  ├── Tool Execute（ToolLoop）
  ├── Agent Stream
  └── Session Append（成功后，可选）
```

继续分析后的关键结论：

1. **既然 `acore` 是对外发布的公开 Go 模块，当前第一优先级是完成首个版本发布。** 当前工作区已经补充标签发布 Workflow、Changelog、发布指南、兼容性说明、包级文档和模块外导入检查，但还没有实际 SemVer 标签与 GitHub Release；完整闭环必须以 `v0.1.0` 发布成功为准。
2. **项目级最大功能缺口仍在 `agent` 应用，而不是 `acore` 最小运行核心。** 应用没有模块边界、配置、组件装配和可运行入口，尚未完成端到端验证。
3. **`acore` 唯一已经有数据契约却没有运行闭环的模块仍是 RunEvent。** 它应先于新增更多领域事件完成发布集成。
4. **`acore` 生产化最短板是适配器和治理实现。** 首要是 Provider/模型 Token Estimator、持久化 Session、Telemetry 订阅器，以及按需提供 LLM 装饰器和 Tool Proxy。
5. **下一批值得专项设计的公共能力是执行预算、Provider 能力/错误语义/公共协议校验和 Structured Output。** 它们会影响多个 Provider 或 Strategy，但当前尚无足够用例授权直接新增包。
6. **Context Contributor/Retriever、History Compactor、Guardrail、丰富 Tool Result/Artifact、Checkpoint、MCP、长期记忆和高级策略仍是场景驱动能力。** 应按依赖顺序逐项设计，而不是批量建立空模块。
7. **README、LICENSE、Strategy 包级文档、安全策略和主要设计状态已经同步。** 后续每次发布仍需持续维护这些公开承诺。
8. **无需恢复通用 Runtime、Looper 或 RunState 包。** 当前运行状态由具体 RunStrategy 私有管理；只有 Checkpoint、Workflow 或跨策略预算出现后，才重新评估共享运行控制层。

## 3. 已实现模块盘点

### 3.1 模型协议：`acore/model`

状态：**已实现并接入**。

已实现：

- Provider 无关的 `Model`、`Message`、`ContentBlock`、`ToolCall` 和 `ToolSpec`；
- 文本、Thinking、图片和 Tool Call 内容类型；
- `Request`、`Result`、`Usage`、`StopReason` 和流事件协议；
- `Provider` 与 `LLM` 分层；
- `Bind`、`Complete` 和并发安全 `ProviderRegistry`；
- Provider 建流错误与流内错误的明确边界；
- 拉取式 `iter.Seq2` Stream 和调用方早停语义。

边界：

- 不包含厂商 HTTP/SSE DTO；
- 不负责 Agent 多轮调度；
- 不负责重试、限流、熔断、Telemetry 或 Token 估算；
- Provider 专属配置不会进入核心 `model.Request`。

### 3.2 OpenAI Provider：`acore/provider/openai`

状态：**已实现并接入模型协议**。

已实现：

- OpenAI Chat Completions 流式 Provider；
- 显式 API Key、Base URL、HTTP Client、Header 和模型目录配置；
- Provider/Model/API 一致性校验；
- System、User、Assistant、Tool 消息和 Tool Call 请求转换；
- SSE 文本、拒绝、Tool Call、Usage 和 StopReason 解析；
- 结构化 `APIError`；
- 非 2xx、错误 Content-Type、异常流、Context 取消和响应体关闭；
- 配置快照和并发 Generate 测试。

当前限制：

- 只实现 Chat Completions，没有 Responses API；
- 没有其他厂商 Provider；
- 没有自动模型发现、重试、限流或 Provider 专属 Token Estimator；
- 部分核心内容类型会因 Chat Completions 映射能力受限而返回 unsupported。

这些限制属于 Provider 适配范围，不应通过扩大 Agent 核心解决。

### 3.3 工具体系：`acore/tool`

状态：**已实现并接入 ToolLoop**。

已实现：

- `Tool`、`Service`、`Spec`、`Call` 和 `Result`；
- Builder 注册 Tool 和 Proxy；
- 成功 Build 后冻结的不可变 `System`；
- Spec 快照、确定性顺序、重复工具检查和 typed nil 检查；
- 有序 Proxy 链、参数改写、短路和有限重试能力；
- Context 取消、错误链、并发执行和输入隔离；
- ToolLoop 与 `model.ToolSpec/ToolCall` 的转换。

当前限制：

- `tool.Result` 只能返回文本；
- 参数只校验为 JSON 对象，不按 Spec 执行完整 JSON Schema 校验；
- 未内置鉴权、审批、超时、限流、审计和沙箱实现；
- Tool Call 幂等性只由具体 Tool 或应用保证。

`tool.Proxy` 已经是治理扩展点，因此不需要再增加通用 Tool Middleware。

### 3.4 Agent 公共契约与 Builder：`acore/agent`

状态：**已实现并接入**。

已实现：

- `Agent.Run`、`Request`、`Result`、`Event`、`Stream` 和 `Complete`；
- 无状态 Messages 与有状态 Session 两种互斥输入；
- Run 级 PromptValues 和可覆盖模型参数；
- Agent Builder 显式注入 LLM、RunStrategy、Prompt Renderer 和默认模型参数；
- Builder 重复配置校验、失败后可恢复和成功后冻结；
- 调用输入、策略输入、流事件和最终结果快照；
- Context 取消、调用方早停、nil Stream、静默结束和非法 Done 保护；
- 同一不可变 Agent 并发 Run。

当前边界：

- Agent Builder 只组装所有策略共享的依赖；
- Tool、Session 和 Context Window 由具体 Strategy Builder 注入；
- Request、Result 和 Agent Event 没有 Run ID 或通用关联元数据；
- 没有 Publisher、Checkpoint 或 Resume。

### 3.5 运行策略

#### SingleTurn：`acore/agent/agent-strategy/singleturn`

状态：**已实现并接入**。

- 执行一次模型生成；
- 透传模型流事件；
- 生成统一 Agent Result；
- 可选注入 Session 和 Context Window；
- 模型返回 Tool Call 时只保留结果，不执行工具。

#### ToolLoop：`acore/agent/agent-strategy/toolloop`

状态：**已实现并接入**。

- 有界 Model↔Tool 循环；
- 同一模型轮次的多个 Tool Call 串行执行；
- 最大模型轮次、最大工具调用数和最大工具结果字节数；
- Tool Call ID 校验和跨轮去重；
- Tool 错误脱敏反馈或 Fail Fast；
- Usage 累加和溢出检查；
- 模型、工具和最终结果 Agent Stream 事件；
- 可选 Session 和每轮 Context Window；
- 早停和取消后停止后续模型调用与工具副作用。

当前限制：

- 没有并行 Tool Call 模式；
- 没有跨 Run 调用账本或幂等协议；
- 没有暂停、人工审批和恢复；
- 没有计划、反思、路由等其他运行算法。

### 3.6 Prompt：`acore/prompt`

状态：**已实现并接入 Agent**。

已实现：

- 最小 `Renderer` 契约；
- `RendererFunc`；
- 不可变 Static Renderer；
- 严格字符串 Template、默认值和缺失变量错误；
- 每个 Run 在进入 Strategy 前渲染一次 System Prompt；
- 输入快照、Context 和并发渲染。

边界：

- 只生成完整 System Prompt；
- Values 只接受显式字符串；
- 不读取 Session、Tool Catalog、检索结果或全局状态；
- 不负责 Message 模板和上下文贡献。

### 3.7 Session：`acore/session`

状态：**核心契约和内存实现已实现，并接入两种 Strategy**。

已实现：

- `Key`、`Revision`、`Snapshot` 和 `Service`；
- `Load` 与 CAS `Append`；
- 并发安全 `MemoryService`；
- 数据深复制、Key 校验、冲突和 Revision 溢出；
- Strategy 在模型调用前加载完整历史；
- 只有 Run 成功且准备产生 RunDone 时才提交本次输入和生成消息；
- Context Window 只影响 Provider 请求视图，不裁剪 Session 原始历史。

当前限制：

- 没有持久化 Store；
- 没有删除、TTL、归档、查询或管理接口；
- Session Append 冲突不会自动重试；
- 失败 Run 和部分生成消息不会写入历史；
- Tool 已产生副作用后再发生提交冲突时，框架无法回滚副作用。

### 3.8 Context Window：`acore/contextwindow`

状态：**Reducer 已实现并接入，Estimator 只有扩展契约**。

已实现：

- `Estimator` 与 `Reducer` 小接口；
- `Apply` 输入隔离和 Reducer 结果安全校验；
- `TailReducer`；
- 根据模型 ContextWindow、输出预留和安全余量计算输入预算；
- 完整估算 System Prompt、Messages 和 Tool Specs 的契约；
- 只在 User 消息边界裁剪；
- 保护当前 Run 新消息、Assistant Tool Call 和 Tool Result；
- ToolLoop 每个模型轮次都重新治理窗口。

当前限制：

- 没有可直接使用的 Provider/模型 Token Estimator；
- TailReducer 只能选择原始消息后缀；
- 不能摘要、改写、合并历史或注入检索内容；
- 模型没有有效窗口元数据时无法安全计算预算。

### 3.9 Event Bus：`acore/event`

状态：**分发机制已实现，未与 Agent 主链路绑定**。

已实现：

- `Event`、`Publisher`、泛型 Handler 和 Subscription；
- 同步、进程内、按精确 Go 类型路由；
- 按订阅顺序执行；
- 处理器错误合并且不跳过后续处理器；
- Context 取消、取消订阅和并发安全；
- 零值 Bus 可用。

边界：

- Event 是旁路通知，不是 Agent 或 Model 的主输出流；
- 没有异步队列、并行分发、跨进程传输、持久化、重试或回放；
- 不应用 Event Handler 实现需要返回决策的审批控制流。

### 3.10 标准运行事件：`acore/agent/runevent`

状态：**契约已实现、尚未接入**。

已定义：

- RunStarted、RunCompleted、RunFailed、RunCanceled；
- ModelTurnStarted、ModelTurnCompleted、ModelTurnFailed；
- ToolCallStarted、ToolCallCompleted；
- Run ID、Sequence、OccurredAt 元数据；
- 脱敏 Failure 和 Tool Call 状态；
- 稳定事件名称和 JSON 字段测试。

实际缺口：

- Agent、SingleTurn 和 ToolLoop 均未依赖 `runevent`；
- 没有 Publisher 注入入口；
- 没有 Run ID、Sequence 和 Clock 的创建/传入机制；
- 没有把 setup error、流错误、Session Commit error、早停和取消映射成标准终态；
- 没有日志、指标或 Trace 订阅适配器。

这是当前最明确的“模块已存在但功能未闭环”问题。

### 3.11 内部支持与工程检查

`acore/internal/clone`、`jsoncheck` 和 `nilcheck` 只服务当前公开包，职责单一，不是可继续堆放任意逻辑的公共模块。

`acore/.github/workflows` 已配置：

- `go build ./...`；
- `go test ./...`；
- `go test -race ./...`；
- `go vet ./...`。

当前 `acore` 根目录已经有 README、快速开始、安全边界说明和 Apache 2.0 LICENSE。常规 CI 在 `main` push 和 pull request 上运行；当前工作区新增的 Release Workflow 监听稳定 SemVer 标签，在独立质量门禁通过后创建 GitHub Release，并验证公共 Go module proxy。Changelog、发布指南、Provider 能力矩阵，以及 `singleturn`、`toolloop` 两个公开子包的包级文档也已经补充。当前剩余缺口是提交这些基础设施并实际发布、验证 `v0.1.0`。

## 4. 当前实际运行数据流

一次 Agent Run 的真实流程如下：

1. Agent 校验 Context、Request 输入形式和模型参数；
2. 对 Request、SessionInput、PromptValues 和模型参数创建隔离副本；
3. Prompt Renderer 每个 Run 渲染一次 System Prompt；
4. 具体 RunStrategy 校验自身依赖；
5. 若使用 Session，先 Load 历史并与本次新消息组合；
6. 每次模型调用前，可选 Context Window Reducer 生成单次 Provider 请求视图；
7. 调用 `model.LLM.Generate` 并把 `model.Event` 包装成 `agent.Event`；
8. SingleTurn 在模型 Done 后结束；
9. ToolLoop 解析 Tool Call、串行执行工具、追加 Assistant/Tool 消息并发起下一轮模型生成；
10. 只有最终成功时才向 Session Append；
11. 产生唯一 RunDone，或通过 Stream error 结束；
12. 调用方提前停止消费时取消内部 Context，不再继续后续副作用。

这说明以下模块已经形成闭环，不应再被列为缺失项：

- Agent Builder；
- Agent Stream；
- SingleTurn；
- ToolLoop；
- Tool/Model 协议转换；
- Prompt；
- Session 基础契约；
- Context Window 基础治理；
- OpenAI Chat Completions Provider。

## 5. 模块缺口

先按“现在是否应进入专项设计”汇总：

| 能力 | 当前状态 | 是否是 acore 核心缺口 | 建议动作 |
| --- | --- | --- | --- |
| 公开模块版本化发布 | 发布基础设施已补充，无实际版本与 Release | 是，发布缺口 | acore 第一优先级，完成 `v0.1.0` 发布与模块外验收 |
| Agent 应用装配 | 应用未实现 | 否 | 项目功能第一优先级，建立端到端闭环 |
| RunEvent 发布集成 | 契约已实现、未接入 | 是，集成缺口 | acore 功能第一优先级 |
| Token Estimator | 有接口、无实现 | 否，适配器缺口 | 选定模型后实现 |
| 持久化 Session | 有接口、只有内存实现 | 否，适配器缺口 | 选定后端后实现 |
| Telemetry/重试/限流/Tool 治理 | 有扩展点、无通用实现 | 否，治理组件缺口 | 按部署需求逐项实现 |
| 执行预算 | 只有 ToolLoop 次数/大小限制 | 候选公共能力 | 在多轮成本控制或 BestOfN 前专项设计 |
| Provider 能力、错误分类与协议校验 | 只有部分模型元数据、Provider 专属错误和分散校验 | 候选公共契约 | 在第二种 API/Provider 或通用重试前设计 |
| Structured Output | 无稳定契约 | 候选公共能力 | 可作为下一项产品能力 |
| Retriever/Context Contributor、Compactor | 无稳定契约 | 否，场景能力 | 长上下文/RAG 用例出现后设计 |
| Guardrail/Policy | Tool 侧已有 Proxy，输入输出侧未设计 | 否，场景能力 | 先明确阻断点和决策语义 |
| Rich Tool Result/Artifact | 文本协议无法表达 | 是，出现多模态结果时的协议缺口 | 有文件/图片/大对象需求后专项设计 |
| Checkpoint/Interrupt/Resume | 未设计 | 否，当前最小闭环不需要 | 人工审批或恢复需求出现后设计 |
| MCP、长期记忆、多 Agent、Workflow | 未设计 | 否，生态/高级编排能力 | 按产品需求引入 |
| Eval/Test Kit | 现有包有单测，无公共评测工具 | 否，开发者体验能力 | 出现跨项目重复测试需求后再抽取 |

这张表中的“不是核心缺口”不表示能力不重要，而是说明现有公开接口已经允许模块外实现，或当前没有足够需求证明它应进入 `acore`。

### 5.1 第一优先级：agent 应用装配

性质：**项目缺口，不是 acore 核心模块缺口**。

`agent/main/maig.go` 只有空 `main`，`agent/` 没有 `go.mod`，工作区根也没有 `go.work`。当前无法从应用层完成可重复的端到端运行。

需要单独设计：

- `agent` 独立 Go 模块或根 `go.work`；
- CLI、HTTP API 或其他入口；
- 配置文件/环境变量到 Provider、Model、Prompt、Tool、Strategy 和 Session 的映射；
- API Key、HTTP Client、超时和资源关闭；
- Agent Stream 的输出协议；
- 应用级错误、日志和退出码；
- 不依赖真实凭证的端到端测试。

应用配置和凭证读取不应进入 `acore`，Agent Builder 也不应变成配置中心或 Service Locator。

### 5.2 第一优先级：RunEvent 发布集成

性质：**acore 现有模块的集成缺口**。

进入实现前必须确定：

- Publisher 注入 Agent Builder、Strategy Builder，还是 RunStrategy 装饰器；
- Run ID 由调用方传入还是框架生成；
- Run ID 是否加入 Agent Request/Result；
- Sequence 和 Clock 的所有权；
- setup error 发生在 RunStarted 前还是后；
- Agent Stream 早停是否发布 RunCanceled；
- Publisher 错误是否中断主 Run、与主错误合并或只上报；
- ToolLoop 专属工具事件如何由通用观测层获得；
- 同一 Agent 并发 Run 时如何隔离元数据；
- 默认不记录 Prompt、消息、Thinking、工具参数/结果和原始错误。

不建议先增加更多 RunEvent 类型。现有九类事件已经足够验证首次集成。

### 5.3 第二优先级：Provider/模型 Token Estimator

性质：**扩展点已实现、缺具体适配**。

没有 Estimator 时，TailReducer 无法直接用于真实模型。首个 Estimator 应面向确定的 Provider/API/模型，并覆盖：

- System Prompt；
- 所有 Message 和 ContentBlock；
- Tool Specs；
- 图片或其他非文本输入；
- Provider 的消息包装开销；
- 未知模型和未知内容类型；
- 安全余量和估算偏差测试。

不要在 `contextwindow` 核心内提供声称适用于所有 Provider 的粗略实现。

### 5.4 第二优先级：持久化 Session 适配器

性质：**扩展点已实现、缺生产实现**。

`session.Service` 已足够支持外部实现。选择数据库或缓存后，需要定义：

- Key 的租户隔离；
- Revision/CAS 的事务映射；
- Message 编码版本和迁移；
- 大小限制、加密和敏感数据；
- TTL、删除、归档和管理能力；
- 同 Session 并发 Run 的应用策略。

不能在 Session Append 冲突后盲目重跑整个 ToolLoop，因为已成功执行的 Tool 副作用可能被重复。

### 5.5 第二优先级：可观测性与运行治理适配

性质：**现有扩展点上的具体组件缺口**。

推荐归属：

| 能力 | 推荐扩展点 |
| --- | --- |
| Run 日志、指标、Trace | RunEvent 订阅器 |
| LLM 重试、限流、熔断、Trace | `model.LLM` 装饰器 |
| Tool 鉴权、超时、限流、审计、有限重试 | `tool.Proxy` |
| Tool 参数 JSON Schema 校验 | 独立 Proxy 或 Tool 实现 |
| 高风险 Tool 预执行审批 | 返回决策的显式 Policy/Approval 接口 |
| Shell、文件、网络隔离 | 受限 Tool、独立执行器或部署沙箱 |

不建议创建同时包装 Agent、LLM、Tool、Session 和 Event 的全能 Middleware。

### 5.6 候选公共能力：执行预算与配额

性质：**候选公共能力，尚未形成稳定接口**。

现有 `ToolLoopLimits` 只限制模型轮次、工具调用数和单个工具结果字节数；`context.Context` 可以限制总时间，`model.Request.MaxTokens` 只能限制单次模型输出。当前不能直接表达：

- 一次 Run 的累计输入/输出 Token 上限；
- 多模型轮次、Reflection 或 BestOfN 的总生成预算；
- 不同工具的权重、配额或并发限制；
- 成本上限和 Provider 价格版本；
- 命中预算时返回失败、部分结果还是可恢复中断。

在出现真实多轮成本治理需求前，不建议立即增加通用 `budget` 包。专项设计时应把“可由框架可靠计算的计数/Token”与“依赖应用价格、租户配额和计费规则的成本”分开；预算检查应发生在下一次有副作用或付费操作之前，而不是只在 Run 结束后统计。

### 5.7 候选公共契约：Provider 能力、错误分类与协议校验

性质：**候选公共契约，第二种 API/Provider 前需要复核**。

当前 `model.Model` 只有 Reasoning、InputModalities、ContextWindow 和 MaxOutputTokens 等部分元数据。它没有稳定表达 Tool Call、Structured Output、图片输出、并行 Tool Call、Prompt Cache 等能力。ToolLoop 目前只能发起请求并由具体 Provider 接受或拒绝。

当前错误也主要由具体 Provider 定义，例如 OpenAI `APIError`。如果未来实现跨 Provider 的重试、Fallback 或路由，装饰器无法仅依靠统一契约稳定判断：

- 错误是否可重试；
- 是否为限流、认证、输入过大、内容过滤或能力不支持；
- 是否已经建立流以及是否可能产生计费或部分输出；
- Fallback 到另一个模型是否语义安全。

相邻问题是 Provider 无关协议的结构校验尚未集中。`model` 只执行少量 Request/Tool Spec 校验，角色与 ContentBlock 组合、ToolCall/Tool Result 关联、图片字段等主要由 OpenAI 适配器检查。第二个 Provider 可能复制这些逻辑或形成不一致语义。后续应区分：

- `model` 负责所有 Provider 共享的结构不变量；
- Provider 负责自身 API 不支持的内容和参数范围；
- Strategy 负责运行算法特有的不变量，例如 Tool Call ID 跨轮唯一。

不应预先枚举所有厂商能力和错误，也不必现在新增宽泛 Validation 包。建议在引入 OpenAI Responses API 或第二个 Provider 时，以两个实际实现的交集验证最小 Capability/Error/Validation 契约；Provider 专属字段仍留在适配包。

### 5.8 候选产品能力：Structured Output 与输出校验

性质：**候选公共能力，可作为下一项产品功能**。

现有模型协议只能返回普通 `model.Message`，没有输出 Schema、JSON 模式、Validator 或 Repair 语义。若应用需要稳定 DTO，需要明确：

- Schema 是 Provider 请求能力还是 Agent 结果校验；
- 只包装一次模型生成，还是可包装任意子策略最终结果；
- Validator 接口、错误路径和脱敏反馈；
- 最大修复轮次、累计 Usage 和预算；
- 流式 Delta 如何处理，最终结构化值放入何种 Result；
- Provider 原生 Structured Output 与通用 Validate/Repair 的降级关系。

优先把 Validator 设计为小型可替换组件，不在核心内实现不完整的 JSON Schema。是否建立独立 Strategy，应由“是否拥有多轮修复控制流”决定。

### 5.9 按需求引入：Guardrail / Policy

性质：**输入输出侧尚未设计，Tool 侧已有扩展点**。

`tool.Proxy` 已能承载工具鉴权、参数审查、超时和审计；普通 `agent.Event` 与 RunEvent 只适合观察，不能返回阻断决策。若需要 Prompt 注入检测、内容合规、PII 处理、模型输出校验或高风险动作审批，应先区分：

- 纯变换、允许/拒绝、需要人工审批三种结果；
- Agent 输入、模型请求、模型结果、工具执行前后的不同拦截点；
- 同步拒绝与可恢复中断；
- 原始敏感内容是否允许进入日志和事件；
- Policy 失败是业务拒绝还是系统错误。

不要把需要决策的 Guardrail 实现成 Event Handler，也不要用一个全能 Middleware 同时修改 LLM、Tool、Session 和 Agent。

### 5.10 按需求引入：Eval / Test Kit

性质：**开发者体验能力，不是运行核心缺口**。

仓库已有较完整的单元测试和示例，外部实现也可直接用公开 `model.LLM`、`tool.Service`、`session.Service` 编写替身。当前没有证据需要公共测试框架。只有多个外部项目重复实现以下能力时，才考虑抽取独立包：

- Scripted/Fake LLM 和流协议断言；
- Tool/Session 契约测试套件；
- 确定性 Agent 场景、Golden Case 和回归数据；
- 质量评分、延迟/Token 统计和离线报告。

评测数据集、业务评分器和 CI 阈值通常属于应用或独立工具，不应进入 Agent 运行链路。

### 5.11 第一优先级：完成首个公开 Go 模块版本

性质：**发布基础设施已实现，实际发布尚未完成**。

`acore` 面向外部用户，当前工作区已经具备顶层 README、快速开始、组装示例、安全边界说明、LICENSE、常规 CI、Release Workflow、Changelog 和发布指南。只有在这些改动提交到 `main`，并由 `v0.1.0` 标签成功触发 GitHub Release 和模块代理验证后，才形成“提交可验证、版本可追踪、外部可安装”的完整闭环。

Go 库的主要发布物是带版本标签的模块源码，不是必须上传单独二进制。当前 `go.mod` 位于 GitHub 仓库根，因此版本应使用仓库根 SemVer 标签，例如首个确认版本 `v0.1.0`；不使用子目录标签，也不应同时在代码中维护另一套容易漂移的版本号。只有未来确实提供 CLI/Server 命令时，才另外构建平台二进制和 checksum。

发布专项方案至少需要覆盖：

- **版本规则**：首个版本号、`v0` 阶段兼容性承诺、何时升级 major/minor/patch、弃用周期；
- **发布触发**：人工审批后创建不可变 `vX.Y.Z` 标签，由 GitHub Actions 校验标签格式与提交状态并创建 GitHub Release；
- **质量门禁**：`go build ./...`、`go test ./...`、`go vet ./...`、`go test -race ./...`，以及需要支持的 Go 版本矩阵；
- **公开 API 检查**：导出标识符文档、破坏性变更检查、README 示例和 Provider 能力矩阵；
- **模块完整性**：`go.mod`/`go.sum`、LICENSE、无本地 `replace`、无敏感文件和无不应发布的生成物；
- **模块外验收**：在临时独立模块执行 `go get github.com/JIAOZAI1/acore@vX.Y.Z`，构建最小 SingleTurn/ToolLoop 示例；
- **发布信息**：Changelog 或自动生成 Release Notes，列明新增、修复、破坏性变化、最低 Go 版本和已知限制；
- **供应链权限**：Workflow 使用最小 `contents: write` 权限，不保存长期 GitHub Token，不移动或覆盖已发布标签；
- **发布后验证**：确认 GitHub Release、`go list -m github.com/JIAOZAI1/acore@vX.Y.Z` 和 Go module proxy/checksum 数据可解析。

当前工作区已经补齐：

- `agent/agent-strategy/singleturn` 与 `toolloop` 的包级文档；
- `v0` API 稳定性、安全支持和漏洞报告说明；
- Provider/API/内容类型能力矩阵；
- RunEvent 设计文档的实际实现状态。

`CHANGELOG.md` 已建立 `0.1.0` 版本章节并记录发布日期；Release Workflow 会拒绝没有对应 Changelog 章节的标签。当前剩余步骤是提交改动、确认远端 CI、创建 annotated tag 并由 GitHub 完成发布后验证。

发布工作流不应根据未审核提交自动推导并推送版本标签；版本决策属于发布者。建议流程是“确认版本与 Release Notes → 创建标签 → CI 重新验证该标签 → 创建 GitHub Release”。

### 5.12 按需求引入：上下文贡献与 RAG

性质：**尚未设计**。

当前调用方可以直接构造 Messages，但没有可组合的 Retriever/Context Contributor。只有出现多个上下文来源时，才需要设计：

- Contributor 输入输出；
- 文档、引用和排序；
- 多来源合并、去重和冲突；
- 注入位置；
- 与 Context Window 的执行顺序；
- 超时、权限和租户隔离。

简单“检索后注入再生成”优先做组件，不必新增 RAGStrategy；只有形成查询重写、评估和再检索循环时才定义策略。

### 5.13 按需求引入：History Compactor

性质：**尚未设计，现有 Reducer 无法表达**。

`contextwindow.Result` 只返回 `MessageStart`，因此不能摘要或改写历史。Compactor 需要单独定义：

- 摘要模型或规则；
- 额外 Usage；
- 摘要失败；
- Tool Call/Tool Result 配对；
- 临时请求视图还是写回 Session；
- 原始历史保留和摘要版本。

不应改变 TailReducer 使其同时承担裁剪、摘要、持久化和检索。

### 5.14 按需求引入：丰富 Tool Result 与 Artifact

性质：**需要协议级专项设计**。

当 Tool 需要返回图片、文件、结构化 JSON 或大对象时，需要同时考虑：

- Tool Result 的可判别内容块；
- 与 `model.ContentBlock` 的转换；
- Agent Event 和 Session 序列化；
- Context Window 估算；
- MIME、大小、所有权和生命周期；
- 内联内容与 Artifact 引用；
- Provider 不支持时的降级。

不能只把 `tool.Result.Content string` 改为 `any`。

### 5.15 按需求引入：Checkpoint / Interrupt / Resume

性质：**尚未设计**。

当前 pull-based Run 只能在进程内继续，不能跨进程恢复。Checkpoint 至少需要：

- 带 schema version 的私有 Memento；
- Store 与 Codec；
- Run ID、Interrupt ID、Session Key 和 Revision；
- 模型调用和 Tool 副作用前后的安全点；
- Resume 路由、CAS/lease 和重复恢复保护；
- 过期、清理、损坏和版本迁移。

Session Snapshot、Agent Result 和 Checkpoint 不能合并成一个通用 State。Checkpoint 也不能自动保证 Tool 副作用 exactly-once。详细参考 [Eino Checkpoint 持久化分析](eino-checkpoint-persistence-analysis.md)。

### 5.16 按需求引入：MCP

性质：**外部协议适配缺口**。

MCP 应适配为 `tool.Tool` 或 `tool.Service`，不修改 ToolLoop 识别 MCP。适配器负责：

- 连接和能力发现；
- Schema 转换；
- 调用 ID、取消、超时和重连；
- 服务端信任、授权和结果脱敏；
- 资源关闭。

首版建议在构建 Agent 前完成发现并快照到不可变 Tool System。运行期热更新需要另行设计版本化 Catalog。

### 5.17 按需求引入：长期记忆

性质：**尚未设计**。

长期记忆与 Session 历史职责不同，可以按用例实现为：

- 检索/写入 Tool；
- Context Contributor；
- 用户画像、文档或向量存储适配器。

在定义公共接口前，先确认写入时机、用户可见性、租户隔离、删除权、保留周期和敏感数据策略。

### 5.18 按需求引入：其他运行策略

当前 [Agent 运行策略路线图](agent-run-strategy-roadmap.md) 中只有 SingleTurn 和 ToolLoop 已实现。

| 策略 | 当前判断 |
| --- | --- |
| Structured Output | 最适合作为下一项候选；需决定是独立策略还是 Validator/Repair 装饰器 |
| Reflection | 有 Draft/Critique/Revise 用例后实现 |
| Router | 至少存在两个成熟子策略后实现 |
| PlanExecute | 先稳定计划 DTO、步骤限制和错误语义 |
| BestOfN | 先定义候选、Scorer、并发和总 Token 预算 |
| RAG | 简单场景优先 Retriever/Contributor |
| Handoff | 依赖 Agent 身份、Registry、循环检测和跨 Agent 预算 |
| Workflow | 有明确节点、边、汇合和状态需求后实现 |
| Human Approval | 依赖 Checkpoint/Resume，不阻塞 goroutine 等待 |

这些策略不是当前核心框架的必做清单。

## 6. 不属于当前缺口的模块

以下能力已经存在或当前不应引入：

- Agent Builder：已实现；
- 模型-工具循环：ToolLoop 已实现；
- Provider Registry：已实现；
- Prompt Renderer：已实现；
- Session 基础契约：已实现；
- Context Window Reducer：已实现；
- 通用 Event Bus：已实现；
- 通用 Runtime/Looper/RunState：当前不需要；
- 全局 Config 包：属于应用层，不进入 `acore`；
- Service Locator：违背显式依赖；
- 全能 Middleware：应由 LLM 装饰器、Tool Proxy 和 RunEvent Observer 分担；
- 动态插件系统：没有运行期加载需求；
- 通用 DAG 引擎：没有稳定节点和调度需求；
- 统一 State 接口：会混淆 Session、Memory、Checkpoint 和 Workflow State；
- `common/util/manager/component`：会破坏单一职责。

## 7. 推荐模块归属

| 能力 | 推荐归属 |
| --- | --- |
| GitHub Actions、SemVer 标签、GitHub Release、Release Notes | `acore` 仓库发布工程 |
| 模块版本解析与分发 | Git 标签 + Go module proxy；不放入运行时代码 |
| 配置、凭证、CLI/API、资源生命周期 | `agent` 应用 |
| Agent 调用契约和共享 Builder | `acore/agent` |
| 具体运行算法 | `acore/agent/agent-strategy/<strategy>` |
| RunEvent 数据契约 | 现有 `acore/agent/runevent` |
| RunEvent 发布集成 | 待专项设计，优先小型 Agent/Strategy 观察组件 |
| Token Estimator | Provider/模型专属适配包 |
| 数据库/缓存 Session | 独立存储适配包或应用实现 |
| LLM 重试和限流 | `model.LLM` 装饰器；通用实现前先补足错误分类需求 |
| Tool 治理 | `tool.Proxy` |
| Telemetry 导出 | RunEvent 订阅器或适配包 |
| 执行预算 | 先放具体 Strategy；确认跨策略复用后再抽小接口 |
| Provider 能力/错误分类/公共结构校验 | `model` 最小公共契约 + Provider 专属详情 |
| Structured Output | Validator 组件；需要修复循环时使用 Strategy/Strategy 装饰器 |
| 输入/输出 Guardrail | Agent/Strategy 边界的显式 Policy；Tool 侧继续用 Proxy |
| Rich Tool Result/Artifact | `tool`、`model` 和序列化链路的联合协议升级 |
| MCP | `tool.Service`/`tool.Tool` 适配包 |
| Retriever/Memory | Tool、Contributor 或独立适配包 |
| Eval/Test Kit | 独立开发/测试包，不进入生产运行链路 |
| Checkpoint | 运行状态语义确认后的独立模块 |

## 8. 推荐实施顺序

### 阶段一：建立公开模块发布闭环

1. 编写 GitHub 版本发布专项方案，确认首个版本、兼容性规则、触发方式和权限；
2. 补齐公开包文档、Provider 能力矩阵、设计状态和 Release Notes；
3. 增加标签发布 Workflow，在发布提交上重新执行 build、test、race 和 vet；
4. 创建不可变 SemVer 标签与 GitHub Release；
5. 从仓库外临时模块通过版本号安装并构建 README 示例；
6. 验证 GitHub 和 Go module proxy 能解析该版本。

验收：外部项目可以使用 `go get github.com/JIAOZAI1/acore@vX.Y.Z` 获得可重复构建的版本，GitHub Release 能追溯到通过门禁的唯一提交。

### 阶段二：建立应用闭环

1. 编写 `agent` 应用模块与入口专项方案；
2. 确认 `go.mod` 或 `go.work`；
3. 组装 OpenAI Provider、LLM、Tool、Strategy、Prompt 和 Agent；
4. 提供最小 CLI/API；
5. 使用可控服务完成端到端自动化测试。

验收：`agent` 应用可以构建，并完成一次单轮生成和一次 ToolLoop；同时作为 `acore` 的模块外消费者验证公开 API。

### 阶段三：接入标准运行事件

1. 编写 Publisher/Run ID/Sequence/Clock 集成方案；
2. 覆盖成功、失败、取消、早停和 Session Commit 失败；
3. 验证事件唯一终态、顺序、并发隔离和脱敏；
4. 提供一个最小日志或测试订阅器。

验收：每个已开始 Run 都有可关联、完整且有序的生命周期事件。

### 阶段四：生产化会话与上下文

1. 选择首个持久化 Session 后端；
2. 实现首个 Provider/模型 Estimator；
3. 增加 Session 冲突和长历史集成测试；
4. 按部署需要补充 LLM 装饰器、Tool Proxy 和 Telemetry Adapter。

验收：会话可跨进程保存，长历史能够在模型窗口内安全运行，关键调用可观测且受限。

### 阶段五：按产品需求选择一个能力

- 若下一步引入第二种 Provider/API 或通用重试：先设计 Capability、错误分类与公共协议校验；
- 若下一步增加多轮/并行生成：先设计总执行预算；
- 若下一步需要稳定业务 DTO：优先 Structured Output；
- 若下一步需要知识注入：选择 Retriever/Contributor，再判断是否需要 RAGStrategy；
- 若下一步需要文件、图片或大对象工具结果：设计 Rich Tool Result/Artifact；
- 若下一步需要人工审批或跨进程恢复：先设计 Checkpoint/Interrupt/Resume；
- MCP、长期记忆、Guardrail 和其他策略按真实产品场景分别进入专项设计。

不要批量创建所有候选模块的空接口或占位实现。

## 9. 当前验证结果

在 `acore` 模块实际执行：

```bash
go list ./...
go build ./...
go test ./...
go vet ./...
go test -race ./...
```

结果：

- `go list ./...`：成功，列出 15 个包；
- `go build ./...`：通过；
- `go test ./...`：通过；
- `go vet ./...`：通过；
- `go test -race ./...`：默认环境 `CGO_ENABLED=0` 无法执行；显式设置 `CGO_ENABLED=1` 后又因本机没有 `gcc` 失败，未完成本地竞态验证；
- CI 与 Release Workflow 均配置在 Ubuntu 环境执行 `go test -race ./...`；
- 所有公开包均已有 package doc；
- Release Workflow YAML 可解析，所有内嵌 Shell 通过 `bash -n` 语法检查；
- 使用临时独立模块和本地 `replace` 导入全部公开包并执行 `go test ./...`：通过；
- `git tag --list` 为空，当前没有可供外部固定使用的 SemVer 版本；
- 尚未执行带版本号的远程 `go get`、GitHub Release 和 module proxy 验证，因为当前没有发布标签。

本次实现了发布 Workflow、Changelog、发布与安全文档、README 版本信息和两个公开 Strategy 子包的包级文档；没有修改 Agent 运行逻辑，也没有创建提交、标签或 GitHub Release。

## 10. 已确认发布决策与后续待确认事项

已确认并按此实现发布基础设施：

1. 首个计划公开版本为 `v0.1.0`，采用 SemVer，`v0` 阶段的破坏性变化必须明确记录；
2. 发布者手工创建 annotated tag，Release Workflow 只验证标签和创建 GitHub Release；
3. 最低版本为 Go 1.26；
4. 当前只发布 Go 模块源码，不构建 CLI/Server 二进制。

后续功能实现前仍需确认：

1. `agent` 使用独立 `go.mod` 还是根 `go.work`；
2. 首个应用入口采用 CLI 还是 HTTP API；
3. RunEvent Publisher 的注入层级和错误语义；
4. Run ID 由调用方提供还是框架生成；
5. 首个 Session 后端；
6. 首个 Estimator 面向哪个 Provider/API/模型；
7. 是否需要 Responses API 或其他 Provider，以及是否已到设计 Capability、错误分类和公共协议校验的时机；
8. 是否已经出现跨模型轮次的总 Token/成本预算需求；
9. 下一项产品能力是 Structured Output、RAG/MCP、Memory、Rich Tool Result、Guardrail 还是 Checkpoint。

这些选择会影响公开 API 或模块边界，应分别形成设计文档并等待确认后再实现。

## 11. 相关文档

- [公开模块发布指南](../RELEASING.md)
- [版本变更记录](../CHANGELOG.md)
- [安全支持策略](../SECURITY.md)
- [Agent 公开契约与 Builder 设计](agent-public-contract-builder-design.md)
- [Agent ToolLoop 策略设计](agent-tool-loop-strategy-design.md)
- [Agent 运行策略路线图](agent-run-strategy-roadmap.md)
- [Prompt 模块设计](prompt-module-design.md)
- [Session 模块设计](session-module-design.md)
- [Context Window 模块设计](context-window-module-design.md)
- [Event 模块设计](event-module-design.md)
- [标准运行事件设计](run-event-module-design.md)
- [OpenAI Provider 设计](openai-provider-design.md)
- [Eino Checkpoint 持久化分析](eino-checkpoint-persistence-analysis.md)
