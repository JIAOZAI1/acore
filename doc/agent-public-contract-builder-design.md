# Agent 公开契约与 Builder 设计方案

> 后续演进：本文档记录 Agent 首个里程碑的设计基线。当前 `Request` 已支持互斥的无状态 `Messages` 和有状态 `Session` 输入以及 Run 级 `PromptValues`；Agent Builder 支持可选 `prompt.Renderer`，`SetSystemPrompt` 保留为静态便捷入口；`SingleTurnStrategy` 的 Session Service 由 `SingleTurnBuilder` 直接注入。以当前实现、[Session 模块设计方案](session-module-design.md)和 [Prompt 模块设计方案](prompt-module-design.md)为准。

## 1. 背景与结论

当前 `acore` 已有可组合的 `model.LLM`、`tool.Service`、`event.Publisher` 以及公开 OpenAI Provider，但还没有 Agent 领域的公开调用契约和统一组装入口。`agent-project-module-gap-analysis.md` 只给出了 P0 路线，并明确要求在修改业务代码前单独确认 Agent API 与运行语义。

本方案收敛为里程碑 1：

1. 新增公开包 `github.com/JIAOZAI1/acore/agent`；
2. 定义可由模块外实现和替换的 `Agent` 接口；
3. 以 Agent 级可早停 `Stream` 作为主 API，同时提供 `Complete` 便捷函数；
4. 提供启动期 `Builder`，显式注入必填 `model.LLM`、`RunStrategy`、System Prompt 和默认模型参数；
5. Builder 产出一个不可变、可并发 Run 的通用 Agent，单轮算法由可替换的公开 `SingleTurnStrategy` 实现；
6. 首版不注入 `tool.Service` 或 `event.Publisher`，不在尚未实现对应语义时暴露无效配置项。

这一阶段会得到真正可运行的无工具单轮 Agent，而不是只有类型定义、运行时返回“未实现”的空壳。

## 2. 目标

1. 模块外调用方可组合任意 `model.LLM` 与 `RunStrategy`，也可直接使用内置 `SingleTurnStrategy`；
2. 外部实现可通过公开类型完整实现 `Agent` 接口，不依赖 `internal` 或非导出标识符；
3. 调用方可消费模型增量事件、提前停止，或通过 `Complete` 直接取得完整结果；
4. Agent 级 Stream 与 `model.Stream` 形成明确包装边界，不把 Provider 流直接当作 Agent 流；
5. Builder 对必填组件、typed nil、重复配置、参数和构建后冻结进行稳定校验；
6. 请求、默认参数、模型事件和运行结果均按边界复制，不暴露 Agent 内部状态；
7. 同一建造后 Agent 可并发处理多个独立 Run，每次 Run 不共享可变执行状态。

## 3. 非目标

本次不实现：

- Tool Calling 循环、`tool.Service` 注入和 Tool 错误回馈策略；
- Agent 领域事件与 `event.Publisher` 集成；
- 最大模型轮次、最大工具调用数或 Token 预算；
- Session/History 的加载与提交；
- Checkpoint、Resume、持久化、记忆、上下文裁剪或压缩；
- 多 Agent、图编排、规划器或动态插件；
- 应用级环境变量、密钥、YAML/JSON 配置加载；
- 修改 `model`、`tool`、`event` 或 Provider 公开契约。

单轮模型结果如果包含 Tool Call，本次 Agent 将其作为终端 Output 原样报告，`ToolCalls` 计数会反映实际数量，但不执行工具或自动发起下一轮模型请求。

## 4. 模块与依赖边界

计划新增：

```text
acore/agent/
├── agent.go         # Agent、Request、Result、Event、Stream 和 Complete
├── builder.go       # 公开 Builder、稳定错误与构建冻结
├── run.go           # 非导出通用 Agent 和策略 Stream 契约保护
├── single_turn.go   # 公开 SingleTurnStrategy 和 model.Stream 包装
├── clone.go         # 非导出深拷贝边界
├── agent_test.go    # 流、完整结果、错误、取消和早停
├── builder_test.go  # 组件校验、快照、冻结和并发 Run
└── example_test.go  # 模块外组装与自定义 Agent 验收
```

依赖方向：

```text
model.LLM ──┐
             ├──► Builder ──► 非导出 configuredAgent
RunStrategy ─┘                         │
                                            ▼
                                       RunStrategy.Run
```

约束：

- `agent` 首版只依赖 Go 标准库、`acore/model` 和已有内部快照/typed-nil 辅助；
- `model`、`tool`、`event`、Provider 不反向依赖 `agent`；
- 公开方法签名不使用非导出类型或 `internal` 包；
- Builder 只组合已构建的 `model.LLM` 和 `RunStrategy`，不了解 Provider 配置、凭证或 Registry；
- 未来 Tool Loop 通过新的公开策略实现，不修改 Builder 的策略选择逻辑，不恢复已删除的 `looper`/`runtime` 包。

## 5. 公开 API

建议契约：

```go
package agent

import (
    "context"
    "iter"

    "github.com/JIAOZAI1/acore/model"
)

type Agent interface {
    Run(context.Context, Request) (Stream, error)
}

type ModelOptions struct {
    Temperature *float64             `json:"temperature,omitempty"`
    MaxTokens   *int                 `json:"maxTokens,omitempty"`
    Reasoning   *model.ReasoningLevel `json:"reasoning,omitempty"`
}

type Request struct {
    Messages []model.Message `json:"messages"`
    Options  ModelOptions    `json:"options,omitempty"`
}

type RunInput struct {
    LLM          model.LLM
    SystemPrompt string
    Request      Request
}

type RunStrategy interface {
    Run(context.Context, RunInput) (Stream, error)
}

type SingleTurnStrategy struct{}

func NewSingleTurnStrategy() *SingleTurnStrategy
func (s *SingleTurnStrategy) Run(context.Context, RunInput) (Stream, error)

type Result struct {
    Output            model.Message    `json:"output"`
    GeneratedMessages []model.Message  `json:"generatedMessages"`
    Usage             model.Usage      `json:"usage"`
    StopReason        model.StopReason `json:"stopReason"`
    ModelID           string           `json:"modelId,omitempty"`
    ProviderID        string           `json:"providerId,omitempty"`
    ModelTurns        int              `json:"modelTurns"`
    ToolCalls         int              `json:"toolCalls"`
}

type EventType uint8

const (
    EventUnknown EventType = iota
    EventRunStart
    EventModel
    EventRunDone
)

func (t EventType) String() string

type Event struct {
    Type       EventType    `json:"type"`
    ModelTurn  int          `json:"modelTurn,omitempty"`
    ModelEvent *model.Event `json:"modelEvent,omitempty"`
    Result     *Result      `json:"result,omitempty"`
}

type Stream = iter.Seq2[Event, error]

func Complete(context.Context, Agent, Request) (*Result, error)

type Builder struct {
    // 字段不导出。
}

func NewBuilder() *Builder
func (b *Builder) UseLLM(model.LLM) error
func (b *Builder) UseRunStrategy(RunStrategy) error
func (b *Builder) SetSystemPrompt(string) error
func (b *Builder) SetModelOptions(ModelOptions) error
func (b *Builder) Build() (Agent, error)
```

稳定错误：

```go
var (
    ErrBuilderBuilt             = errors.New("agent: builder already built")
    ErrNilLLM                   = errors.New("agent: nil LLM")
    ErrLLMAlreadySet            = errors.New("agent: LLM already set")
    ErrMissingLLM               = errors.New("agent: missing LLM")
    ErrNilRunStrategy           = errors.New("agent: nil run strategy")
    ErrRunStrategyAlreadySet    = errors.New("agent: run strategy already set")
    ErrMissingRunStrategy       = errors.New("agent: missing run strategy")
    ErrConfigAlreadySet         = errors.New("agent: config already set")
    ErrInvalidOptions           = errors.New("agent: invalid model options")
    ErrInvalidRequest           = errors.New("agent: invalid request")
    ErrNilAgent                 = errors.New("agent: nil agent")
    ErrUnexpectedModelStreamEnd = errors.New("agent: model stream ended without done")
    ErrInvalidModelDoneEvent    = errors.New("agent: invalid model done event")
    ErrUnexpectedStreamEnd      = errors.New("agent: stream ended without done")
    ErrInvalidDoneEvent         = errors.New("agent: done event has no result")
)
```

### 5.1 可导出性边界

- `Agent`、`Request`、`RunInput`、`RunStrategy`、`SingleTurnStrategy`、`ModelOptions`、`Result`、`EventType`、`Event`、`Stream`、`Complete`、`Builder`、构建方法和稳定错误全部导出；
- `Agent` 接口只使用标准库、`model` 和 Agent 包已导出类型，仓库外实现无需包内特权；
- Builder 构建的具体 Agent、运行累计器、深拷贝函数和 Stream 转换逻辑保持非导出；
- 公开 `Event` 通过 `ModelEvent` 包装模型流，而不把 `model.Event` 类型别名成 Agent 事件；
- 未来增加 Tool 事件时追加 `EventType` 枚举和导出字段，不修改 `Agent.Run` 方法。

### 5.2 为何以 Stream 为主 API

- Agent 必须保留底层模型的增量输出和早停能力；
- Tool Loop 加入后，Agent 流还需表达模型轮次与工具执行进度，仅返回完整结果会过早限制协议；
- `Complete` 为不需要流的调用方提供与 `model.Complete` 类似的简单入口；
- `event.Publisher` 仍是旁路通知机制，不承担 Agent 主输出。

## 6. Builder 语义

### 6.1 组件与配置

1. `UseLLM` 和 `UseRunStrategy` 是必填组件入口；均拒绝 nil 和 typed nil；
2. 同一 Builder 重复调用组件方法分别返回 `ErrLLMAlreadySet` 和 `ErrRunStrategyAlreadySet`，不隐式覆盖；
3. Builder 不设置隐式单轮默认值，调用方使用 `NewSingleTurnStrategy` 显式选择单轮策略；
4. `SetSystemPrompt` 配置建造后 Agent 的固定应用级指令；
5. `SetModelOptions` 配置默认模型参数，对指针字段做值快照；
6. `SetSystemPrompt` 或 `SetModelOptions` 的重复调用返回 `ErrConfigAlreadySet`，错误文本指明冲突字段；
7. `Build` 缺少 LLM 或策略时分别返回 `ErrMissingLLM` 或 `ErrMissingRunStrategy`，Builder 仍允许继续配置；
8. 首次成功 `Build` 后冻结 Builder，所有变更方法和再次 `Build` 均返回 `ErrBuilderBuilt`；
9. Builder 仅用于单 goroutine 启动装配，不承诺并发安全；
10. Builder 不读取环境变量，不创建 Provider、Tool System、Bus 或全局单例。

### 6.2 ModelOptions 校验与合并

- `MaxTokens` 非 nil 时必须大于 0；
- `Temperature` 非 nil 时必须是有限数；Agent 层不引入特定 Provider 的数值范围；
- `Reasoning` 非 nil 时必须是 `model` 已定义的枚举值；
- Builder 默认值在 Build 时快照；
- Run 请求中的非 nil 字段逐项覆盖 Builder 默认值，nil 字段继承默认值；
- 合并后再校验一次，并构建新的 `model.Request`，不将 Builder 中的指针直接暴露给 LLM。

## 7. Request、历史与数据所有权

- `Request.Messages` 是调用方为本次 Run 提供的完整历史；首版 Agent 不加载、保存或隐式追加跨 Run Session；
- Request 至少包含一条 Message，空历史返回 `ErrInvalidRequest`；
- Builder 的 System Prompt 写入 `model.Context.SystemPrompt`，不作为普通 Message 追加；
- 本里程碑不向 `model.Context.Tools` 注入内容；
- Agent 深拷贝 Message/Content/ToolCall.Arguments/Signature 等引用数据后才调用 LLM；
- LLM 返回的 Event Block 与 Result 在向 Agent 调用方暴露前做快照，调用方修改事件不影响 Agent 终端结果；
- `Result.Output` 是终端模型 Message，`GeneratedMessages` 是本次 Run 新产生的可重放消息。单轮首版两者内容相同，Tool Loop 加入后 `GeneratedMessages` 将包含中间 Assistant 和 Tool Message。

## 8. SingleTurnStrategy 的 Agent Stream 语义

### 8.1 建流前错误

`Agent.Run` 直接返回的错误：

- nil/canceled Context；
- 空 Messages 或无效 ModelOptions；
- `model.LLM.Generate` 建流前错误；
- LLM 返回 nil Stream。

Context 已结束时优先返回 `ctx.Err()`。请求校验错误包装 `ErrInvalidRequest`，模型建流错误保留原错误链。

### 8.2 成功流顺序

单轮成功流：

```text
EventRunStart
    → EventModel(ModelTurn=1, ModelEvent=<model.EventStart/.../Done>)
    → EventRunDone(Result=<agent.Result>)
```

规则：

1. `EventRunStart` 仅产生一次；
2. 底层每个成功 `model.Event` 包装为 `EventModel`，`ModelTurn` 从 1 开始；
3. 底层 `model.EventDone` 必须带非 nil Result；
4. 根据模型 Result 构建 Agent Result，再产生唯一 `EventRunDone`；
5. 模型流静默结束或缺失有效 Done 时返回稳定协议错误，不产生 RunDone；
6. 底层流错误和 Context 错误由 Agent Stream 返回，错误后立即结束；
7. 调用方提前停止 Agent Stream 时，包装迭代立即退出底层 `model.Stream`，使 Provider generator 的资源释放 `defer` 能够执行；
8. 同一 Run 所有可变累计状态都位于 Stream generator 内，不保存到 Agent 实例。

### 8.3 Complete 语义

`Complete` 消费 Agent Stream：

- nil 或 typed-nil Agent 返回 `ErrNilAgent`；
- 返回第一个有效 `EventRunDone.Result`；
- RunDone 没有 Result 返回 `ErrInvalidDoneEvent`；
- 流在 RunDone 前结束返回 `ErrUnexpectedStreamEnd`；
- Stream 错误和 Context 错误保留错误链。

## 9. 结果与计数

单轮首版的 `Result` 映射：

| `model.Result` / 运行状态 | `agent.Result` |
|---|---|
| `Message` | `Output` |
| `[]Message{Message}` | `GeneratedMessages` |
| `Usage` | `Usage` |
| `StopReason` | `StopReason` |
| `ModelID` | `ModelID` |
| `ProviderID` | `ProviderID` |
| 成功模型轮次 | `ModelTurns = 1` |
| Output 中 `ContentToolCall` 数量 | `ToolCalls` |

未来 Tool Loop 追加模型轮次时，Usage 各字段做有界 `int64` 累加并检测溢出；本次仅有一轮，直接复制模型 Usage，不提前引入累加工具。

## 10. 并发、兼容性与安全

- 建造后 Agent 只保存 LLM、RunStrategy 引用、System Prompt 和已快照的默认参数；
- Agent 不保存 Context、Request、Stream、Result 或任何单次 Run 状态；
- 同一 Agent 可并发 Run，注入的 LLM 和 RunStrategy 也必须允许并发调用；
- 不记录 Prompt、Message、Thinking、Tool Arguments 或模型输出；
- `Agent` 是新增公开契约，后续不向接口追加方法；能力扩展通过 Request/Result/Event 增加可选字段或新的小接口完成；
- EventType 只在尾部追加新值，不重排已发布数值；
- Builder 可在后续追加 `UseTools`、运行限制和 Publisher 配置，不改变现有构建方法；
- 不为已删除的 AgentLoop、RunState、Runtime 增加类型别名或兼容壳。

## 11. 测试与验证计划

至少覆盖：

1. `Agent` 可由外部包实现，Builder 产物可作为公开 `Agent` 使用；
2. 缺少、nil/typed-nil、重复的 LLM 与 RunStrategy，以及构建后变更；
3. 首次 Build 失败后可继续配置，成功后 Builder 冻结；
4. System Prompt、默认 ModelOptions 快照与 Run 级逐字段覆盖；
5. 请求 Messages、ToolCall Raw JSON、Thinking Signature 和返回 Event/Result 的深拷贝；
6. RunStart、所有 Model Event 包装和 RunDone 顺序；
7. Output、GeneratedMessages、Usage、StopReason、Model/Provider ID、ModelTurns 和 ToolCalls 计数；
8. LLM 建流错误、流错误、nil 流、静默结束、无 Result Done 和 Context 取消；
9. Agent Stream 提前停止时底层 generator 执行资源释放；
10. `Complete` 的 nil Agent、成功、错误和协议失败；
11. 同一 Agent 并发 Run 时请求和结果相互隔离。

验证命令：

```bash
gofmt -w agent/*.go
go test ./agent
go test ./...
go vet ./...
go test -race ./agent
```

测试使用脚本化 fake `model.LLM`，不访问真实 Provider 或网络。

## 12. 验收标准

- 仓库外调用方可完整实现 `agent.Agent`；
- 调用方可通过 Builder 组装任意符合 `model.LLM` 和 `RunStrategy` 的实现；
- Builder 必填、typed nil、重复配置、参数快照和冻结语义有测试；
- 建造后 Agent 为不可变值，不依赖具体策略类型，单次 Run 状态完全隔离；
- 无工具单轮 Run 可流式产生 Agent 事件并返回完整 Agent Result；
- 错误、Context、流早停和底层资源释放语义明确且可测；
- 不修改现有 `model`、`tool`、`event` 和 Provider 行为；
- 新增 Go 代码格式化，Agent 包测试、`acore` 全量测试和 `go vet` 通过；竞态检测因环境不可用时必须如实说明。

## 13. 后续里程碑

本方案确认并实现后，下一个业务方案再定义 Tool Calling 闭环，包括：

1. Builder `UseTools(tool.Service)` 与 typed-nil/重复组件语义；
2. `tool.Spec` 到 `model.ToolSpec` 的快照转换；
3. `model.ToolCall` 到 `tool.Call` 以及 `tool.Result` 到 Tool Message 的转换；
4. 同轮多 Tool Call 串行顺序；
5. Tool 错误的 fail-fast/脱敏回馈策略；
6. 最大模型轮次、最大 Tool Call 数和稳定超限错误；
7. EventToolStart/EventToolDone 的 Agent Stream 字段和计数/Usage 累加。

运行策略抽象的详细职责、兼容性和测试语义见 `agent-run-strategy-refactor-design.md`。本里程碑不同时实现 Tool Loop、Session 或事件总线集成。
