# Agent 项目已实现模块与模块缺口分析

## 1. 分析说明

本文基于 2026-08-26 工作区中的实际代码重新分析，分析基线为：

- `acore`：`main` 分支，提交 `9409d40`；
- `agent`：仅存在空入口 `agent/main/maig.go`；
- `doc`：现有 Agent、Strategy、Tool、Prompt、Session、Context Window、Event 和 Provider 设计文档。

本次分析先删除旧版 `agent-project-module-gap-analysis.md`，再从当前源码、公开 API、包依赖、测试和应用目录重新得出结论，不把旧文档中的阶段判断作为事实来源。

未查阅外部参考项目。本文只引用仓库内已经存在的设计资料，不声称采用了 Eino、pi 或 DeepSeek Harness 的新增方案。

为避免把不同性质的问题都称为“缺少模块”，本文使用四种状态：

| 状态 | 含义 |
| --- | --- |
| 已实现并接入 | 公开契约、实现和主运行链路均已存在 |
| 契约已实现、尚未接入 | 数据类型或接口存在，但运行代码没有使用 |
| 扩展点已实现、缺具体适配 | 核心接口足够，缺面向某个 Provider、存储或部署环境的实现 |
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

重新分析后的关键结论：

1. **项目当前最大的缺口不在 `acore`，而在 `agent` 应用。** 应用没有模块边界、配置、组件装配和可运行入口，尚未完成端到端验证。
2. **`acore` 当前最明确的集成缺口是 RunEvent。** `agent/runevent` 已定义标准事件，但 Agent 和 Strategy 均未发布这些事件。
3. **生产化缺口主要是具体适配器。** `contextwindow.Estimator`、`session.Service`、`model.LLM` 装饰器和 `tool.Proxy` 等扩展点已经存在，不应重复设计大接口。
4. **Checkpoint、MCP、长期记忆、丰富工具结果和高级策略仍是场景驱动能力。** 它们不是当前最小框架的完整性缺陷。
5. **无需恢复通用 Runtime、Looper 或 RunState 包。** 当前运行状态由具体 RunStrategy 私有管理，符合策略可替换和构建期/运行期分离原则。

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

当前 `acore` 根目录没有 README、版本标签和发布说明。

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

### 5.6 第二优先级：公开模块文档与发布

性质：**工程化缺口**。

`acore` 面向外部用户，但目前缺少：

- 顶层 README 和快速开始；
- 模块选择与组装示例；
- API 稳定性和兼容性承诺；
- 语义化版本和发布流程；
- 安全边界说明；
- Provider 能力矩阵；
- 设计文档状态同步。

例如 `run-event-module-design.md` 仍写着“待确认、只设计不实现”，但 `agent/runevent` 已实际存在。此类状态应在后续文档维护中同步。

### 5.7 按需求引入：上下文贡献与 RAG

性质：**尚未设计**。

当前调用方可以直接构造 Messages，但没有可组合的 Retriever/Context Contributor。只有出现多个上下文来源时，才需要设计：

- Contributor 输入输出；
- 文档、引用和排序；
- 多来源合并、去重和冲突；
- 注入位置；
- 与 Context Window 的执行顺序；
- 超时、权限和租户隔离。

简单“检索后注入再生成”优先做组件，不必新增 RAGStrategy；只有形成查询重写、评估和再检索循环时才定义策略。

### 5.8 按需求引入：History Compactor

性质：**尚未设计，现有 Reducer 无法表达**。

`contextwindow.Result` 只返回 `MessageStart`，因此不能摘要或改写历史。Compactor 需要单独定义：

- 摘要模型或规则；
- 额外 Usage；
- 摘要失败；
- Tool Call/Tool Result 配对；
- 临时请求视图还是写回 Session；
- 原始历史保留和摘要版本。

不应改变 TailReducer 使其同时承担裁剪、摘要、持久化和检索。

### 5.9 按需求引入：丰富 Tool Result 与 Artifact

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

### 5.10 按需求引入：Checkpoint / Interrupt / Resume

性质：**尚未设计**。

当前 pull-based Run 只能在进程内继续，不能跨进程恢复。Checkpoint 至少需要：

- 带 schema version 的私有 Memento；
- Store 与 Codec；
- Run ID、Interrupt ID、Session Key 和 Revision；
- 模型调用和 Tool 副作用前后的安全点；
- Resume 路由、CAS/lease 和重复恢复保护；
- 过期、清理、损坏和版本迁移。

Session Snapshot、Agent Result 和 Checkpoint 不能合并成一个通用 State。Checkpoint 也不能自动保证 Tool 副作用 exactly-once。详细参考 [Eino Checkpoint 持久化分析](eino-checkpoint-persistence-analysis.md)。

### 5.11 按需求引入：MCP

性质：**外部协议适配缺口**。

MCP 应适配为 `tool.Tool` 或 `tool.Service`，不修改 ToolLoop 识别 MCP。适配器负责：

- 连接和能力发现；
- Schema 转换；
- 调用 ID、取消、超时和重连；
- 服务端信任、授权和结果脱敏；
- 资源关闭。

首版建议在构建 Agent 前完成发现并快照到不可变 Tool System。运行期热更新需要另行设计版本化 Catalog。

### 5.12 按需求引入：长期记忆

性质：**尚未设计**。

长期记忆与 Session 历史职责不同，可以按用例实现为：

- 检索/写入 Tool；
- Context Contributor；
- 用户画像、文档或向量存储适配器。

在定义公共接口前，先确认写入时机、用户可见性、租户隔离、删除权、保留周期和敏感数据策略。

### 5.13 按需求引入：其他运行策略

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
| 配置、凭证、CLI/API、资源生命周期 | `agent` 应用 |
| Agent 调用契约和共享 Builder | `acore/agent` |
| 具体运行算法 | `acore/agent/agent-strategy/<strategy>` |
| RunEvent 数据契约 | 现有 `acore/agent/runevent` |
| RunEvent 发布集成 | 待专项设计，优先小型 Agent/Strategy 观察组件 |
| Token Estimator | Provider/模型专属适配包 |
| 数据库/缓存 Session | 独立存储适配包或应用实现 |
| LLM 重试和限流 | `model.LLM` 装饰器 |
| Tool 治理 | `tool.Proxy` |
| Telemetry 导出 | RunEvent 订阅器或适配包 |
| MCP | `tool.Service`/`tool.Tool` 适配包 |
| Retriever/Memory | Tool、Contributor 或独立适配包 |
| Checkpoint | 运行状态语义确认后的独立模块 |

## 8. 推荐实施顺序

### 阶段一：建立应用闭环

1. 编写 `agent` 应用模块与入口专项方案；
2. 确认 `go.mod` 或 `go.work`；
3. 组装 OpenAI Provider、LLM、Tool、Strategy、Prompt 和 Agent；
4. 提供最小 CLI/API；
5. 使用可控服务完成端到端自动化测试。

验收：`agent` 应用可以构建，并完成一次单轮生成和一次 ToolLoop。

### 阶段二：接入标准运行事件

1. 编写 Publisher/Run ID/Sequence/Clock 集成方案；
2. 覆盖成功、失败、取消、早停和 Session Commit 失败；
3. 验证事件唯一终态、顺序、并发隔离和脱敏；
4. 提供一个最小日志或测试订阅器。

验收：每个已开始 Run 都有可关联、完整且有序的生命周期事件。

### 阶段三：生产化会话与上下文

1. 选择首个持久化 Session 后端；
2. 实现首个 Provider/模型 Estimator；
3. 增加 Session 冲突和长历史集成测试；
4. 按部署需要补充 LLM 装饰器、Tool Proxy 和 Telemetry Adapter；
5. 完善 README、示例和版本策略。

验收：会话可跨进程保存，长历史能够在模型窗口内安全运行，关键调用可观测且受限。

### 阶段四：按产品需求选择一个能力

从 Structured Output、Retriever/RAG、History Compactor、Rich Tool Result、MCP、Checkpoint 或长期记忆中选择真实需要的一项，重新检查代码并编写专项方案。

不要批量创建所有候选模块的空接口或占位实现。

## 9. 当前验证结果

在 `acore` 模块实际执行：

```bash
go list ./...
go test ./...
go vet ./...
go test -race ./...
```

结果：

- `go list ./...`：成功，列出 15 个包；
- `go test ./...`：通过；
- `go vet ./...`：通过；
- `go test -race ./...`：未执行成功，当前环境 `CGO_ENABLED=0`，Go 返回 `-race requires cgo`；
- CI 已配置在 Ubuntu 环境执行 `go test -race ./...`；
- 命令输出包含 `Failed to create stream fd: Operation not permitted`，但不影响前三个成功命令的退出状态。

本次只重新生成 Markdown 分析文档，没有修改 `acore` 或 `agent` 业务代码。

## 10. 下一步实现前必须确认的决策

1. `agent` 使用独立 `go.mod` 还是根 `go.work`；
2. 首个应用入口采用 CLI 还是 HTTP API；
3. RunEvent Publisher 的注入层级和错误语义；
4. Run ID 由调用方提供还是框架生成；
5. 首个 Session 后端；
6. 首个 Estimator 面向哪个 Provider/API/模型；
7. 是否需要 Responses API 或其他 Provider；
8. 下一项产品能力是 Structured Output、RAG/MCP、Memory 还是 Checkpoint。

这些选择会影响公开 API 或模块边界，应分别形成设计文档并等待确认后再实现。

## 11. 相关文档

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
