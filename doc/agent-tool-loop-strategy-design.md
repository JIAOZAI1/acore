# Agent ToolLoopStrategy 设计方案

> 后续演进：`ToolLoopBuilder` 现已支持直接注入 `session.Service`，建造后的 `ToolLoopStrategy` 自行加载和提交会话历史，不存在 Session Runner 或策略外层包装器。详见 [Session 模块设计方案](session-module-design.md)。

## 1. 背景与结论

当前 `acore` 已具备：

- `model.LLM`、`model.ToolSpec`、`model.ToolCall` 和 `RoleTool` 消息协议；
- `tool.Service` 的工具目录与单次执行能力；
- 可替换的 `agent.RunStrategy` 和已实现的 `SingleTurnStrategy`；
- Agent 级 Stream、Result、Builder、输入快照和协议保护。

尚缺少把这些组件连接成闭环的多轮策略。本方案新增公开 `ToolLoopStrategy`：模型产生 Tool Call 后按顺序执行工具，把 Tool Result 转为可重放的 Tool Message，再发起下一轮模型生成，直到模型不再请求工具或触发明确限制。

核心取舍：

1. ToolLoop 是独立公开策略，不修改通用 Agent Builder 的策略选择逻辑；
2. `tool.Service`、循环限制和 Tool 错误模式由公开 `ToolLoopBuilder` 组装；
3. 通用 Agent Builder 仍只持有 LLM、RunStrategy 和共享模型配置；
4. 首版同一模型轮次中的多个 Tool Call 严格按内容顺序串行执行；
5. 默认把 Tool 错误转换为脱敏 Tool Message，让模型有机会修正；可显式选择 fail-fast；
6. 每轮模型事件、每次工具执行和最终结果都通过 Agent Stream 暴露；
7. 模型轮次、工具调用数和 Tool Result 大小都有硬限制，所有限制在副作用发生前尽可能校验。

## 2. 目标

1. 实现完整的 Model → Tool → Model 循环；
2. 复用现有 `model.LLM` 和 `tool.Service`，不复制工具注册或执行逻辑；
3. 保持模型和工具包互不依赖，由 Agent 策略显式转换 DTO；
4. 支持外部工具系统实现、typed nil 校验、目录快照和构建冻结；
5. 明确多 Tool Call 顺序、错误回馈、限制、事件、历史和 Usage 语义；
6. 调用方提前停止、Context 取消或任何错误路径都不会继续执行新的工具副作用；
7. 策略构建后不可变，同一策略实例可以并发用于多个 Agent Run。

## 3. 非目标

本次不实现：

- 同轮 Tool Call 并行执行；
- Tool Call 依赖图或事务回滚；
- 动态工具注册、运行期目录刷新或按请求筛选工具；
- Human Approval、Checkpoint、Resume 或长时间暂停；
- Tool Result 流、多模态 Tool Result 或二进制附件；
- 自动重试、超时、缓存、鉴权或日志；这些继续由 Tool Proxy 提供；
- 完整 JSON Schema 参数校验；继续由 Tool System 和具体 Tool 负责；
- MCP、远程进程、沙箱或容器执行；
- ReAct 专属类型；ReAct 首先作为 ToolLoop 的 Prompt/配置变体；
- 多 LLM、Planner、子 Agent 或策略路由。

## 4. 模块与依赖边界

计划调整：

```text
acore/agent/
├── agent.go                  # 追加 Tool Event、ToolErrors 和稳定错误
├── clone.go                  # Tool Event 深拷贝
├── tool_loop_builder.go      # ToolLoopBuilder、Limits、ErrorMode
├── tool_loop.go              # ToolLoopStrategy 与运行循环
├── tool_loop_convert.go      # tool/model DTO 显式转换
├── tool_loop_usage.go        # Usage 安全累加
├── tool_loop_builder_test.go
├── tool_loop_test.go
└── tool_loop_example_test.go
```

依赖方向：

```text
tool.Builder ──► tool.Service ─────────────┐
                                           │
ToolLoopBuilder ──► ToolLoopStrategy       │
                              │            │
                              ▼            ▼
Agent Builder ──► configuredAgent ──► RunStrategy.Run
                              │
                              ▼
                          model.LLM
```

约束：

- `agent` 可以依赖 `model` 和 `tool`；`model`、`tool` 不反向依赖 `agent`；
- ToolLoopBuilder 只持有已构建的 `tool.Service`，不注册具体 Tool；
- Agent Builder 不增加 `UseTools`，也不通过类型断言检查策略是否需要工具；
- ToolLoopStrategy 的专属依赖在策略构建时完成校验，避免到首个 Run 才发现缺少工具系统；
- `RunInput` 本次不增加 Tool 字段，保持共享运行输入只承载通用组件。

## 5. 公开 API

### 5.1 ToolLoopBuilder 与策略

```go
type ToolLoopLimits struct {
    MaxModelTurns      int `json:"maxModelTurns"`
    MaxToolCalls       int `json:"maxToolCalls"`
    MaxToolResultBytes int `json:"maxToolResultBytes"`
}

func DefaultToolLoopLimits() ToolLoopLimits

type ToolErrorMode uint8

const (
    ToolErrorModeFeedback ToolErrorMode = iota
    ToolErrorModeFailFast
)

func (m ToolErrorMode) String() string

type ToolLoopBuilder struct {
    // 字段不导出。
}

func NewToolLoopBuilder() *ToolLoopBuilder
func (b *ToolLoopBuilder) UseTools(tool.Service) error
func (b *ToolLoopBuilder) SetLimits(ToolLoopLimits) error
func (b *ToolLoopBuilder) SetToolErrorMode(ToolErrorMode) error
func (b *ToolLoopBuilder) Build() (*ToolLoopStrategy, error)

type ToolLoopStrategy struct {
    // 字段不导出。
}

func (s *ToolLoopStrategy) Run(context.Context, RunInput) (Stream, error)
```

默认限制：

```text
MaxModelTurns      = 8
MaxToolCalls       = 32
MaxToolResultBytes = 64 KiB
ToolErrorMode      = feedback
```

默认值由 `DefaultToolLoopLimits` 返回，不暴露可被修改的全局变量。调用 `SetLimits` 时三个字段都必须大于 0；未调用时使用上述默认值。

### 5.2 Agent Tool 事件

在现有 EventType 尾部追加，保持已发布枚举数值不变：

```go
const (
    EventUnknown EventType = iota
    EventRunStart
    EventModel
    EventRunDone
    EventToolStart
    EventToolDone
)

type ToolEvent struct {
    Call    tool.Call    `json:"call"`
    Result  *tool.Result `json:"result,omitempty"`
    IsError bool         `json:"isError,omitempty"`
}

type Event struct {
    Type       EventType    `json:"type"`
    ModelTurn  int          `json:"modelTurn,omitempty"`
    ModelEvent *model.Event `json:"modelEvent,omitempty"`
    Tool       *ToolEvent   `json:"tool,omitempty"`
    Result     *Result      `json:"result,omitempty"`
}
```

语义：

- `EventToolStart`：Tool 非 nil，包含 Call 快照，Result 为 nil，IsError 为 false；
- `EventToolDone` 成功：包含相同 Call 和 Result，IsError 为 false；
- `EventToolDone` 失败：Result 只包含可发送给模型的脱敏文本，IsError 为 true；
- Tool 事件的 `ModelTurn` 是产生该 Tool Call 的模型轮次；
- `tool.Call.Arguments` 在每个事件边界深拷贝；
- 事件不携带原始 Go error，避免 JSON 契约不稳定和错误详情泄漏；fail-fast 的原错误通过 Stream error 链返回给可信调用方。

### 5.3 Result 扩展

```go
type Result struct {
    // 现有字段保持不变。
    ToolErrors int `json:"toolErrors"`
}
```

`ToolCalls` 统计已接受并实际尝试执行的调用，包含成功和失败；`ToolErrors` 统计工具执行失败。因协议校验或预算检查在整批执行前失败的 Tool Call 不计入已执行计数，且不会产生 ToolStart。

### 5.4 稳定错误

```go
var (
    ErrToolLoopBuilderBuilt    = errors.New("agent: tool loop builder already built")
    ErrNilToolService          = errors.New("agent: nil tool service")
    ErrToolServiceAlreadySet   = errors.New("agent: tool service already set")
    ErrMissingToolService      = errors.New("agent: missing tool service")
    ErrInvalidToolCatalog      = errors.New("agent: invalid tool catalog")
    ErrInvalidToolLoopLimits   = errors.New("agent: invalid tool loop limits")
    ErrInvalidToolErrorMode    = errors.New("agent: invalid tool error mode")
    ErrInvalidModelResult      = errors.New("agent: invalid model result")
    ErrInvalidToolCall         = errors.New("agent: invalid model tool call")
    ErrModelTurnLimitExceeded  = errors.New("agent: model turn limit exceeded")
    ErrToolCallLimitExceeded   = errors.New("agent: tool call limit exceeded")
    ErrToolResultTooLarge      = errors.New("agent: tool result too large")
    ErrUsageOverflow           = errors.New("agent: usage overflow")
)
```

错误使用 `%w` 包装调用名称、轮次或限制等非敏感定位信息；不把 Tool Arguments、Tool Result、Prompt 或原始模型内容写入错误文本。

## 6. ToolLoopBuilder 语义

### 6.1 组件与冻结

1. `UseTools` 必填，拒绝 nil 和 typed nil；
2. 第二次调用 `UseTools` 返回 `ErrToolServiceAlreadySet`；
3. `SetLimits` 和 `SetToolErrorMode` 是可选单值配置，重复调用返回现有 `ErrConfigAlreadySet` 并说明字段；
4. Build 缺少 Tool Service 返回 `ErrMissingToolService`，Builder 不冻结；
5. Build 校验并快照工具目录，成功后冻结；
6. 成功 Build 后所有方法及再次 Build 返回 `ErrToolLoopBuilderBuilt`；
7. ToolLoopBuilder 只用于单 goroutine 启动装配，不承诺并发安全；
8. 建造后的 ToolLoopStrategy 不修改目录、限制或错误模式。

`ToolLoopStrategy.Run` 直接被调用时也会校验 nil receiver、Context、RunInput.LLM、Messages、ModelOptions 以及内部 Tool Service；不依赖“只能由 Agent Builder 调用”的隐含前提。未经 ToolLoopBuilder 构建的零值策略返回 `ErrMissingToolService`。

### 6.2 工具目录快照

Build 调用一次 `tool.Service.Specs()`，按返回顺序转换并保存为 `[]model.ToolSpec`。必须校验：

- Name 非空；
- Name 不重复；
- Parameters 是合法 JSON 且顶层是对象；
- Description 和 Parameters 做值快照。

空目录允许成功构建；此时策略行为等同于“带空工具列表的循环”，通常会在一轮模型生成后结束。外部 `tool.Service` 在 Build 后不得改变执行目录语义；内置 `tool.System` 已是不可变实现。

转换始终发生在 Agent 边界：

```go
model.ToolSpec{
    Name:        spec.Name,
    Description: spec.Description,
    Parameters:  clonedParameters,
}
```

## 7. 运行状态与数据流

每次 Run 创建私有状态：

```text
workingMessages    输入完整历史 + 本次新生成消息
generatedMessages  仅本次产生的 Assistant/Tool 消息
usage              所有成功模型轮次的累计 Usage
modelTurns         已完成的模型轮次
toolCalls          已尝试的工具调用数
toolErrors         工具失败数
seenToolCallIDs    本次 Run 已见调用 ID 集合
lastModelResult    最终模型结果
```

这些状态全部位于 Stream generator 内，不保存到 ToolLoopStrategy 实例。

完整流程：

```text
RunInput
   │
   ├── 首轮 model.Request(Messages + Tools)
   ▼
Model Stream ──► Assistant Message
                       │
              ┌────────┴────────┐
              │ 无 Tool Call    │ 有 Tool Call
              ▼                 ▼
          RunDone         validate batch/limits
                                    │
                              ToolStart
                                    │
                           tool.Service.Execute
                                    │
                               ToolDone
                                    │
                            append Tool Message
                                    │
                                    └──► 下一轮 Model
```

## 8. 模型轮次

### 8.1 请求构造

每轮 `model.Request` 包含：

- Builder System Prompt；
- 当前 `workingMessages` 的深拷贝；
- ToolLoopBuilder 在 Build 时快照的全部 `model.ToolSpec`；
- RunInput 中已经合并完成的 Temperature、MaxTokens 和 Reasoning。

工具目录每轮保持相同顺序，不调用 Service.Specs 动态刷新。

### 8.2 首轮与后续建流错误

- `ToolLoopStrategy.Run` 在返回 Agent Stream 前建立第一轮 `model.Stream`；因此第一轮 LLM 建流错误直接由 Run 返回；
- 后续轮次在 Agent Stream generator 内建立；建流错误通过 Stream error 返回；
- nil model.Stream、缺失 Done 或 Done 没有 Result 使用现有模型协议错误；
- Context 已结束时始终优先返回 `ctx.Err()`；
- 调用方在 RunStart 处早停时，必须取消并启动底层 generator 的释放路径，与 SingleTurnStrategy 保持一致。

### 8.3 模型事件

底层每个成功 `model.Event` 都包装为：

```go
Event{
    Type:       EventModel,
    ModelTurn:  currentTurn,
    ModelEvent: clonedModelEvent,
}
```

模型轮次从 1 开始。每轮 Done Event 仍向调用方暴露；随后策略根据 Done Result 决定结束或执行工具。

## 9. Tool Call 提取与校验

Model Done Result 的 Message 必须是 `RoleAssistant`，否则返回 `ErrInvalidModelResult`。Tool Call 从 Assistant Message 的 Content 中按索引顺序提取。每批调用在任何工具执行前整体校验：

1. `ContentToolCall` 必须带非 nil ToolCall；
2. ID 非空；
3. Name 非空；
4. Arguments 必须是合法 JSON 对象；
5. ID 在整个 Run 内唯一；
6. 本批数量不能使累计 ToolCalls 超过 MaxToolCalls；
7. 当前轮次之后必须还有下一模型轮次预算。

任何一项失败时：

- 返回稳定协议或限制错误；
- 本批一个工具都不执行；
- 不产生 ToolStart/ToolDone；
- 不产生 RunDone。

限制检查优先级：如果当前模型轮次已经等于 MaxModelTurns 且仍请求工具，先返回 `ErrModelTurnLimitExceeded`，避免执行无法被后续模型消费的副作用；然后检查 Tool Call 总预算。

Content 中存在 Tool Call 时以实际 Content 为循环依据，即使 StopReason 不是 `ReasonToolUse` 也继续执行。若 StopReason 是 `ReasonToolUse` 但 Content 中没有 Tool Call，返回 `ErrInvalidToolCall`，因为无法构造下一步。

## 10. 工具执行

### 10.1 串行顺序

同轮多个 Tool Call 严格按 Assistant Message 中出现顺序串行执行：

```text
ToolStart(call-1) → Execute(call-1) → ToolDone(call-1)
ToolStart(call-2) → Execute(call-2) → ToolDone(call-2)
```

首版不并行，原因是：

- Tool 可能有副作用或顺序依赖；
- 串行事件、取消和错误语义更确定；
- 无需引入 goroutine、汇合、并发限制和部分成功排序；
- 后续若增加并行模式，必须单独设计显式配置，不能静默改变默认顺序。

### 10.2 调用转换

```go
tool.Call{
    ID:        modelCall.ID,
    Name:      modelCall.Name,
    Arguments: clonedArguments,
}
```

策略先产生 ToolStart；只有调用方继续消费 Stream 才执行工具。Execute 成功后检查 `len(result.Content)` 不超过 MaxToolResultBytes，再产生 ToolDone 和 Tool Message。

成功 Tool Message：

```go
model.Message{
    Role:       model.RoleTool,
    ToolCallID: call.ID,
    Content: []model.ContentBlock{{
        Kind: model.ContentText,
        Text: result.Content,
    }},
}
```

每个 Tool Message 紧跟其对应调用，追加到 workingMessages 和 generatedMessages。

### 10.3 Tool Result 大小

按 UTF-8 字符串的字节长度 `len(Content)` 检查。超限时：

- 不截断内容，避免生成不可验证或语义错误的部分结果；
- 产生脱敏的 ToolDone 失败事件；
- ToolErrors 加一，ToolDone 的固定安全文本为 `tool result too large`；
- 返回 `ErrToolResultTooLarge` Stream error；
- 不把原始 Tool Result 加入消息或错误文本；
- 不继续执行本批后续工具。

## 11. Tool 错误策略

### 11.1 Feedback（默认）

除 Context 取消外，Tool Service 返回的错误转换为 `RoleTool`、`IsError=true` 的消息，模型可以在下一轮改正参数、选择其他工具或给出最终答复。

安全文本固定分类：

| 原错误 | 发给模型的文本 |
|---|---|
| `tool.ErrToolNotFound` | `tool not found` |
| `tool.ErrInvalidArguments` | `invalid tool arguments` |
| 其他错误 | `tool execution failed` |

不包含原始错误、参数、内部路径、堆栈或凭证。失败 Tool Message：

```go
model.Message{
    Role:       model.RoleTool,
    ToolCallID: call.ID,
    IsError:    true,
    Content: []model.ContentBlock{{
        Kind: model.ContentText,
        Text: safeMessage,
    }},
}
```

ToolErrors 加一，产生 ToolDone(IsError=true)，然后继续同批剩余调用和下一模型轮次。实际错误的日志、重试、Tracing 和告警由 Tool Proxy 负责。

### 11.2 FailFast

ToolErrors 加一并产生脱敏 ToolDone 后，立即通过 Stream error 返回包装后的原错误，不追加失败 Tool Message，不执行同批剩余调用，也不产生 RunDone。

错误文本只附加 Tool Name 和 Call ID，不包含 Arguments。`errors.Is/As` 保留 Tool Service 原错误链。

### 11.3 Context 错误

Execute 返回后，无论得到 Result 还是 error，都先检查 `ctx.Err()`。Context 错误优先：

- 直接返回 `ctx.Err()`；
- 不产生虚假的 ToolDone 或 Tool Message；
- 不继续本批或下一轮。

## 12. Agent Stream 顺序

一次包含两次工具调用的成功运行：

```text
EventRunStart
  → EventModel(turn=1, Start/.../Done with assistant tool calls)
  → EventToolStart(turn=1, call-1)
  → EventToolDone(turn=1, call-1)
  → EventToolStart(turn=1, call-2)
  → EventToolDone(turn=1, call-2)
  → EventModel(turn=2, Start/.../Done with final answer)
  → EventRunDone
```

规则：

- RunStart 只产生一次，不随模型轮次重复；
- ToolLoopStrategy 负责完整 Agent Stream，通用 configuredAgent 继续执行协议保护和输出快照；
- 任何 Stream error 后立即终止，不产生 RunDone；
- RunDone 是唯一成功终点；
- 调用方早停时，当前 generator 返回，未开始的后续模型和工具绝不执行；
- ToolStart 后早停发生在 Execute 前，因此不会产生工具副作用；
- ToolDone 已产生意味着该工具调用已完成。

## 13. Usage、计数与最终结果

每轮成功模型 Done 后累计 Usage 的所有字段：

- InputTokens；
- OutputTokens；
- CacheRead；
- CacheWrite；
- ReasoningTokens；
- TotalTokens。

每个字段使用安全 `int64` 加法；发生正向或负向溢出时返回 `ErrUsageOverflow`，不产生 RunDone。首版不自行重新计算 TotalTokens，分别累计 Provider 报告的每个字段。

最终 Result：

| 字段 | ToolLoopStrategy 语义 |
|---|---|
| Output | 最后一轮、无 Tool Call 的 Assistant Message |
| GeneratedMessages | 本次 Run 产生的所有 Assistant 和 Tool Message，保持执行顺序 |
| Usage | 所有成功模型轮次 Usage 累加 |
| StopReason | 最后一轮模型 StopReason |
| ModelID | 最后一轮模型 ModelID |
| ProviderID | 最后一轮模型 ProviderID/响应标识 |
| ModelTurns | 成功完成的模型轮次数 |
| ToolCalls | 实际尝试执行的工具调用数 |
| ToolErrors | 工具执行失败数 |

中间模型 Result 仍可通过对应 EventModel(Done) 观察，但不会覆盖最终 Result 的终止元数据。

## 14. Context、早停与资源

- ToolLoopStrategy 不保存 Context 到结构体；
- 第一轮和每个后续模型请求都使用本次 Run 的派生 Context；
- 每个 model.Stream 的 generator 都有明确退出并触发 Provider defer；
- `tool.Service.Execute` 使用同一 Run Context，必须响应取消；
- 策略不自行创建后台 goroutine；
- 调用方早停、Context 取消、限制错误、模型错误和工具 fail-fast 都立即阻止后续副作用；
- Tool Service 已开始执行后是否能立即停止取决于具体 Tool 是否遵守 Context 契约；策略不会在取消后假定工具已回滚。

## 15. 数据所有权与安全

- ToolLoopBuilder 在 Build 时快照 Tool Specs 和 JSON Schema；
- 每轮 model.Request 深拷贝工作消息、Tool Arguments、Signature 和 Tool Specs；
- Tool Event、Tool Message、GeneratedMessages 和最终 Result 相互隔离；
- ToolLoopStrategy 只保存 Tool Service 引用、目录快照、Limits 和 ErrorMode；
- Tool Arguments、Tool Result、Prompt、Thinking 和模型内容不进入日志或错误文本；
- Feedback 模式只向模型发送固定脱敏错误；
- 策略不捕获、记录或持久化凭证；
- 外部 Tool Service 和具体 Tool 仍必须自行处理授权、参数业务校验和敏感输出。

## 16. 兼容性与组装示例

现有 SingleTurn 组装不变：

```go
builder := agent.NewBuilder()
_ = builder.UseLLM(llm)
_ = builder.UseRunStrategy(agent.NewSingleTurnStrategy())
singleTurnAgent, err := builder.Build()
```

ToolLoop 显式构建并注入策略：

```go
toolLoopBuilder := agent.NewToolLoopBuilder()
_ = toolLoopBuilder.UseTools(toolService)
_ = toolLoopBuilder.SetLimits(agent.ToolLoopLimits{
    MaxModelTurns:      8,
    MaxToolCalls:       32,
    MaxToolResultBytes: 64 * 1024,
})
toolLoop, err := toolLoopBuilder.Build()
if err != nil {
    return err
}

agentBuilder := agent.NewBuilder()
_ = agentBuilder.UseLLM(llm)
_ = agentBuilder.UseRunStrategy(toolLoop)
value, err := agentBuilder.Build()
```

兼容性：

- `Agent.Run`、Request、RunInput、RunStrategy 和 Complete 不变；
- EventType 只在尾部追加；
- Event 和 Result 只增加可选/零值字段；
- SingleTurnStrategy 的 ToolErrors 为 0，Tool 字段为 nil；
- 通用 Agent Builder 不增加策略专属方法；
- ToolLoopStrategy 是新增公开组件，不为旧的 looper/runtime 类型提供兼容壳。

## 17. 测试计划

### 17.1 Builder 与目录

1. 缺少、nil/typed-nil、重复 Tool Service；
2. 默认 Limits、无效 Limits、ErrorMode 和重复配置；
3. 空目录、顺序、深拷贝、空名、重复名和非法 Schema；
4. 首次 Build 失败后可继续配置，成功后冻结；
5. 模块外可构造 ToolLoopStrategy 并作为 RunStrategy 注入。

### 17.2 正常流程

1. 无 Tool Call 一轮结束；
2. 单 Tool Call 后第二轮结束；
3. 同轮多个 Tool Call 串行顺序；
4. 多轮 Tool Call；
5. Tool Spec 到 model.ToolSpec 转换和每轮稳定目录；
6. Assistant/Tool working history 和 GeneratedMessages 顺序；
7. Event ModelTurn、ToolStart/Done 和唯一 RunDone；
8. 最终 Output、StopReason、Model/Provider ID 和各项计数。

### 17.3 错误与限制

1. 非 Assistant Model Result、无效 Tool Call、重复 ID 和 ToolUse 无调用；
2. 批量 Tool Call 超限时零副作用；
3. 最后模型轮次仍请求工具时零副作用；
4. Tool Result 超限；
5. Feedback 的三类脱敏文本和继续循环；
6. FailFast 的 ToolDone、错误链和剩余调用不执行；
7. Context 错误优先；
8. 首轮/后续模型建流、流错误、nil Stream、静默结束和无 Result Done；
9. Usage 每字段累加及正负溢出。

### 17.4 所有权、早停与并发

1. Tool Schema、Arguments、Signature、Event、Tool Result 和最终 Result 深拷贝；
2. RunStart、模型事件和 ToolStart 处早停；
3. 早停后 Provider defer 执行且未开始工具不执行；
4. 同一 Agent + ToolLoopStrategy 并发 Run 状态隔离；
5. fake LLM 和 fake Tool Service 的确定性测试，不访问网络。

验证命令：

```bash
gofmt -w agent/*.go
go test ./agent
go test ./...
go vet ./...
go test -race ./agent
```

## 18. 验收标准

- ToolLoopStrategy 通过公开 Builder 构造并实现 RunStrategy；
- Agent Builder 不持有 Tool Service，也不识别 ToolLoopStrategy 具体类型；
- Model、Tool、Message 和 Event 转换边界明确且可重放；
- 多 Tool Call 默认串行，顺序稳定；
- Feedback 不向模型泄漏原始 Tool 错误；
- 所有限制和整批校验在工具副作用前尽可能生效；
- Usage、GeneratedMessages、ToolCalls、ToolErrors 和终止元数据准确；
- Context、早停和错误路径不继续发起新模型请求或工具调用；
- 新公开类型和方法都有 Go 文档及模块外示例；
- Agent 包、`acore` 全量测试和 `go vet` 通过；race 因环境不可用时如实说明。

本文档已确认并实现。`acore/agent` 已提供公开 `ToolLoopBuilder`、`ToolLoopStrategy`、Tool 事件、运行限制和错误模式；本次未同时实现其他路线图策略。
