# 标准运行事件定义模块设计方案

状态：待确认（本阶段仅设计，不实现、不接入发布流程）

## 1. 背景

当前仓库已有三类不同语义的事件契约：

- `acore/event`：通用事件总线，只负责按 Go 类型分发，不定义 Agent 领域字段。
- `acore/agent.Event`：Agent 的拉取式运行输出，服务于调用方消费运行结果。
- `acore/model.Event`：模型 Provider 的流式协议，描述增量文本、工具调用片段和结束信号。

这些契约分别解决分发、运行输出和 Provider 流协议问题，不能直接作为跨组件观测 Agent 运行生命周期的标准事件。因此新增一个独立的运行事件定义模块，仅描述稳定的领域事件和数据契约。

## 2. 目标与非目标

### 目标

1. 定义可由模块外实现、发布和订阅的标准运行事件。
2. 覆盖运行、模型轮次和工具调用三个层级的成功、失败、取消边界。
3. 统一事件名称、公共元数据、终态和顺序语义。
4. 默认不携带提示词、消息正文、思维内容、工具参数/结果和原始错误，降低敏感数据泄露风险。

### 非目标

- 本阶段不修改 `event.Bus`、`agent.Agent`、Builder 或 RunStrategy。
- 本阶段不决定 Publisher 的注入位置、RunID 生成方式、异步队列和背压策略。
- 本阶段不把每个模型增量 Delta 或消息对象转换为运行事件。
- 本阶段不定义持久化格式、跨进程传输协议或指标聚合实现。

## 3. 模块边界

新增公开包：`github.com/JIAOZAI1/acore/agent/runevent`。

依赖方向：

```text
runevent -> acore/event
runevent -> acore/model（复用 Usage、StopReason）
agent、Provider、tool、session ->（未来发布时）runevent
```

`runevent` 不依赖 `agent` 包本身，避免与现有 Agent 运行对象形成循环依赖；它只定义数据，不持有 Publisher，也不保存可变运行状态。

## 4. 公共契约

### 4.1 公共元数据与错误摘要

```go
type Metadata struct {
    RunID      string    `json:"runId"`
    Sequence   uint64    `json:"sequence"`
    OccurredAt time.Time `json:"occurredAt"`
}

type Failure struct {
    Stage     string `json:"stage"`
    Code      string `json:"code"`
    Retryable bool   `json:"retryable,omitempty"`
}
```

`RunID`、`Sequence` 和 `OccurredAt` 由发布方提供。事件定义模块不自动生成序号，也不隐式读取全局时钟或上下文。`Failure` 只允许稳定、脱敏的阶段和错误码，不承诺携带原始 `error` 文本。

### 4.2 工具调用状态

```go
type ToolCallStatus uint8

const (
    ToolCallStatusUnknown ToolCallStatus = iota
    ToolCallStatusSucceeded
    ToolCallStatusFailed
    ToolCallStatusCanceled
)
```

提供 `String() string`，未知数值返回 `"unknown"`。状态用于工具调用完成事件，不与 Go `error` 的具体类型绑定。

### 4.3 标准事件

所有事件均为值类型，使用值接收者实现 `event.Event` 的 `Name() string`；事件名称是稳定字符串常量。

| 类型 | 名称 | 关键字段 |
| --- | --- | --- |
| `RunStartedEvent` | `agent.run.started` | `Metadata`、`ModelID`、`ProviderID` |
| `ModelTurnStartedEvent` | `agent.model.turn.started` | `Metadata`、`Turn`、`ModelID`、`ProviderID` |
| `ModelTurnCompletedEvent` | `agent.model.turn.completed` | `Metadata`、`Turn`、`model.Usage`、`model.StopReason`、`DurationMS` |
| `ModelTurnFailedEvent` | `agent.model.turn.failed` | `Metadata`、`Turn`、`Failure` |
| `ToolCallStartedEvent` | `agent.tool.call.started` | `Metadata`、`Turn`、`CallID`、`ToolName` |
| `ToolCallCompletedEvent` | `agent.tool.call.completed` | `Metadata`、`Turn`、`CallID`、`ToolName`、`Status`、`ResultBytes`、`ErrorCode`、`DurationMS` |
| `RunCompletedEvent` | `agent.run.completed` | `Metadata`、累计 `model.Usage`、`StopReason`、计数摘要 |
| `RunFailedEvent` | `agent.run.failed` | `Metadata`、`Failure`、可选当前轮次和工具标识 |
| `RunCanceledEvent` | `agent.run.canceled` | `Metadata`、`Stage`、可选当前轮次和工具标识 |

建议的字段定义如下（实现时每个事件单独声明）：

```go
type RunStartedEvent struct { Metadata Metadata; ModelID, ProviderID string }
type ModelTurnStartedEvent struct { Metadata Metadata; Turn int; ModelID, ProviderID string }
type ModelTurnCompletedEvent struct {
    Metadata Metadata; Turn int; ModelID, ProviderID string
    Usage model.Usage; StopReason model.StopReason; DurationMS int64
}
type ModelTurnFailedEvent struct { Metadata Metadata; Turn int; Failure Failure }
type ToolCallStartedEvent struct { Metadata Metadata; Turn int; CallID, ToolName string }
type ToolCallCompletedEvent struct {
    Metadata Metadata; Turn int; CallID, ToolName string
    Status ToolCallStatus; ResultBytes int; ErrorCode string; DurationMS int64
}
type RunCompletedEvent struct {
    Metadata Metadata; Usage model.Usage; StopReason model.StopReason
    ModelTurns, ToolCalls, ToolErrors, GeneratedMessageCount int
}
type RunFailedEvent struct { Metadata Metadata; Failure Failure; ModelTurn int; CallID, ToolName string }
type RunCanceledEvent struct { Metadata Metadata; Stage string; ModelTurn int; CallID, ToolName string }
```

字段为摘要和标识，不包含原始内容。`ModelID`、`ProviderID`、`ToolName`、`CallID` 是否暴露由上层脱敏策略保证；不应放入凭证、租户密钥或完整请求参数。

## 5. 生命周期与顺序语义

典型成功流程：

```text
RunStartedEvent
  -> ModelTurnStartedEvent
  -> ModelTurnCompletedEvent
  -> ToolCallStartedEvent -> ToolCallCompletedEvent（可重复）
  -> ModelTurnStartedEvent -> ModelTurnCompletedEvent（可重复）
  -> RunCompletedEvent
```

失败或取消流程以 `RunStartedEvent` 开始，并以且仅以一个运行终态结束：`RunCompletedEvent`、`RunFailedEvent` 或 `RunCanceledEvent`。每个 `ModelTurnStartedEvent` 必须对应一个 `ModelTurnCompletedEvent` 或 `ModelTurnFailedEvent`；每个 `ToolCallStartedEvent` 必须对应一个 `ToolCallCompletedEvent`。

同一运行内 `Sequence` 必须严格递增，事件发布方负责保证顺序；事件总线不得重排。事件已经发布后不回滚，缺失或重复事件由消费方按序号和终态自行诊断。是否在入口参数校验前发布 `RunStartedEvent`，留待后续发布流程设计确定；当前 Agent 在校验通过后才进入运行策略。

## 6. 与现有事件契约的关系

- `model.Event` 继续作为 Provider 流协议。未来可将模型流的完成信号映射为 `ModelTurnCompletedEvent`，协议错误映射为 `ModelTurnFailedEvent`，但不把文本 Delta 逐条转换为运行事件。
- `agent.Event` 继续作为 Agent 拉取式输出。未来可将运行开始、工具开始/完成、运行完成等边界映射为对应标准事件；不改变其现有消费者契约。
- `event.Bus` 继续只负责通用类型分发，不增加运行事件专用分支或领域判断。订阅方通过 `event.HandlerFunc[runevent.RunCompletedEvent]` 等精确类型订阅。

## 7. 隐私、兼容性与扩展

事件结构采用显式字段和 JSON 标签，字段新增应保持向后兼容；删除或改变名称属于破坏性变更。未知事件名称和未知枚举值应允许消费者安全忽略。原始提示词、消息、思维链、图片、工具参数、工具结果、原始错误文本默认不进入标准事件；如确需审计，另行设计受控扩展事件和权限边界。

## 8. 后续实现拆分

本方案确认后，分两步实现：

1. 在 `acore/agent/runevent` 新增 `*Event` 类型、名称、枚举、注释和单元测试；验证所有类型实现 `event.Event`，名称唯一且 JSON 字段稳定。
2. 另行设计并实现发布集成：明确 Publisher 注入点、RunID/Sequence 生命周期、同步或异步错误策略、取消语义及脱敏策略。该步骤不应与事件定义混在同一个改动中。

验证计划：在 `acore` 模块执行 `gofmt`、`go test ./...`、`go vet ./...`；若加入并发发布实现，再执行 `go test -race ./...`。

## 9. 待确认事项

请确认以下边界后再进入代码实现：

1. 包路径采用 `acore/agent/runevent`，而不是扩展现有 `agent` 或 `event` 包。
2. 首期标准事件采用上述九类及稳定名称。
3. 事件只携带脱敏摘要，不携带原始内容和原始错误。
4. 首期只定义契约，不接入 Publisher、Bus 和 Agent 运行流程。
5. `model.Usage`、`model.StopReason` 作为完成事件的复用字段保持现有类型。
