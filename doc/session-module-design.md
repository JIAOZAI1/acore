# Session 模块设计方案

## 1. 背景与结论

当前 `acore/agent` 已将 Agent 执行算法抽象为 `RunStrategy`，并提供 `SingleTurnStrategy` 和 `ToolLoopStrategy`。Session 会影响策略使用的消息输入以及策略成功后的历史提交，因此应当是具体运行策略的可组装依赖。

本方案确定：

1. 新增公开 `session.Service` 抽象，负责加载和原子追加可重放的 `model.Message`；
2. Session Service 由各具体策略 Builder 直接注入，并由建造后的策略实例持有；
3. `SingleTurnStrategy` 和 `ToolLoopStrategy` 分别在自身 `Run` 流程中加载、使用和提交 Session；
4. Agent Builder 只接收已构建好的 RunStrategy，不接收、保存或调用 Session；
5. 不新增 Session Runner、SessionStrategy 装饰器、Facade 或第二套 Agent 调用接口。

组装结构：

```text
session.Service ──► SingleTurnBuilder ──► SingleTurnStrategy

session.Service ──► ToolLoopBuilder ────► ToolLoopStrategy
tool.Service    ──┘

model.LLM + 已构建 RunStrategy ──► Agent Builder ──► Agent
```

## 2. 目标与非目标

### 2.1 目标

- Session 作为运行策略的显式组件，不上移到 Agent 调用层；
- 模块外可实现、替换和测试 Session Service；
- 不需要 Session 的策略保持现有无状态用法；
- 同一策略实例可安全处理多个并发 Run；
- 明确历史所有权、提交点、错误、取消和并发冲突语义；
- 保持 `Agent.Run`、`RunStrategy.Run`、Agent Stream 和 `Complete` 方法签名不变。

### 2.2 非目标

首个里程碑不包含：

- Agent 上层 Session Runner 或 RunStrategy 外层 Session 包装器；
- Token 估算、上下文裁剪、摘要或压缩；
- 长期记忆、用户画像或向量检索；
- Interrupt/Resume 和 Checkpoint；
- 工具副作用的事务回滚或 exactly-once 保证；
- 会话标题、分支、搜索、分页或 UI 元数据；
- 通过 `context.Context` value 隐式传递 Session Key 或 Service；
- 在 Agent Builder 中判断策略类型并自动注入 Session。

## 3. 模块与依赖边界

新增 `github.com/JIAOZAI1/acore/session` 包：

```text
session ──► model
agent   ──► session
agent   ──► model / tool
```

- `session` 只依赖 `model.Message`，不依赖 `agent`、`tool`、Provider 或具体存储；
- `agent` 中需要会话能力的具体策略依赖 `session.Service`；
- Agent Builder 不感知 RunStrategy 是否持有 Session；
- 应用负责先构建 Session Service 和具体 RunStrategy，再将策略交给 Agent Builder；
- 外部 RunStrategy 可在自己的 Builder 中接收公开 `session.Service`。

## 4. Session 公开契约

```go
package session

import (
    "context"

    "github.com/JIAOZAI1/acore/model"
)

// Key identifies one conversation in an application-defined scope.
type Key struct {
    Scope string `json:"scope"`
    ID    string `json:"id"`
}

// Revision is the optimistic-concurrency version of one conversation.
// Revision 0 represents an absent conversation.
type Revision uint64

// Snapshot is one history view returned by a Service.
type Snapshot struct {
    Revision Revision        `json:"revision"`
    Messages []model.Message `json:"messages"`
}

// Service loads and atomically appends replayable conversation messages.
// Implementations must be safe for concurrent use.
type Service interface {
    Load(context.Context, Key) (Snapshot, error)
    Append(context.Context, Key, Revision, []model.Message) (Revision, error)
}
```

Service 契约：

1. 会话不存在时，`Load` 返回 revision 0 和空 Messages，不返回 not-found 错误；
2. `Append` 必须原子比较 expected revision、追加整批 Messages 并将 revision 增加 1；
3. revision 不匹配时返回 `ErrConflict`，不部分写入、覆盖或自动合并；
4. 空 Key 和空追加批次是无效输入；
5. revision 溢出时返回 `ErrRevisionExhausted`，不回绕到 0；
6. Service 必须对输入输出建立数据所有权边界，不与调用方共享可变 slice/RawMessage；
7. Service 必须响应 Context 取消，不保存 Context 或单次 Run 状态。

稳定错误：

```go
var (
    ErrInvalidContext    = errors.New("session: invalid context")
    ErrInvalidKey        = errors.New("session: invalid key")
    ErrInvalidMessages   = errors.New("session: invalid messages")
    ErrInvalidSnapshot   = errors.New("session: invalid snapshot")
    ErrConflict          = errors.New("session: conflict")
    ErrRevisionExhausted = errors.New("session: revision exhausted")
)
```

首版提供并发安全的 `MemoryService`，用于测试、示例和单进程临时会话。真实数据库实现由应用或独立适配模块根据实际选型提供。

## 5. Agent Request 的会话输入

现有 `Request.Messages` 是无状态 Run 的完整历史。为避免该字段在有无 Session 时隐式切换语义，Request 使用两种互斥输入：

```go
// SessionInput contains the key and new, uncommitted messages for one run.
type SessionInput struct {
    Key      session.Key     `json:"key"`
    Messages []model.Message `json:"messages"`
}

// Request contains exactly one of Messages and Session.
type Request struct {
    Messages []model.Message `json:"messages,omitempty"`
    Session  *SessionInput   `json:"session,omitempty"`
    Options  ModelOptions    `json:"options,omitempty"`
}
```

| 运行形态 | `Request.Messages` | `Request.Session` |
|---|---|---|
| 无状态 Run | 调用方准备的完整历史 | nil |
| Session Run | nil/空 | Key + 本次尚未保存的新消息 |

规则：

- 同时填充 `Messages` 和 `Session`、两者都空或 Session.Messages 为空时返回 `ErrInvalidRequest`；
- `configuredAgent` 负责互斥形态校验、ModelOptions 合并和输入深拷贝，不做 Session I/O；
- 具体策略收到 SessionInput 后，使用自身持有的 Service 处理；
- 未组装 Session Service 的策略收到 SessionInput 时返回 `ErrSessionUnsupported`；
- 已组装 Session Service 的策略仍可处理无状态 Request，此时不调用 Service。

## 6. 策略 Builder 设计

### 6.1 SingleTurnBuilder

当前 SingleTurn 只有无参构造函数。为支持组件注入，新增：

```go
type SingleTurnBuilder struct {
    session    session.Service
    sessionSet bool
    built      bool
}

func NewSingleTurnBuilder() *SingleTurnBuilder
func (b *SingleTurnBuilder) UseSession(session.Service) error
func (b *SingleTurnBuilder) Build() (*SingleTurnStrategy, error)

type SingleTurnStrategy struct {
    session session.Service
}
```

- Session 是可选组件；未配置时构建无状态 SingleTurn；
- `UseSession` 拒绝 nil/typed nil 和重复设置；
- 成功 Build 后 Builder 冻结，策略不暴露 Session setter；
- 现有 `NewSingleTurnStrategy()` 保留为无状态简捷入口；
- 需要 Session 时使用 SingleTurnBuilder。

### 6.2 ToolLoopBuilder

在现有 ToolLoopBuilder 上增加：

```go
func (b *ToolLoopBuilder) UseSession(session.Service) error

type ToolLoopStrategy struct {
    tools     tool.Service
    toolSpecs []model.ToolSpec
    limits    ToolLoopLimits
    errorMode ToolErrorMode
    session   session.Service
}
```

Session 为可选组件，Tool Service 仍是 ToolLoop 的必填组件。`Build` 将 Session Service 引用快照到不可变 ToolLoopStrategy。

### 6.3 Builder 共同语义

- `UseSession(nil)` 或 typed nil 返回 `ErrNilSessionService`；
- 重复设置返回 `ErrSessionServiceAlreadySet`；
- Builder 成功后再配置返回对应 Builder Built 错误；
- Builder 仅用于单 goroutine 启动期组装；
- 策略可并发 Run，注入的 Session Service 也必须并发安全。

外部 RunStrategy 通过自己的 Builder 或构造函数接收 `session.Service`。不向 `RunStrategy` 接口追加 Session 方法，也不定义统一可变 setter。

## 7. 策略内部运行流程

### 7.1 准备历史

每个内置策略在自身 `Run` 入口处：

1. 无状态 Request：直接使用 `Request.Messages`，不调用 Session Service；
2. Session Request 但策略未注入 Service：返回 `ErrSessionUnsupported`；
3. Session Request：通过 `Service.Load` 取得 Snapshot；
4. 按“Snapshot.Messages → SessionInput.Messages”拼接为本次完整历史；
5. 保留 Key、revision 和本次新消息作为该 Run 的私有状态；
6. 策略实例不保存任何单次 Run 数据。

SingleTurn 和 ToolLoop 可复用 `agent` 包内的非导出 prepare/commit 辅助函数，但该辅助逻辑不实现 `RunStrategy`，不包装 Agent 或具体策略，不是新的执行层。

### 7.2 SingleTurn 提交点

```text
SingleTurnStrategy.Run
  ├─准备完整历史
  ├─LLM.Generate
  └─Model Done
       ├─构造 agent.Result
       ├─提交 Session（如果存在）
       └─产生 EventRunDone
```

### 7.3 ToolLoop 提交点

```text
ToolLoopStrategy.Run
  ├─准备完整历史
  ├─模型 / Tool 循环
  └─最终 Assistant 消息且不再包含 Tool Call
       ├─构造 agent.Result
       ├─提交 Session（如果存在）
       └─产生 EventRunDone
```

ToolLoop 中间 Assistant Tool Call 和 Tool Result 只保存在当前 `toolLoopRunState.generatedMessages`，直到策略完整成功才一次提交。

## 8. 历史提交语义

一次成功 Session Run 原子追加：

```text
Request.Session.Messages
    + Result.GeneratedMessages
```

不保存 System Prompt、ModelOptions、Usage、StopReason、Model/Provider ID、流式 delta、Agent 事件或运行控制状态。

规则：

1. Session Append 在 `EventRunDone` 对调用方可观察之前完成；
2. 看到 RunDone 代表策略成功且历史已提交；
3. Append 失败时 Stream 返回错误，不产生 RunDone；
4. 模型或 Tool 错误、超限、Context 取消、协议错误或 RunDone 前早停均不提交；
5. 不自动重试 Agent Run 或在冲突后重放策略；
6. Append 失败不能回滚已经发生的 Tool 副作用。

`Result.GeneratedMessages` 是策略生成的规范历史增量。Session 逻辑不根据 `Result.Output` 猜测或补齐消息。

## 9. 错误语义

`agent` 新增稳定错误：

```go
var (
    ErrSingleTurnBuilderBuilt   = errors.New("agent: single turn builder already built")
    ErrNilSessionService        = errors.New("agent: nil session service")
    ErrSessionServiceAlreadySet = errors.New("agent: session service already set")
    ErrSessionUnsupported       = errors.New("agent: session input unsupported")
    ErrLoadSession              = errors.New("agent: load session")
    ErrCommitSession            = errors.New("agent: commit session")
)
```

- Load 失败同时保留 `ErrLoadSession` 和 Service 原错误链；
- Append 失败同时保留 `ErrCommitSession` 和 Service 原错误链；
- CAS 冲突可同时通过 `errors.Is(err, agent.ErrCommitSession)` 和 `errors.Is(err, session.ErrConflict)` 判断；
- Context 已取消时优先返回 `ctx.Err()`；
- 错误文本不包含历史消息、Tool Arguments、Thinking 内容或完整 Session Key。

## 10. 并发与数据所有权

### 10.1 策略并发

- 策略实例只持有 Session Service 引用和不可变配置；
- Key、Snapshot、revision、本次输入和生成消息只位于当前 Run；
- 同一策略实例可并发处理不同 Session Key；
- Session Service 必须并发安全，策略不为它增加全局锁。

### 10.2 同 Session 并发

同一 Key 的两个 Run 可同时加载 revision R：

```text
Run A: Append(expected=R) ──► 成功，revision=R+1
Run B: Append(expected=R) ──► ErrConflict，不写入
```

CAS 防止历史被覆盖，但不阻止两个 Run 都先调用模型或工具。若业务要求在副作用前串行化同 Session Run，应在应用调度层引入会话粘性、有界队列或 lease 协调，不扩大历史 Service 职责。

### 10.3 深拷贝

`configuredAgent`、具体策略和 MemoryService 需要复制：

- `[]model.Message` 和 `Message.Content`；
- Thinking Signature；
- ToolCall 及 `json.RawMessage` Arguments；
- `SessionInput` 指针；
- ModelOptions 指针字段；
- 对外暴露的 Event/Result。

不为此导出通用 Clone API，复制实现保持在模块内部。

## 11. 安全与状态边界

- `Key.Scope` 是应用定义的隔离域，Service 必须将 `(Scope, ID)` 作为联合 Key；
- Scope 不等于授权，应用必须根据已认证主体生成 Scope；
- Session 可能包含 Prompt、图像、Thinking Signature、Tool Arguments 和 Tool Result，持久化实现需明确加密、访问控制、保留期和删除策略；
- 框架默认不记录消息内容或完整 Session Key；
- Session 只保存跨 Run 可重放消息，不保存单次运行状态、Checkpoint、长期记忆或审计事件；
- Session 始终加载和保存完整历史，不按模型窗口修改持久化数据；具体策略可按[上下文窗口模块设计方案](context-window-module-design.md)注入 Reducer，仅裁剪单次模型请求视图。摘要 Compactor 仍未实现。

## 12. 预计实现范围

```text
acore/session/
  session.go             Key、Revision、Snapshot、Service 和错误
  clone.go               消息副本
  memory.go              MemoryService
  memory_test.go

acore/agent/
  agent.go               SessionInput、Request 形态和错误
  clone.go               SessionInput 副本
  run.go                 Request 形态校验，不做 Session I/O
  session.go             内置策略共用的私有 prepare/commit 辅助
  single_turn.go         SingleTurn 的 Session 运行逻辑
  single_turn_builder.go SingleTurnBuilder
  tool_loop.go           ToolLoop 的 Session 运行逻辑
  tool_loop_builder.go   ToolLoopBuilder.UseSession
  *_test.go              Builder、流、错误、副本和并发测试
```

不修改 `model`、`tool`、`event` 和 Provider 行为。实现时同步修正已有文档中关于 Request 和 SingleTurn 构建方式的过时描述。

## 13. 测试与验证

至少覆盖：

1. Session Service 的空会话、追加、revision、冲突、溢出、Context 和深拷贝；
2. MemoryService 同 Key 并发 Append 仅一个成功，不同 Key 隔离；
3. SingleTurnBuilder/ToolLoopBuilder 的 nil、typed nil、重复配置和冻结；
4. 无状态 Request、Session Request 以及非法互斥形态；
5. 未注入 Session 的策略拒绝 SessionInput；
6. 已注入 Session 的策略处理无状态 Request 时不访问 Service；
7. SingleTurn/ToolLoop 的历史拼接、提交内容和事件顺序；
8. 建流错误、Stream 错误、Tool 错误、超限、取消和早停不提交；
9. Append 错误/CAS 冲突不产生 RunDone，不重跑模型或工具；
10. 同一策略实例并发 Run 时请求、Snapshot 和 revision 互相隔离。

在 `acore` 模块目录中执行：

```bash
gofmt -w session/*.go agent/*.go
go test ./session ./agent
go test ./...
go vet ./...
go test -race ./session ./agent
```

## 14. 兼容性与风险

### 14.1 兼容性

- 现有 `Request{Messages: ...}` 调用保持不变；
- `Request` 尾部增加可选 `Session` 字段，keyed struct literal 不受影响；
- 使用位置字面量构造 `Request` 的外部代码需迁移；
- `NewSingleTurnStrategy()` 保留为无状态入口；
- ToolLoopBuilder 的现有 Tool、Limits 和 ErrorMode 语义不变；
- `RunStrategy` 接口不增加方法，外部实现不因本次功能破坏编译。

### 14.2 已知风险

- 未注入上下文窗口 Reducer 时，完整历史仍可能超出模型窗口；已注入时，固定 Prompt、Tool Specs 或当前受保护轮次本身过大仍会明确失败；
- Session Append 失败不能撤销已执行的 Tool 副作用；
- CAS 是事后冲突检测，不能阻止并发 Run 都调用模型或工具；
- 远程 Append 可存在“服务端已写入但响应丢失”的不确定结果；
- 新增 RunStrategy 不会自动拥有 Session，必须在它自身的设计中明确是否支持和提交点。

## 15. 验收标准

- Session Service 只通过具体策略 Builder 注入；
- SingleTurnStrategy 和 ToolLoopStrategy 直接持有 Service；
- Agent Builder 不包含 Session 字段或 `UseSession` 方法；
- 不存在 Session Runner、SessionStrategy 或其他包装执行层；
- 模块外可完整实现 `session.Service`；
- 无状态运行保持现有行为，Session Run 使用显式 Key 和本次新消息；
- 两个内置策略在各自成功终止分支中提交历史；
- 只有 Session Append 成功才产生 Agent RunDone；
- 失败、取消和 RunDone 前早停不提交部分历史；
- 并发冲突不覆盖历史、不部分写入、不自动重跑策略；
- 相关格式化、测试、静态检查和竞态检测通过。

本文档已经确认，实现按本方案执行。
