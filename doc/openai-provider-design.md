# OpenAI Provider 适配器设计方案

## 1. 背景与结论

当前 `acore/model` 已定义公开的 `model.Provider`、`model.LLM`、统一消息、Tool Call 和流事件协议，但工作区中没有当前生效的真实 Provider 实现。历史上的 `internal/provider/openai` 已处于删除状态，且 `internal` 路径不符合 `acore` 对外公开组件的新定位。

本方案建议新增公开包：

```text
github.com/JIAOZAI1/acore/provider/openai
```

首版结论：

1. 实现 OpenAI **Chat Completions** 流式适配器，对应 `POST /v1/chat/completions`；
2. 实现 `model.Provider`，支持文本、用户图片输入、Function Tool Call、Usage、Reasoning Effort 和流式错误；
3. 对外导出 `Provider`、`Config`、构造函数、Provider/API 标识和可分类的 `APIError`；
4. 使用 Go 标准库实现 HTTP、JSON 和 SSE 解析，首版不引入 OpenAI SDK 或其他第三方依赖；
5. 不修改当前 `model` 公开协议，不恢复已删除的旧 `internal/provider/openai` 包。

OpenAI 官方对新项目推荐 Responses API，但当前 `model.Message` 协议更接近 Chat Completions 的角色消息模型。Responses API 在应用手工管理上下文时要求回传完整输出项，推理模型的 reasoning items 也必须在 Tool Call 后回传；当前协议无法无损保留 Responses output item ID、`phase`、encrypted reasoning item 等 Provider 状态。因此首版先选择能被当前核心契约完整表达的 Chat Completions，Responses API 待 `model` 回放协议单独设计后增加。

## 2. 官方协议依据与本地化取舍

本方案于 2026-08-25 核对了以下 OpenAI 官方文档：

- [Create chat completion](https://developers.openai.com/api/reference/cli/resources/chat/subresources/completions/methods/create)：当前 Chat Completions 请求参数、消息、流、Tool Call、`max_completion_tokens` 和 `reasoning_effort`；
- [Function calling](https://developers.openai.com/api/docs/guides/function-calling)：Function Tool 定义、Tool Call 结果回传和多调用语义；
- [Streaming API responses](https://developers.openai.com/api/docs/guides/streaming-responses)：流式读取、事件和增量数据处理；
- [Create a model response](https://developers.openai.com/api/reference/cli/resources/responses/methods/create)：Responses API 的 input/output item、reasoning encrypted content、Tool Call 和上下文状态要求。

关键本地化取舍：

- 官方文档建议新项目优先尝试 Responses，但适配器首先必须保证 `acore/model` 多轮回放协议完整，不为使用新端点而丢失 Provider 状态；
- Chat Completions 的 `messages`、assistant `tool_calls` 和 tool `tool_call_id` 可直接映射当前 `model.Message`；
- `max_tokens` 已被官方标记为 deprecated，首版统一使用 `max_completion_tokens`；
- 不开启或伪造未由 `model.ToolSpec` 表达的 strict function schema，继续将业务参数校验交给 Tool；
- Provider 只处理协议转换和传输，默认不重试、不限流、不打日志，这些能力应由 LLM 装饰器或应用层组合。

## 3. 目标

1. 模块外调用方可导入 `provider/openai` 并构建可用的 `model.Provider`；
2. 配置、凭证、模型目录和 HTTP Client 显式注入，不读取隐式全局状态；
3. 输入消息和工具 Schema 转换不修改调用方数据；
4. 将 OpenAI SSE 转换为符合 `model.Stream` 不变量的事件序列；
5. 正确区分建流前错误、HTTP API 错误、建流后读取/协议错误和 Context 取消；
6. 调用方提前停止消费流时及时关闭 HTTP 响应体；
7. 构建后的 Provider 不可变并可被多个 goroutine 并发调用。

## 4. 非目标

首版不实现：

- Responses API、Conversations API 或 `previous_response_id`；
- OpenAI 内建 Web Search、File Search、Computer Use、Code Interpreter、MCP 或自定义文本 Tool；
- Audio、Video、File 输入或输出；
- Structured Outputs、JSON Mode、Logprobs、Prompt Cache 策略、Service Tier 或 Provider 特有请求选项；
- 运行时调用 OpenAI Models API 动态获取模型；
- 硬编码一份可能过期的 OpenAI 默认模型列表；
- 自动重试、指数退避、限流、断路、日志或 Trace；
- Agent Tool Loop 或工具实际执行；Provider 只产生 `model.ToolCall`；
- OpenAI-compatible 服务的任意 Provider ID；首版 Provider ID 固定为 `openai`；
- 保证 API 提供商之外的网络代理或兼容端点行为。

## 5. 模块与文件边界

计划新增：

```text
acore/provider/openai/
├── provider.go       # 公开 Config、Provider、New、ID/Models/Generate
├── request.go        # model.Request -> Chat Completions 请求
├── stream.go         # SSE 读取、增量累积和 model.Event 映射
├── error.go          # 稳定错误与 APIError
├── provider_test.go  # 配置、HTTP、错误、资源与并发
├── request_test.go   # 请求映射和校验
├── stream_test.go    # 流事件、Tool Call、Usage 和失败路径
└── example_test.go   # 公开 API 最小示例，不访问真实网络
```

依赖方向：

```text
agent 应用 / 后续 Agent Builder
             │
             ▼
    provider/openai.Provider
             │ implements
             ▼
         model.Provider
             │
             ▼
    OpenAI Chat Completions API
```

约束：

- `model` 不依赖 `provider/openai`；
- `provider/openai` 只依赖 Go 标准库和 `acore/model`；
- OpenAI 线上 JSON/SSE 类型全部保持包内非导出，不进入 `acore/model` 或适配器公开 API；
- `Provider` 不持有 Context，不拥有调用方传入 `http.Client` 的关闭权。

## 6. 公开 API

建议公开契约：

```go
package openai

import (
    "context"
    "errors"
    "net/http"

    "github.com/JIAOZAI1/acore/model"
)

const (
    ProviderID         = "openai"
    APIChatCompletions = "openai-chat-completions"
)

var (
    ErrMissingAPIKey  = errors.New("openai: missing API key")
    ErrNoModels       = errors.New("openai: no models configured")
    ErrInvalidModel   = errors.New("openai: invalid model")
    ErrInvalidRequest = errors.New("openai: invalid request")
    ErrInvalidStream  = errors.New("openai: invalid stream")
)

type Config struct {
    APIKey     string
    BaseURL    string
    HTTPClient *http.Client
    Headers    http.Header
    Models     []model.Model
}

type Provider struct {
    // 字段不导出。
}

func New(config Config) (*Provider, error)

func (p *Provider) ID() string
func (p *Provider) Models() []model.Model
func (p *Provider) Generate(context.Context, model.Model, model.Request) (model.Stream, error)

type APIError struct {
    StatusCode int
    RequestID  string
    Type       string
    Code       string
    Param      string
    Message    string
}

func (e *APIError) Error() string
```

### 6.1 可导出性边界

- `Config`、`Provider`、`New`、Provider/API 标识、稳定可判断错误和 `APIError` 可导出；不支持的角色、内容块或请求参数统一包装 `ErrInvalidRequest`；
- `Provider` 的可变构建状态、HTTP/SSE wire types、累积器和协议转换函数保持非导出；
- 公开方法签名只使用标准库、`acore/model` 和本包导出类型，不泄漏内部 wire type；
- 新包使用外部测试包 `openai_test` 补充公开 API 验收，确保模块外调用方可完整构建和使用。

### 6.2 为何不使用 Provider Builder

首版 Provider 只有一次性配置校验与不可变快照，`Config + New` 已能清晰表达构建过程。为它增加只转存字段的 Builder 会造成重复抽象。后续 Agent Builder 依旧通过 `model.Provider` 或已绑定 `model.LLM` 组装 Provider，不要求每个底层类型都拥有 Builder。

## 7. 配置和构建语义

`New` 行为：

1. `APIKey` 不得为空，缺失时返回 `ErrMissingAPIKey`；
2. `BaseURL` 为空时使用包内默认值 `https://api.openai.com/v1`；
3. 自定义 `BaseURL` 必须是带 `http`/`https` scheme 和 host 的绝对 URL，不允许 query 或 fragment；
4. `HTTPClient` 为 nil 时使用 `http.DefaultClient`；Provider 不隐式设置整请求硬超时，由调用 Context 或应用注入的 Client 决定；
5. `Models` 必须至少包含一项，不提供可过期的默认模型目录；
6. 每个 Model 必须有非空 ID，Model ID 不得重复；
7. Model `Provider` 为空时快照为 `openai`，非空时必须等于 `ProviderID`；
8. Model `API` 为空时快照为 `APIChatCompletions`，非空时必须等于该值；
9. 深拷贝 `Headers`、`Models` 及 Model 中的 slice，构建后调用方修改原数据不影响 Provider；
10. `Models()` 每次返回深拷贝，不暴露内部快照。

`Headers` 用于网关、组织/项目标识或测试。Provider 应先复制自定义 Header，随后覆盖安全关键 Header：

```text
Authorization: Bearer <APIKey>
Content-Type: application/json
Accept: text/event-stream
```

调用方不能通过 `Headers` 覆盖凭证或破坏请求协议。

## 8. 请求映射

### 8.1 请求级字段

| `model.Request` | Chat Completions |
|---|---|
| 已绑定 `model.Model.ID` | `model` |
| `Context.SystemPrompt` | 首条 `developer` message |
| `Context.Messages` | `messages` |
| `Context.Tools` | `tools` 中的 function definitions |
| `Temperature` | `temperature` |
| `MaxTokens` | `max_completion_tokens` |
| `ReasoningDefault` 或 nil | 不发送 `reasoning_effort` |
| `ReasoningOff` | `reasoning_effort: "none"` |
| `ReasoningLow/Medium/High` | `reasoning_effort: "low"/"medium"/"high"` |
| 流协议 | `stream: true` + `stream_options.include_usage: true` |

`Temperature` 必须是 OpenAI Chat Completions 接受的 0–2 之间有限数，`MaxTokens` 必须为正数，未知 `ReasoningLevel` 枚举值必须拒绝。即使调用方绕过 `model.Bind` 直接使用 Provider，也不向远程发送已知无效请求。

System Prompt 映射为 `developer` message，因为 OpenAI 当前文档说明 o1 及更新模型使用 developer message 表达应用级指令。首版不自动按模型名称切换 `system`/`developer`；需要旧模型的应用应显式配置兼容适配器，不在核心中使用模型名称猜测能力。

### 8.2 消息映射

| `model.Message` | Chat Completions |
|---|---|
| `RoleUser` + `ContentText` | user text/content part |
| `RoleUser` + `ContentImage` URL | user `image_url` content part |
| `RoleUser` + `ContentImage` Data | `data:<mime>;base64,<data>` 形式的 `image_url` |
| `RoleAssistant` + `ContentText` | assistant content |
| `RoleAssistant` + `ContentToolCall` | assistant function `tool_calls` |
| `RoleTool` + `ToolCallID` + text | tool message 的 `tool_call_id` 和 content |

校验约束：

- 拒绝 `RoleUnknown`；
- `RoleTool` 必须有 `ToolCallID`，首版只允许文本内容；
- OpenAI Tool Message 没有 `is_error` 字段；`RoleTool.IsError` 作为本地编排元数据不进入线上请求，调用方必须将模型需要看到的失败信息写入文本 content；
- Tool Call 只允许出现在 Assistant 消息，ID、名称非空，参数必须是有效 JSON；
- 图片只允许出现在 User 消息；URL 与 Data 二选一，Data 必须同时提供 MIME type；
- 首版不向 OpenAI 输入传递 `ContentThinking`，因为 Chat Completions 官方协议没有通用的推理文本回放字段；遇到该内容时返回显式的不支持错误，不静默丢弃；
- 混合文本与图片时使用 content part 数组；纯文本消息可使用字符串以保持请求简洁；
- Provider 不校验 Tool Arguments 是 JSON object，只要求它是有效 JSON；具体 Tool 的 object 约束由 Agent/Tool System 在执行前校验。

### 8.3 Tool Spec 映射

```text
model.ToolSpec
  Name        -> function.name
  Description -> function.description
  Parameters  -> function.parameters
```

首版不设置 `strict: true`。当前 `model.ToolSpec` 不表达 strict 意图，而且 `tool.Builder` 只保证 Schema 是 JSON object，不保证满足 strict mode 对 `additionalProperties: false` 和 required fields 的限制。适配器不得为调用方伪造 strict 契约。

## 9. HTTP 与错误语义

### 9.1 建流前错误

`Generate` 直接返回的错误包括：

- nil/canceled Context（nil Context 由 Go 常规用法约束，不接受）；
- 未配置或 Provider/API 不匹配的 Model；
- 不支持的角色、内容块、图片或 Tool Call；
- JSON 请求构建/编码失败；
- HTTP Request 创建或 `Client.Do` 失败；
- 非 2xx HTTP 响应；
- 2xx 响应不是 `text/event-stream`。

Context 已取消或 `Client.Do` 因 Context 结束时，优先返回 `ctx.Err()`，便于调用方使用 `errors.Is`。

### 9.2 APIError

非 2xx 响应最多读取 64 KiB 错误体，优先解析 OpenAI 标准结构：

```json
{
  "error": {
    "message": "...",
    "type": "...",
    "param": "...",
    "code": "..."
  }
}
```

映射为可导出 `*APIError`，并保留 HTTP status 与响应 Header 中的 request ID。错误体无法解析时只返回受限、去首尾空白的摘要，不保存 Header、API Key 或完整响应对象。

Provider 不根据 429/5xx 自动重试。调用方可以声明 `var apiErr *openai.APIError`，再通过 `errors.As(err, &apiErr)` 检查 `StatusCode`，并在上层结合幂等性与 Context 实现有界重试。

## 10. SSE 与 `model.Stream` 映射

### 10.1 流生命周期

`Generate` 收到合法 SSE 响应后返回 `model.Stream`。迭代开始时：

1. yield 一次 `model.EventStart`；
2. 按 SSE 事件边界读取 `data:`，支持同一事件多个 data line；
3. 忽略注释、未使用的 SSE 字段和 JSON 未知字段；
4. 处理 Chat Completion chunk，只接受 choice index 0；请求不设置 `n`，因此预期唯一 choice；
5. 读到 `data: [DONE]` 后检查 finish reason，结束未完成的 block，并 yield 唯一 `model.EventDone`；
6. 任意错误 yield 一次 error 后立即结束，不再产生 Done；
7. 通过 generator `defer` 关闭 response body，包括正常完成、错误和调用方提前停止迭代。

单个 SSE 事件设置显式大小上限（建议 4 MiB），超限返回协议错误，避免错误或恶意端点导致无界内存占用。

### 10.2 内容块累积

当首次观察到某类内容时创建 block 并 yield `EventContentStart`：

- `delta.content` -> 一个 `ContentText` block；
- `delta.refusal` -> 同一个用户可见的 `ContentText` block，不丢弃安全拒绝文本；
- `delta.tool_calls[index]` -> 按 tool index 分配一个 `ContentToolCall` block；
- Tool Call 的 ID 和 function name 保存元数据，arguments 增量追加并产生 `EventContentDelta`；
- Chat Completions 官方协议未提供通用 reasoning text delta，首版不解析未文档化的 `reasoning_content` 扩展字段。

终端时按 block 首次出现的顺序产生 `EventContentEnd`，并使其 `Block` 与 `Result.Message.Content` 完全一致。Tool Arguments 为空时规范为 `{}`，非法 JSON 返回流协议错误。

`EventDone.Result` 的 Message Role 固定为 `RoleAssistant`，`ProviderID` 固定为 `openai`；`ModelID` 优先使用服务端 chunk 返回的实际模型 ID，缺失时回退到请求的 Model ID。

### 10.3 Usage 映射

`stream_options.include_usage: true` 使用流尾 Usage chunk：

| Chat Completions | `model.Usage` |
|---|---|
| `prompt_tokens` | `InputTokens` |
| `completion_tokens` | `OutputTokens` |
| `prompt_tokens_details.cached_tokens` | `CacheRead` |
| `prompt_tokens_details.cache_write_tokens` | `CacheWrite` |
| `completion_tokens_details.reasoning_tokens` | `ReasoningTokens` |
| `total_tokens` | `TotalTokens` |

Usage 未返回时保持零值，不因计费元数据缺失破坏已完成的模型结果。

### 10.4 终止原因映射

| OpenAI `finish_reason` | `model.StopReason` |
|---|---|
| `stop` | `ReasonStop` |
| `length` | `ReasonLength` |
| `tool_calls` / 兼容历史 `function_call` | `ReasonToolUse` |
| `content_filter` | `ReasonContentFilter` |
| 其他未知值 | `ReasonUnknown` |

未知 finish reason 仍是一次 API 成功结束，使用 `ReasonUnknown` 保留向前兼容，不把未知枚举伪造成网络错误。但流在 `[DONE]` 前 EOF、完全缺失 finish reason、JSON 损坏或 Tool Call 不完整时返回 `ErrInvalidStream` 包装错误。

## 11. 并发、数据所有权与安全

- Provider 构建后只读，模型目录和 Header 快照不在运行期修改；
- 同一 Provider 可并发调用 `Generate`，调用方注入的 `http.Client`/Transport 必须符合标准库的并发用法；
- 不保存 Request、Context、响应体或单次流累积器到 Provider 全局状态；
- API Key 只保存在 Provider 内存配置中并写入 Authorization Header，不进入 `model.Request`、错误、日志或测试输出；
- 对 BaseURL、Header、模型 ID、MIME type、Tool Schema 和流数据按协议边界校验；
- HTTP 错误体和 SSE 事件都有大小上限；
- 默认不记录 Prompt、图片 Data、Tool Arguments、API Key 或完整 API 错误体。

## 12. 实施顺序

用户确认本方案后，按以下顺序实现：

1. 新增 `provider/openai/error.go` 与 `provider.go`，完成公开 API、配置快照和模型目录校验；
2. 新增 `request.go`，完成消息、图片、Tool Spec/Call 和请求参数映射；
3. 新增 `stream.go`，完成有界 SSE 读取、增量累积、Usage 与 finish reason 映射；
4. 补充自定义 `http.RoundTripper` 协议测试、公开 API 示例和资源关闭测试；测试不监听本地端口，可在网络受限的沙箱中运行；
5. 对新增 Go 文件执行 `gofmt`；
6. 在 `acore` 模块执行相关测试、全量测试、`go vet` 和新 Provider 包竞态检测。

## 13. 测试与验证计划

单元和协议测试至少覆盖：

1. 缺少 API Key、Models、Model ID，重复 Model ID，Provider/API 不匹配和非法 BaseURL；
2. Config/Header/Models/InputModalities 构建后快照，`Models()` 返回深拷贝；
3. 默认 URL、自定义 BaseURL、Authorization、Content-Type、Accept 及自定义 Header 优先级；
4. developer prompt、User/Assistant/Tool 文本、URL/base64 图片、Tool Spec、Tool Call、Temperature、MaxTokens 和 ReasoningLevel 请求映射；
5. 未知 Role、Thinking、错误位置的图片/Tool Call、缺失 ToolCallID、非法 Arguments 和非法图片数据；
6. 纯文本流的 Start/ContentStart/Delta/ContentEnd/Done 顺序；
7. 同一轮多个增量 Tool Call 的索引、ID、名称、Arguments 和最终顺序；
8. Usage 全字段和全部 finish reason 映射；
9. OpenAI 结构化 API 错误、非 JSON 错误、request ID、错误体上限和错误链；
10. 非 SSE 2xx 响应、损坏 JSON、过大 SSE、缺失 finish reason、非法 Tool Arguments、未收到 `[DONE]` 和静默 EOF；
11. 请求前取消、建流期间取消、迭代期间取消与 deadline；
12. 正常完成、流错误和调用方提前停止迭代均关闭 response body；
13. 同一 Provider 并发生成无竞态。

验证命令：

```bash
gofmt -w provider/openai/*.go
go test ./provider/openai
go test ./...
go vet ./...
go test -race ./provider/openai
```

测试不访问真实 OpenAI 网络，不读取真实 API Key。实现完成后可由应用所有者使用自己的凭证单独进行手工端到端验证，不将其作为可重复单元测试。

## 14. 验收标准

- 仓库外调用方可通过公开导入路径创建 OpenAI Provider；
- Provider 实现 `model.Provider`，可通过 `model.Bind` 或 `ProviderRegistry` 构建 `model.LLM`；
- 文本、用户图片、Function Tool Schema、Assistant Tool Call 和 Tool Result 可完整往返映射；
- 流顺序、增量 Tool Arguments、Usage、finish reason、错误和 Context 语义符合 `model.Stream` 契约；
- 配置、Header、模型目录和输入数据没有反向修改；
- 所有响应体在正常、错误和早停路径都可靠关闭；
- 没有引入第三方依赖，没有修改 `model`、`tool` 或 `event` 公开行为；
- 新包单元测试、`acore` 全量测试、`go vet` 和 Provider 竞态检测通过。

## 15. 兼容性、风险与后续演进

### 15.1 公开 API 兼容性

`provider/openai` 是新增公开包，没有现行公开 API 需要兼容。已删除的 `internal/provider/openai` 无法被外部导入，不为它增加类型别名或兼容转发层。

一旦导出 `Config`、`Provider`、常量、稳定错误和 `APIError`，就形成模块对外契约；首版因此只导出外部组装、调用和错误分类确实需要的字段。

### 15.2 已知风险

- Chat Completions 仍受 OpenAI 支持，但官方对新项目推荐 Responses；本方案优先保证当前核心协议正确，后续仍需演进 Responses 支持；
- 不同 OpenAI 模型对 Temperature、Reasoning Effort、Developer Message、图片和 Tool 的支持不完全一致；Provider 不通过模型名猜测能力，服务端依然可返回参数不兼容错误；
- `model.ToolSpec` 暂无 strict 字段，Function Calling 为 best effort；Tool System 仍必须不信任模型参数并重新校验；
- 流协议是外部输入，需对事件大小、JSON、Tool Call 完整性和静默 EOF 进行严格校验；
- 公开 `Headers` 和 `BaseURL` 增加了可配置面，必须防止它们覆盖鉴权或绕过 URL 校验。

### 15.3 Responses API 后续演进前置条件

在增加 Responses Provider 前，先对 `model` 回放协议形成独立方案，至少解决：

1. 如何在 Provider 无关消息中保留可选的 Provider 私有回放数据；
2. 如何保留 Responses output item 的 ID、type、`phase` 和顺序；
3. reasoning summary、encrypted reasoning content 和普通可见 Thinking 的边界；
4. 使用完整手工上下文还是 `previous_response_id`/Conversation；
5. 状态化 API 与 Agent Session、Checkpoint、租户隔离及数据保留策略的关系；
6. Responses 内建工具与本地 `tool.Service` 的职责边界。

本方案只授权确认后实现首版 Chat Completions Provider，不同时修改 `model` 回放协议或实现 Responses API。
