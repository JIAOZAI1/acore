# LLM 抽象设计

## 1. 目标

`internal/model` 只定义厂商无关的 LLM 协议，不实现 OpenAI、Anthropic、Gemini 等具体协议。

核心目标：

1. 上层 Agent 不感知厂商请求格式。
2. 同时支持流式输出、多轮消息、推理内容、图片和工具调用。
3. `Model` 只描述模型，`Provider` 负责厂商行为。
4. 一个可调用的 `LLM` 是 `Provider + Model` 的绑定结果。
5. 流正常结束、运行期错误和建流错误具有明确且不同的语义。

暂不纳入核心协议：厂商私有参数、API Key、HTTP Header、重试和计费。这些属于 Provider 配置或更上层的运行策略。

---

## 2. 分层

```text
Agent / Looper
      │
      ▼
LLM（已绑定 Provider 和 Model）
      │
      ▼
Provider（认证、传输、协议转换）
      │
      ▼
厂商 API
```

### Model

纯数据描述符，包含模型 ID、所属 Provider、上下文窗口、输出上限和能力信息，不包含网络行为或凭据。

### Provider

具体厂商的扩展点，负责：

- 管理认证与客户端配置；
- 提供模型目录；
- 将统一 `Request` 转换为厂商请求；
- 将厂商流转换为统一 `Stream`；
- 在消费方提前退出时释放连接等资源。

### LLM

面向 Agent 的主要接口。它已经绑定一个 Provider 和一个 Model，因此每次生成不会再出现“实际调用哪个模型”的歧义。

```go
type LLM interface {
    Model() Model
    Generate(ctx context.Context, req Request) (Stream, error)
}
```

通过 `Bind(provider, model)` 创建，也可以通过 `ProviderRegistry.LLM(providerID, modelID)` 查找并创建。

---

## 3. 统一消息协议

### Context

一次请求携带完整且只读的对话上下文：

```go
type Context struct {
    SystemPrompt string
    Messages     []Message
    Tools        []ToolSpec
}
```

Provider 不得修改调用方传入的切片、消息或内容块。

### Message

消息只保存可回放的对话数据：

- `RoleUser`：用户输入；
- `RoleAssistant`：模型回复；
- `RoleTool`：工具执行结果，必须设置 `ToolCallID`。

Token 用量和停止原因不写入 Message，而是放在本次生成的 `Result` 中，避免把调用元数据混入下一轮对话。

### ContentBlock

统一支持：

- `ContentText`：普通文本；
- `ContentThinking`：推理文本及厂商签名；
- `ContentImage`：URL 或 base64 图片；
- `ContentToolCall`：工具名称、调用 ID 和原始 JSON 参数。

工具参数使用 `json.RawMessage`，避免转为 `map[string]any` 后丢失数字精度或改变回放内容。

---

## 4. 请求与结果

### Request

核心请求只提供可跨厂商表达的参数：

```go
type Request struct {
    Context     Context
    Temperature *float64
    MaxTokens   *int
    Reasoning   *ReasoningLevel
}
```

API Key、BaseURL、Header 由 Provider 构造参数管理，不进入 Request，避免凭据被意外序列化，也避免 Agent 依赖某个厂商。

### Result

```go
type Result struct {
    Message    Message
    Usage      Usage
    StopReason StopReason
    ModelID    string
    ProviderID string
}
```

`StopReason` 只描述成功生成为什么停止：自然结束、长度限制、工具调用、内容过滤等。取消、网络错误、协议错误使用 Go `error` 表达。

---

## 5. 流协议

流使用：

```go
type Stream = iter.Seq2[Event, error]
```

正常事件顺序：

```text
start
  contentStart(index=0, kind=thinking)
  contentDelta(index=0, ...)
  contentEnd(index=0, final block)
  contentStart(index=1, kind=text)
  contentDelta(index=1, ...)
  contentEnd(index=1, final block)
done(result)
```

事件含义：

| 事件 | 含义 |
|---|---|
| `EventStart` | 生成开始 |
| `EventContentStart` | 一个内容块开始，携带块类型和必要元数据 |
| `EventContentDelta` | 文本、推理文本或工具参数 JSON 片段 |
| `EventContentEnd` | 内容块结束，携带最终完整块 |
| `EventDone` | 唯一正常终止事件，携带完整 Result |

错误分两类：

1. `Generate` 直接返回 error：参数、认证或建立请求失败，尚未形成流；
2. Stream yield error：建流后发生网络、解析或取消错误。

错误不再伪装成事件，因此不需要同时维护 `EventError` 和 Go error 两套错误通道。

### 流不变量

Provider 实现必须保证：

1. 正常流恰好产生一个 `EventDone`；
2. 运行期失败 yield 一个 error 后立即结束；
3. `ContentIndex` 在同一内容块生命周期内保持稳定；
4. `EventContentEnd.Block` 与最终 `Result.Message.Content` 一致；
5. 消费者提前停止迭代时，生成器通过 `defer` 关闭响应体和其他资源；
6. context 取消后尽快停止底层 IO，并返回 `context.Canceled` 或 `context.DeadlineExceeded`；
7. 禁止没有 done 或 error 的静默结束。

`Complete` 是流的收拢函数。它读取 `EventDone.Result`；静默结束会返回 `ErrUnexpectedStreamEnd`。

---

## 6. Provider 注册与模型寻址

Provider ID 和 Model ID 组成完整模型地址：

```text
providerID/modelID
```

不能只按 Model ID 全局查找，因为不同 Provider 可能暴露同名模型。

`ProviderRegistry`：

- 拒绝 nil Provider；
- 拒绝重复 Provider ID；
- 返回确定顺序的 Provider 列表；
- 通过 `(providerID, modelID)` 创建绑定后的 LLM。

---

## 7. 文件职责

```text
internal/model/
├── llm.go       # 消息、请求、结果、事件、Stream、LLM、Bind、Complete
├── provider.go  # Provider 接口和 ProviderRegistry
└── llm_test.go  # 抽象契约测试，不依赖任何真实厂商

internal/provider/
└── openai/      # OpenAI Chat Completions Provider 与测试
```

后续厂商实现继续使用独立子包，避免核心协议反向依赖实现：

```text
internal/provider/anthropic/
internal/provider/gemini/
```

如果该能力需要被仓库外的项目直接导入，应在 API 稳定后将协议移动到非 `internal` 的公共包，例如 `llm/`；否则 Go 会阻止外部项目导入。

---

## 8. 后续实现顺序

1. 为 Message、ContentBlock、ToolSpec 增加完整协议校验；
2. 增加流事件顺序的通用契约测试；
3. 继续完善 Provider 配置和凭据管理，但不把凭据放入 Request；
4. 以 OpenAI Provider 作为首个参考实现完善契约测试；
5. 实现 Agent 工具循环和重试策略；
6. 协议稳定后再增加其他 Provider。
