# 上下文窗口模块设计方案

> 实施状态（2026-08-26）：方案已按本文边界实现。`acore/contextwindow` 已提供 Reducer、Estimator、Apply 和 TailReducer；SingleTurn/ToolLoop 可通过各自 Builder 注入窗口治理，并在每次模型调用前应用。

## 1. 背景与现状

方案设计时的 `acore` 基线已具备以下相关能力：

- `model.Model` 已声明 `ContextWindow` 和 `MaxOutputTokens`，但尚未定义窗口治理行为；
- `model.Context` 表示一次模型调用的完整输入，包含 System Prompt、Messages 和 Tool Specs；
- `prompt.Renderer` 在每次 Agent Run 开始时生成一次 System Prompt；
- `session.Service` 加载和保存完整、可重放的跨 Run 消息历史；
- `SingleTurnStrategy` 在加载 Session 后发起一次模型调用；
- `ToolLoopStrategy` 持有 Tool Specs，并在每轮工具执行后继续扩展 `workingMessages`、再次调用模型；
- `ModelOptions.MaxTokens` 表示本次模型调用请求的最大输出 Token 数。

方案实施前，两个内置策略都会把完整消息历史直接放入 `model.Request.Context.Messages`。当 Session 历史、工具结果、System Prompt 或 Tool Specs 持续增长时，请求可能超过模型上下文窗口。Provider 最终只能返回供应商错误，框架无法在调用前进行一致、可替换的治理。

上下文窗口不是 Session 的存储职责，也不是 Prompt 的渲染职责：

- Session 应保存完整历史，保证会话可审计、可重放，并避免把某个模型的窗口大小固化到持久化数据；
- Prompt 只生成 System Prompt，不应同时加载历史、估算 Token 或裁剪消息；
- Provider 负责协议转换和远程调用，不应替 Agent 决定丢弃哪段业务历史；
- 运行策略掌握每一次真实模型调用的 System Prompt、Messages、Tools 和输出预算，是执行窗口治理的正确位置。

## 2. 本阶段目标

本阶段新增一个最小、公开、可替换的上下文窗口模块，目标如下：

1. 在 `acore` 新增公开包 `github.com/JIAOZAI1/acore/contextwindow`；
2. 通过公开 `Reducer` 契约，在每次模型调用前选择需要保留的消息后缀；
3. 通过公开 `Estimator` 契约解耦模型/Provider 相关的 Token 估算；
4. 提供一个只删除完整旧对话轮次的 `TailReducer`；
5. 统一预算 System Prompt、Messages、Tool Specs、请求输出预留和安全余量；
6. 保证当前 Run 的输入及本 Run 已产生的 Assistant/Tool 消息不会被裁剪；
7. SingleTurn 和 ToolLoop 均可通过各自 Builder 显式注入 Reducer；
8. 不配置 Reducer 时完全保持现有行为；
9. Session 始终保存完整历史，裁剪结果只属于某一次模型请求；
10. 模块外调用方可以实现、组装和替换 Reducer/Estimator，不依赖 `internal` 包或全局状态。

## 3. 非目标

本阶段不包含：

- 修改、压缩或删除 Session 中已经保存的消息；
- 自动摘要、LLM 驱动的历史压缩或摘要结果持久化；
- 检索增强、长期记忆、用户资料或其他 Context Contributor；
- 修改、截断或降级 System Prompt、Tool Specs、图片、Tool Arguments、Tool Result 的单个内容块；
- 在裁剪失败后自动降低 `MaxTokens`；
- 捕获 Provider 的“上下文超限”错误后自动重试；
- 通用且声称精确的内置 Tokenizer；
- Provider 专属 Tokenizer 依赖、远程 Token 计数 API 或模型目录在线发现；
- 将窗口估算值、裁剪数量或完整上下文加入 Agent Result、事件或默认日志；
- 新增名为 `context` 的 Go 包，避免与标准库 `context` 混淆；
- 改变外部自定义 `RunStrategy` 的接口；
- 为了窗口治理把 Session、Prompt、Tool 或 LLM 注入 Agent Builder 并形成服务定位器。

LLM 摘要会产生额外模型调用、Usage、失败和递归窗口问题，还涉及摘要是否写回 Session 的一致性语义。本阶段先提供可验证的“完整前缀删除”能力；摘要压缩在出现真实需求后以独立 Compactor 方案设计。

## 4. 核心设计决策

### 4.1 使用独立 `contextwindow` 包

包依赖关系如下：

```text
contextwindow ──► model
agent         ──► contextwindow
agent         ──► model / prompt / session / tool

contextwindow -X-> agent / session / prompt / tool / provider
```

`contextwindow` 只依赖 Provider 无关的 `model.Model` 和 `model.Context`。它不读取 Session、不渲染 Prompt、不发现工具，也不调用主 Agent 使用的 LLM。

### 4.2 Reducer 只返回“从哪里开始保留”

首版 Reducer 不返回一组可任意改写的消息，而只返回原消息切片的起始索引：

```text
原消息： [可删除旧前缀........................][受保护后缀]
结果：                         MessageStart ──► [保留消息......]
```

这样可以由框架强制保证：

- 只删除完整前缀，不重排、不改写、不伪造消息；
- System Prompt 和 Tool Specs 不会被组件偷偷修改；
- 当前 Run 的受保护消息保持内容、顺序和数据所有权不变；
- 裁剪视图不会被错误提交回 Session；
- 外部 Reducer 的错误结果可以被统一校验。

如果未来需要摘要替换，新增独立 Compactor 契约，不扩大首版 Reducer 的权限。

### 4.3 由具体策略 Builder 注入

Reducer 分别注入 `SingleTurnStrategy` 和 `ToolLoopStrategy`，不注入 Agent Builder：

- Agent 外层不知道 ToolLoop 的 Tool Specs；
- ToolLoop 第二轮及后续轮次只存在于策略内部；
- 不同外部 RunStrategy 可能有完全不同的消息和模型调用方式；
- 策略级注入与当前 Session Service 的归属方式一致，依赖保持显式。

### 4.4 Session 保留完整历史

窗口裁剪只作用于马上发送给 Provider 的 `model.Request` 副本：

```text
Session Snapshot + 本次输入 + 本次生成消息
                   │
                   ├──► 完整 workingMessages ──► 成功后提交 Session
                   │
                   └──► Context Window Reducer ──► 临时消息后缀 ──► LLM
```

Reducer 的结果不覆盖 `workingMessages`，因此 ToolLoop 下一轮仍从完整运行历史重新计算窗口，Session 提交内容也不受影响。

## 5. 公开契约

### 5.1 Reducer 输入与结果

计划在 `acore/contextwindow/window.go` 定义：

```go
package contextwindow

import (
    "context"

    "github.com/JIAOZAI1/acore/model"
)

// Input is one immutable snapshot used to fit a model context.
// Code constructing Input should use keyed fields.
type Input struct {
    Model                 model.Model
    Context               model.Context
    RequestedOutputTokens int64
    ProtectedMessages     int
}

// Result selects the suffix Context.Messages[MessageStart:].
type Result struct {
    MessageStart int
}

// Reducer selects a context message suffix for one model request.
// Implementations must support concurrent calls or synchronize their state.
type Reducer interface {
    Reduce(context.Context, Input) (Result, error)
}

// ReducerFunc adapts a function to Reducer.
type ReducerFunc func(context.Context, Input) (Result, error)

func (f ReducerFunc) Reduce(ctx context.Context, input Input) (Result, error)

// Apply invokes reducer, validates its result, and returns an isolated context.
func Apply(context.Context, Reducer, Input) (model.Context, error)
```

字段语义：

- `Model`：当前 `LLM.Model()` 的快照，提供模型和 Provider 标识以及窗口元数据；
- `Context`：本次模型调用裁剪前的完整 System Prompt、Messages 和 Tool Specs 深拷贝；
- `RequestedOutputTokens`：当前 `ModelOptions.MaxTokens` 的值；未显式设置时为 0；
- `ProtectedMessages`：`Context.Messages` 末尾必须原样保留的消息数量；
- `MessageStart`：保留后缀的首个索引，0 表示完整保留。

Reducer 必须满足：

```text
0 <= MessageStart <= len(Messages) - ProtectedMessages
```

当 `MessageStart > 0` 时，保留后缀必须从 `RoleUser` 开始。框架会校验范围和安全边界，不信任外部 Reducer 返回值。

`ProtectedMessages` 在 Agent 的正常调用路径中始终大于 0。Reducer 直接调用时收到空消息、无效保护数量或无效模型预算，应返回稳定输入错误。

调用方应优先使用 `Apply`，而不是直接调用 `Reducer.Reduce`。`Apply` 统一负责：

- 校验 nil/typed nil Reducer、Context 和通用 Input 约束；
- 对传给 Reducer 的 Model/Context 建立深拷贝；
- 在 Reducer 返回后优先处理 Context 取消；
- 校验 MessageStart 未进入保护后缀，且非零切点从 User 消息开始；
- 只从原 Input 构造最终消息后缀，忽略 Reducer 对其输入副本的任何修改；
- 返回保留原 System Prompt 和 Tool Specs 的完整 `model.Context` 深拷贝。

这样模块外自定义 RunStrategy 也能复用与内置策略相同的安全应用逻辑，不必重复实现索引校验和数据所有权边界。

### 5.2 Token Estimator

计划在同一包定义：

```go
// Estimator estimates all input tokens represented by value for selected.
type Estimator interface {
    Estimate(context.Context, model.Model, model.Context) (int64, error)
}

// EstimatorFunc adapts a function to Estimator.
type EstimatorFunc func(context.Context, model.Model, model.Context) (int64, error)

func (f EstimatorFunc) Estimate(
    ctx context.Context,
    selected model.Model,
    value model.Context,
) (int64, error)
```

Estimator 必须计算整个输入，而不只是消息文本：

- System Prompt；
- Message 的角色、文本、Thinking、签名和协议开销；
- 图片等多模态内容的模型计费规则；
- Tool Call ID、名称、Arguments 及消息结构开销；
- Tool Specs 的名称、描述、JSON Schema 及 Provider 序列化开销。

Estimator 返回负数属于无效结果。实现应根据 `Model.Provider`、`Model.API` 和 `Model.ID` 选择正确规则，并且必须支持并发调用。

框架不提供一个伪装成精确 Tokenizer 的通用实现。原因是文本编码、多模态计费和工具协议开销均随 Provider、API 和模型变化。首版提供接口和函数适配器；应用可以显式注入自己的精确或保守估算器。估算误差由 Estimator 实现负责，`TailReducer` 的安全余量用于吸收已知误差，但不能把近似估算变成硬保证。

### 5.3 TailReducer

计划在 `acore/contextwindow/tail.go` 提供：

```go
type TailConfig struct {
    Estimator            Estimator
    SafetyMarginTokens   int64
    FallbackOutputTokens int64
}

func NewTailReducer(TailConfig) (*TailReducer, error)
```

配置语义：

- `Estimator` 必填，拒绝 nil 和 typed nil；
- `SafetyMarginTokens` 必须大于等于 0，默认 0，不擅自选择模型无关常量；
- `FallbackOutputTokens` 必须大于等于 0；只有请求未设置 `MaxTokens` 且 `Model.MaxOutputTokens` 也未知时才使用；
- 三处都未提供输出预留时返回预算不可用错误，不使用不安全的隐式值。

`TailReducer` 构建后配置不可变；只要注入的 Estimator 可并发使用，同一 Reducer 就可以安全服务多个并发 Run。

### 5.4 稳定错误

计划提供以下包级稳定错误：

```go
var (
    ErrInvalidContext       = errors.New("contextwindow: invalid context")
    ErrNilReducer           = errors.New("contextwindow: nil reducer")
    ErrNilEstimator         = errors.New("contextwindow: nil estimator")
    ErrInvalidConfig        = errors.New("contextwindow: invalid config")
    ErrInvalidInput         = errors.New("contextwindow: invalid input")
    ErrBudgetUnavailable    = errors.New("contextwindow: budget unavailable")
    ErrEstimate             = errors.New("contextwindow: estimate input")
    ErrInvalidEstimate      = errors.New("contextwindow: invalid estimate")
    ErrCannotFit            = errors.New("contextwindow: context cannot fit")
    ErrInvalidResult        = errors.New("contextwindow: invalid reducer result")
)
```

错误使用 `%w` 保留错误链。错误文本可以包含模型 ID、消息索引、预算和估算 Token 数等低敏感元数据，不包含 Prompt、消息、工具参数、工具结果、图片数据或 JSON Schema 原文。

## 6. Token 预算语义

### 6.1 模型字段

实现时补充 `model.Model` 字段注释，明确：

- `ContextWindow`：模型一次生成允许的“输入 Token + 请求最大输出 Token”总量；0 表示未知；
- `MaxOutputTokens`：模型允许的最大输出 Token 数；0 表示未知。

不改变字段类型和 JSON 名称，也不对未启用 Reducer 的现有模型配置新增强制校验。

### 6.2 输出预留优先级

`TailReducer` 按以下顺序确定输出预留：

1. `Input.RequestedOutputTokens > 0`；
2. `Input.Model.MaxOutputTokens > 0`；
3. `TailConfig.FallbackOutputTokens > 0`；
4. 否则返回 `ErrBudgetUnavailable`。

`RequestedOutputTokens` 来自已经完成默认值合并的 `ModelOptions.MaxTokens`，因此 Run 级覆盖优先于 Agent 默认配置。

上下文窗口组件不负责判断请求输出是否超过 Provider 的模型输出上限；这仍属于模型/Provider 请求校验。组件只按实际请求值预留空间。

### 6.3 输入预算

使用 `int64` 做安全运算：

```text
InputBudget = Model.ContextWindow
            - ReservedOutputTokens
            - SafetyMarginTokens
```

以下情况直接失败：

- `ContextWindow <= 0`；
- 输出预留无法确定；
- 输出预留或安全余量为负数；
- 输出预留与安全余量之和不小于 ContextWindow；
- Estimator 返回负数；
- 固定内容与受保护消息仍超过 InputBudget。

Token 预算覆盖完整 `model.Context`。不能先给 Messages 分配窗口、再忽略 System Prompt 或 Tool Specs，否则 ToolLoop 的实际请求仍可能超限。

## 7. TailReducer 算法

### 7.1 裁剪单位

TailReducer 只允许在 `RoleUser` 消息前切断历史。这会按“用户消息发起的一轮对话”删除完整旧轮次，避免出现以下无效开头：

- 孤立的 Tool Result；
- 没有对应 Assistant Tool Call 的 Tool 消息；
- 从一次 Assistant/Tool 交互的中间开始；
- 删除当前 User 消息但保留其后 Assistant/Tool 状态。

如果现有消息序列没有可用的 User 边界，TailReducer 不猜测 Provider 是否允许从 Assistant 或 Tool 开始；无法完整保留时返回 `ErrCannotFit`。

### 7.2 处理步骤

1. 校验 Context、Input、模型窗口、保护数量和预算；
2. 对完整 Context 调用 Estimator；
3. 若已不超过 InputBudget，返回 `MessageStart = 0`；
4. 从最旧的可删除 User 边界开始，依次尝试删除更多完整前缀；
5. 每个候选 Context 都保留原 System Prompt、Tool Specs 和受保护后缀；
6. 第一个满足预算的候选即为保留信息最多的结果；
7. 没有候选可满足预算时返回 `ErrCannotFit`。

首版采用清晰的顺序扫描。它不假设外部 Estimator 对任意消息序列严格单调，也不为了提前优化而引入复杂索引。未来只有在基准表明确证明是瓶颈时，才考虑可验证的缓存或二分策略。

### 7.3 不做内容级截断

TailReducer 不截断单条文本、Tool Arguments、Tool Result、图片或 Tool Schema。一条受保护消息本身过大时明确失败，由应用在输入边界、Tool Service 或专门的内容治理组件解决。静默截断 JSON、图片或工具结果可能破坏协议，不能作为窗口模块默认行为。

## 8. Agent 集成设计

### 8.1 Builder API

在两个具体策略 Builder 中分别增加可选组件：

```go
func (b *SingleTurnBuilder) UseContextWindow(contextwindow.Reducer) error
func (b *ToolLoopBuilder) UseContextWindow(contextwindow.Reducer) error
```

并在构建后的策略中保存只读引用：

```go
type SingleTurnStrategy struct {
    session       session.Service
    contextWindow contextwindow.Reducer
}

type ToolLoopStrategy struct {
    tools         tool.Service
    session       session.Service
    contextWindow contextwindow.Reducer
    // 现有字段保持不变。
}
```

Builder 规则与现有组件一致：

- Reducer 可选；未配置时保持完整历史直传；
- 拒绝 nil、typed nil 和重复设置；
- Build 成功后 Builder 冻结；
- 建造后的策略不提供可变 setter；
- `NewSingleTurnStrategy()` 继续返回不带 Session 和窗口治理的无状态策略。

Agent Builder 不增加 `UseContextWindow`。

### 8.2 受保护消息的确定

Reducer 只治理“旧历史”，不能删除当前 Run 正在处理的消息。

Session Run：

- Session Snapshot 中已提交的消息是可删除候选；
- `Request.Session.Messages` 全部受保护；
- ToolLoop 本 Run 新产生的 Assistant 和 Tool 消息继续加入受保护后缀。

无状态 Run 无显式“历史/本次输入”分界，因此采用保守规则：

- 从最后一条 `RoleUser` 到结尾的消息全部受保护；
- 如果没有 `RoleUser`，全部消息受保护；
- ToolLoop 本 Run 后续生成的消息继续受保护。

该规则不修改 `agent.Request`，保持现有无状态 API 兼容。应用如果需要精确区分大量旧历史与本次输入，应使用 Session 形态，或在调用前自行整理完整历史。

当 Session 新输入从 Tool/Assistant 开始时，保护数量仍保证这些消息不会被删除；TailReducer 还会把切点回退到不晚于保护边界的 User 消息，避免留下孤立工具协议。

### 8.3 公共私有辅助逻辑

在 `acore/agent/context_window.go` 增加私有辅助函数，职责为：

1. 深拷贝 `LLM.Model()`、System Prompt、Messages 和 Tool Specs；
2. 从已合并的 `ModelOptions.MaxTokens` 计算 `RequestedOutputTokens`；
3. 调用公开 `contextwindow.Apply`；
4. 用返回的完整 Context 替换本次 `model.Request.Context`；
5. 保留 Temperature、MaxTokens 和 Reasoning 等模型生成选项；
6. 使用 `ErrReduceContextWindow` 包装组件错误。

通用的 Context 取消、结果校验和深拷贝由 `contextwindow.Apply` 负责。Agent 私有辅助函数只处理运行策略到模块契约的转换和 Agent 错误边界。即使外部 Reducer 错误地保留或修改收到的 slice，也不能影响策略的完整 `workingMessages`、Session 提交或 Provider 请求。

### 8.4 SingleTurn 数据流

```text
Request
  └─► prepareSessionRun（可选加载完整历史）
        └─► 构建完整 model.Request
              └─► Context Window Reducer（可选，一次）
                    └─► LLM.Generate
                          └─► 成功结果提交完整 Session 增量
```

Reducer 在 Session Load 后调用，因此能看到完整历史；在 `LLM.Generate` 前调用，因此失败时不会产生模型请求。

### 8.5 ToolLoop 数据流

```text
完整 workingMessages
  └─► 构建第 N 轮 model.Request（含 System Prompt + Tool Specs）
        └─► Context Window Reducer（每轮调用）
              └─► 第 N 轮 LLM.Generate
                    ├─无 Tool Call ─► 提交完整 Session 增量
                    └─有 Tool Call ─► 执行工具并扩展完整 workingMessages
                                           └─► 下一轮重新治理窗口
```

ToolLoop 必须在每个模型轮次前调用 Reducer，而不是只在首轮调用，因为 Assistant Tool Call 和 Tool Result 会持续占用窗口。

裁剪后的消息不写回 `toolLoopRunState.workingMessages`。每轮都从完整状态重新构造候选 Context，防止请求视图污染运行状态和最终 Session。

## 9. 错误、取消与流语义

Agent 包计划增加：

```go
var (
    ErrNilContextWindowReducer = errors.New("agent: nil context window reducer")
    ErrContextWindowAlreadySet = errors.New("agent: context window reducer already set")
    ErrReduceContextWindow     = errors.New("agent: reduce context window")
)
```

语义如下：

- Builder 错误直接返回，不冻结 Builder；
- 首轮 Reduce 发生在首个模型 Stream 建立前，失败由 `Run` 直接返回；
- ToolLoop 后续轮次 Reduce 发生在 Agent Stream 消费期间，失败通过 Stream error 返回；
- Reduce 失败不产生对应轮次的 Model 事件，不产生 RunDone；
- Reduce 失败不提交 Session，也不自动重跑模型或工具；
- 如果前一轮工具已经执行，后续 Reduce 失败不能撤销其外部副作用，这与后续模型建流失败的现有语义一致；
- `context.Canceled` 和 `context.DeadlineExceeded` 原样优先返回；
- Reducer/Estimator 不得吞掉 Context 取消；
- 错误链同时保留 `agent.ErrReduceContextWindow` 和具体 `contextwindow` 或 Estimator 错误，便于 `errors.Is/As` 判断。

首轮 Reducer 调用仍发生在 `EventRunStart` 之前，与当前首轮 `LLM.Generate` 的建流时机一致，不改变现有事件顺序。

## 10. 并发、所有权与生命周期

- 具体策略实例只持有 Reducer 的只读接口引用；
- Model、Context、Messages、ToolCall Arguments、Thinking Signature、Tool Specs Schema 均按现有深拷贝规则隔离；
- Reducer 不得保留一次调用的可变输入；框架仍会在边界复制，避免下游共享；
- Reducer 和 Estimator 必须支持并发调用，或在自身内部同步；
- TailReducer 不维护 Run 状态；候选索引、预算和估算值都是调用局部变量；
- Builder 仍只用于启动期单 goroutine 组装；
- Reducer/Estimator 首版没有 `Close`；如具体实现持有资源，由应用按其具体类型管理生命周期；
- 构建后的策略不能原地替换 Reducer，需要变更时重新构建策略和 Agent。

## 11. 安全与可观测性

上下文可能包含用户输入、私密历史、图片、Thinking Signature、Tool Arguments、Tool Result 和 Tool Schema。安全边界如下：

- 框架默认不记录或发布完整 Input/Context；
- 错误只报告操作、模型 ID、索引和 Token 数，不拼接内容；
- Estimator 是能够读取完整模型输入的受信任组件，应用不得注入来源不明的实现；
- 不把 API Key、Header 或 Provider 连接配置传给 Reducer/Estimator；
- TailReducer 只返回索引，不返回或缓存内容；
- 首版 Agent Result/Event 不增加估算 Token 或删除消息数，避免在未定义观测稳定性前扩大公开协议；
- 如未来增加指标，只记录模型/Provider ID、预算、估算值、删除数量、耗时和错误类别等低敏感元数据，完整内容必须显式启用并经过脱敏设计。

窗口治理不是 Prompt Injection 防护，也不是数据访问控制。被保留的历史仍会发送给 Provider，应用仍需负责 Session 授权、数据保留和 Provider 合规。

## 12. 实现文件范围

实际实现范围：

```text
acore/contextwindow/
  window.go                Input、Result、Reducer、ReducerFunc、Apply、错误
  estimator.go             Estimator 和 EstimatorFunc
  tail.go                  TailConfig、TailReducer 和预算/裁剪算法
  clone.go                 Model Context 深拷贝
  window_test.go           Apply、公开契约、结果校验、副本、错误与 Context
  estimator_test.go        EstimatorFunc 契约
  tail_test.go             预算、User 边界、保护后缀和 Estimator 行为
  example_test.go          自定义 Estimator + TailReducer 组装示例

acore/model/
  llm.go                   澄清 ContextWindow/MaxOutputTokens 字段语义

acore/agent/
  agent.go                 Agent 侧稳定错误
  context_window.go        策略适配、保护消息计算和 Agent 错误边界
  single_turn.go           每次 SingleTurn 模型调用前治理
  single_turn_builder.go   UseContextWindow
  tool_loop.go             每轮 ToolLoop 模型调用前治理
  tool_loop_builder.go     UseContextWindow
  *_test.go                Builder、Session、流、ToolLoop、并发和副本测试
```

不修改 `session.Service`、`prompt.Renderer`、`RunStrategy`、`agent.Request`、Provider 请求协议、Tool Service 或事件协议。

现有设计文档中与“完整历史直接发送给模型”或“上下文窗口尚未实现”有关的状态已同步；没有重写无关历史方案。

## 13. 测试与验证计划

### 13.1 `contextwindow` 单元测试

至少覆盖：

1. ReducerFunc/EstimatorFunc 正常适配和 nil 函数；
2. nil、typed nil Estimator，负安全余量和负兜底输出配置；
3. nil Context、已取消 Context 和 Deadline；
4. 未知/非法 ContextWindow、未知输出预留和不可用预算；
5. 输出预留的请求值、模型值、配置兜底优先级；
6. System Prompt、Messages、Tool Specs 全部进入 Estimator；
7. 完整输入已适配时返回起点 0；
8. 只从 User 边界删除最少的完整旧轮次；
9. 不在 Assistant/Tool 中间切断；
10. 受保护后缀不被删除；
11. 固定内容、单条当前消息或当前 Tool 交互过大时返回 `ErrCannotFit`；
12. Estimator 错误、负数结果和错误链；
13. Estimator 修改输入时不污染调用方数据；
14. Apply 拒绝 Reducer 的负数、越界、进入保护后缀和非 User 切点；
15. Reducer 修改 Apply 输入副本时不污染最终 Context；
16. 并发 Reduce 不共享候选状态。

### 13.2 Agent 集成测试

至少覆盖：

1. 两个策略 Builder 的 nil、typed nil、重复配置、失败后重试和 Build 冻结；
2. 未配置 Reducer 时模型收到的请求与当前行为一致；
3. SingleTurn 在 Session Load 后、模型调用前治理一次；
4. 无状态请求保护最后一个 User 轮次；
5. Session 请求保护全部新输入，不裁剪 Session 提交内容；
6. ToolLoop 每轮治理，后续轮次包含本 Run 的 Assistant Tool Call 和 Tool Result；
7. Reducer 能看到 System Prompt、完整 Tool Specs、模型描述和已合并 MaxTokens；
8. 非法 MessageStart（负数、越界、进入保护后缀、从非 User 开始）被拒绝；
9. Reducer 修改收到的 Input 不污染 workingMessages、Provider 请求或 Session；
10. 首轮 Reduce 失败不调用模型、不产生 Stream；
11. 后续 Reduce 失败不发起下一模型轮次、不提交 Session、不产生 RunDone；
12. 取消、早停和错误链语义；
13. 同一策略实例并发 Run 的保护数量、消息和 Reducer 调用互相隔离。

### 13.3 执行命令

在 `acore` Go 模块目录中执行：

```bash
gofmt -w contextwindow/*.go agent/*.go model/llm.go
go test ./contextwindow ./agent
go test ./...
go vet ./...
go test -race ./contextwindow ./agent
```

只格式化实际改动的 Go 文件。若实现阶段没有引入共享可变状态，竞态测试仍用于验证外部组件调用和策略并发边界。

实施验证（2026-08-26）：

- `go test ./contextwindow ./agent`：通过；
- `go test ./...`：通过；
- `go vet ./...`：通过；
- `go test -race ./contextwindow ./agent`：当前环境无法执行。默认关闭 CGO；显式设置 `CGO_ENABLED=1` 后确认环境缺少 `gcc`，失败发生在 `runtime/cgo` 构建阶段，未进入包测试。

## 14. 兼容性与迁移

### 14.1 保持兼容

- `agent.Agent`、`RunStrategy`、`Request`、`RunInput`、Stream、Event 和 Result 均不变；
- `session.Service`、`prompt.Renderer`、`tool.Service` 和 `model.LLM` 接口不变；
- 不配置 Reducer 时不读取或要求 `Model.ContextWindow/MaxOutputTokens`，现有模型配置继续工作；
- `NewSingleTurnStrategy()` 的现有无状态行为保持不变；
- 现有 SingleTurnBuilder/ToolLoopBuilder 调用方不需要修改；
- 外部自定义 RunStrategy 不会自动获得窗口治理，也不会因接口变化而编译失败；它可以直接使用公开 `contextwindow` 契约实现自己的接入点；
- Provider 不需要依赖 `contextwindow` 包。

### 14.2 启用后的显式要求

启用 TailReducer 的应用必须满足至少一种输出预留来源：

- Run/Agent 默认 `ModelOptions.MaxTokens`；
- `model.Model.MaxOutputTokens`；
- `TailConfig.FallbackOutputTokens`。

同时必须为 Model 配置正数 `ContextWindow`，并注入适合该 Provider/API/Model 的 Estimator。缺少元数据时显式失败，避免窗口模块看似启用、实际却使用未经确认的默认值。

## 15. 风险与后续演进

### 15.1 已知风险

- Estimator 不准确时仍可能超限，或过度裁剪历史；安全余量只能缓解，不能证明精确；
- 只保留最近完整轮次可能丢失早期约束和事实，应用应把稳定规则放在 System Prompt，把长期事实交给未来的记忆/检索组件；
- System Prompt、Tool Specs 或当前轮次本身过大时无法通过删除历史解决；
- ToolLoop 工具已经产生外部副作用后，下一轮窗口失败不能回滚副作用；
- 每个候选边界都重新估算完整 Context，超长历史和昂贵 Estimator 可能带来额外开销；
- Session 长期保存完整历史，存储仍会增长；窗口模块不等于 Session 保留策略。

### 15.2 后续扩展方向

在真实需求明确后可以独立设计：

- Provider 专属 Estimator 包或适配器；
- 带明确 Usage 和失败语义的摘要 Compactor；
- 摘要缓存及其与 Session revision 的一致性；
- Context Contributor/检索结果的独立预算和信任级别；
- 输入预算观测事件和低敏感指标；
- 针对文本、图片、工具结果和 Tool Specs 的分项限额；
- Provider 返回超限错误后的受控重算策略；
- 在证明 Estimator 单调且性能必要后进行前缀成本缓存或二分裁剪。

这些能力不改变本阶段“Session 保存完整历史、每次模型调用生成临时窗口视图”的基本边界。

## 16. 验收标准

实现完成需同时满足：

1. `contextwindow` 是公开且只依赖 `model` 的独立包；
2. 模块外可以完整实现 Reducer 和 Estimator；
3. TailReducer 统一计算 Prompt、Messages、Tools、输出预留和安全余量；
4. TailReducer 只删除完整旧 User 轮次，不改写消息内容；
5. 当前 Run 输入和本 Run 生成消息不会被裁剪；
6. SingleTurn 在唯一模型调用前治理，ToolLoop 在每轮模型调用前治理；
7. Reducer 只通过具体策略 Builder 注入，Agent Builder 不感知；
8. Session 加载和提交保持完整历史，不保存临时裁剪视图；
9. 未配置 Reducer 时行为与现有实现一致；
10. 缺少模型窗口、输出预留或有效 Estimator 时显式失败；
11. 取消、错误链、Stream、早停和工具副作用语义明确且测试覆盖；
12. 输入输出深拷贝和并发运行测试通过；
13. `go test ./...` 和 `go vet ./...` 通过；相关 `go test -race` 在具备 C 编译器的环境中执行。

## 17. 实施结论

实现保持了确认的四项边界：

1. Reducer 由具体策略 Builder 注入，而不是 Agent Builder；
2. 首版只做完整旧前缀裁剪，不做摘要或消息内容改写；
3. Session 永久保持完整历史，裁剪仅影响单次 Provider 请求；
4. Core 不内置声称通用准确的 Tokenizer，启用时必须显式注入 Estimator，并提供可靠的模型窗口和输出预留。

除当前环境缺少 C 编译器导致竞态测试未执行外，业务代码、单元/集成测试和相关文档已按本文范围完成。
