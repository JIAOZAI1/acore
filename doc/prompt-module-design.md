# Prompt 模块设计方案

> 实施状态（2026-08-25）：方案已按本文边界实现。当前 `acore/prompt` 已提供 `Renderer`、`RendererFunc`、`Static` 和严格 `Template`；`agent.Builder` 已支持可选 `UsePrompt`，`agent.Request` 已支持 `PromptValues`，现有 `SetSystemPrompt` 保留为静态 Prompt 便捷入口。

## 1. 背景与结论

方案实施前，`acore` 已有 Provider 无关的模型协议、可替换的 Agent 运行策略、Tool Service、Event Bus 和 Session Service，但 Prompt 仍只是 `agent.Builder.SetSystemPrompt(string)` 接收的固定字符串：`configuredAgent` 在 Build 时保存该字符串，每次 Run 再通过 `RunInput.SystemPrompt` 交给 `SingleTurnStrategy` 或 `ToolLoopStrategy`，最终写入 `model.Context.SystemPrompt`。

这种实现适合固定指令，但无法在不修改 Agent 核心流程的情况下完成以下需求：

- 按一次 Run 的显式变量生成 System Prompt；
- 在静态 Prompt 和模板 Prompt 之间替换实现；
- 由模块外提供自定义 Prompt 生成逻辑；
- 对缺失变量、并发复用、数据所有权和渲染错误形成统一契约。

本方案收敛为首个 Prompt 里程碑：

1. 在 `acore` 新增公开包 `github.com/JIAOZAI1/acore/prompt`；
2. 以最小公开接口 `Renderer` 表达“根据一次 Run 的显式输入生成 System Prompt”；
3. 提供不可变、并发安全的 `Static` 和基于标准库 `text/template` 的严格 `Template` 实现；
4. Agent Builder 新增可选的 `UsePrompt` 组件入口，保留 `SetSystemPrompt` 作为静态 Prompt 的兼容便捷方法；
5. `agent.Request` 新增 Run 级 `PromptValues`，由 Agent 在策略执行前完成快照并渲染一次；
6. `RunInput.SystemPrompt` 和现有策略契约保持不变，Prompt 不进入 Session 历史，也不在 Tool Loop 每轮重新渲染；
7. 首版不引入 Prompt 注册表、分区覆盖、文件发现、热更新、Prompt Hub、消息模板或上下文窗口治理。

Prompt 是 Agent 的一个可替换组装组件，但不是全局服务定位器，也不负责加载应用配置、读取文件、发现工具或管理会话。

## 2. 目标与非目标

### 2.1 目标

1. 模块外调用方可以实现、注入和替换 Prompt Renderer，不依赖 `internal` 包；
2. 固定字符串用法保持简单，并兼容当前 `SetSystemPrompt` 行为；
3. 模板在启动期完成解析，缺失变量在运行期明确失败，不把 `<no value>` 静默发送给模型；
4. 应用级默认变量与 Run 级变量有明确覆盖规则；
5. 每次 Run 只生成一个稳定的 System Prompt，Tool Loop 的所有模型轮次复用同一结果；
6. 调用方变量、模板默认变量和 Agent 内部状态之间不共享可变 map；
7. 建造后的 Agent 和内置 Renderer 可安全并发 Run；
8. Prompt 错误、Context 取消和策略错误有清晰且可用 `errors.Is` 判断的边界；
9. Prompt 内容、变量和值默认不进入错误、日志、事件、Result 或 Session。

### 2.2 非目标

首个里程碑不包含：

- 生成或改写 User、Assistant、Tool Message；
- few-shot 消息模板、历史拼接、Session 加载或提交；
- Token 估算、上下文窗口裁剪、摘要、压缩或 Prompt 自动优化；
- Tool Spec 格式化、工具选择或 Tool Loop 控制指令的自动注入；
- RAG、长期记忆、用户画像、工作区指令或文件内容加载；
- Prompt Section 注册表、优先级、全局/Agent Scope 覆盖、动态增删或热更新；
- Prompt 版本仓库、远程 Prompt Hub、A/B 实验、灰度或持久化；
- 自定义模板函数、脚本执行、反射式对象访问或第三方模板引擎；
- 自动转义或“防 Prompt Injection”承诺；
- 将 Prompt 内容作为默认可观测事件或日志字段。

这些能力涉及不同的所有权、信任等级、生命周期或预算语义，应在实际需求出现后分别设计，不扩张首版稳定契约。

## 3. 当前边界与问题定位

当前数据流为：

```text
agent.Builder.SetSystemPrompt(string)
              │
              ▼ Build
       configuredAgent.systemPrompt
              │
              ▼ Run
       RunInput.SystemPrompt
              │
        ┌─────┴─────────┐
        ▼               ▼
SingleTurnStrategy  ToolLoopStrategy
        │               │ 每个模型轮次复用
        └──────┬────────┘
               ▼
    model.Context.SystemPrompt
```

职责判断：

- `model.Context` 表示一次模型生成的完整输入，只承载已经生成好的 System Prompt，不负责模板；
- Agent Builder 是共享组件和配置的统一组装边界，适合接收 Prompt Renderer；
- `configuredAgent` 已负责 Request 校验、配置合并、输入快照和构造 `RunInput`，适合执行“一次 Run 一次渲染”；
- `RunStrategy` 负责运行算法，不应为相同 Prompt 模板重复实现渲染；
- Session 只保存可回放的对话消息，不保存应用指令和 Run 级 Prompt 变量；
- Tool Service 的 Tool Spec 已通过 `model.Context.Tools` 进入模型请求，Prompt 包不应再依赖 Tool Service 并生成第二份工具目录。

因此，Prompt 模块应位于 `model` 之前、运行策略之外，并只输出最终 System Prompt 字符串。

## 4. 模块与依赖边界

实际新增：

```text
acore/prompt/
├── prompt.go          # Renderer、RendererFunc、Input、Values 和稳定错误
├── static.go          # 不可变静态 Renderer
├── template.go        # 严格 text/template Renderer
├── prompt_test.go     # 公开契约、错误、Context 和函数适配器
├── template_test.go   # 模板、变量、快照和并发测试
└── example_test.go    # 模块外静态/模板/自定义 Renderer 示例
```

实际调整：

```text
acore/agent/
├── agent.go           # Request.PromptValues 和 Prompt 相关稳定错误
├── builder.go         # UsePrompt、SetSystemPrompt 兼容入口和组件冻结
├── run.go             # Run 级 Prompt 快照、渲染和错误边界
├── clone.go           # Prompt Values 复制
├── prompt_test.go     # 组装、渲染时机、错误、快照、Tool Loop 和并发
└── prompt_example_test.go # Agent 组装模板 Prompt 示例
```

依赖方向：

```text
                Go 标准库
                    ▲
                    │
              acore/prompt
                    ▲
                    │ Renderer
PromptValues ──► acore/agent ──► acore/model
                       │
                       ├──► RunStrategy
                       ├──► session
                       └──► tool
```

约束：

- `prompt` 首版只依赖 Go 标准库，不依赖 `agent`、`model`、`tool`、`session`、Provider 或应用包；
- `agent` 依赖公开 `prompt` 契约，`prompt` 不反向依赖 Agent；
- Prompt Renderer 不通过 `context.Context.Value` 获取普通变量；
- Prompt Renderer 不持有 LLM、RunStrategy、Session Service、Tool Service 或完整 Agent Request；
- 应用级 Prompt 内容和变量来源由 `agent/` 应用或其他装配层负责，`acore/prompt` 不读取环境变量或文件；
- Builder 保存 Renderer 行为引用，不把 Builder 退化为按名称查找 Renderer 的注册中心。

## 5. Prompt 公开契约

建议 API：

```go
package prompt

import "context"

// Values contains explicit string variables for one render.
type Values map[string]string

// Input is the per-render snapshot passed to a Renderer. Implementations must
// treat it as read-only. Code constructing Input should use keyed fields.
type Input struct {
    Values Values
}

// Renderer generates one complete system prompt.
// Implementations must be safe for concurrent calls and must not retain Input.
type Renderer interface {
    Render(context.Context, Input) (string, error)
}

// RendererFunc adapts a function to Renderer.
type RendererFunc func(context.Context, Input) (string, error)

func (f RendererFunc) Render(context.Context, Input) (string, error)

// Static renders one fixed system prompt.
type Static struct {
    // 字段不导出。
}

func NewStatic(text string) *Static
func (s *Static) Render(context.Context, Input) (string, error)

// TemplateConfig configures one template compiled during application setup.
type TemplateConfig struct {
    Name     string
    Text     string
    Defaults Values
}

// Template is an immutable strict text template.
type Template struct {
    // 字段不导出。
}

func NewTemplate(TemplateConfig) (*Template, error)
func (t *Template) Render(context.Context, Input) (string, error)
```

稳定错误：

```go
var (
    ErrInvalidContext  = errors.New("prompt: invalid context")
    ErrNilRenderer     = errors.New("prompt: nil renderer")
    ErrInvalidTemplate = errors.New("prompt: invalid template")
    ErrRender           = errors.New("prompt: render")
)
```

### 5.1 可导出性边界

- `Values`、`Input`、`Renderer`、`RendererFunc`、`Static`、`TemplateConfig`、`Template`、构造函数和稳定错误全部导出；
- `Renderer` 的方法只使用标准库和 `prompt` 包的公开类型，模块外可完整实现；
- 模板解析树、默认变量快照、变量合并和缓冲区保持非导出；
- 不导出可修改模板解析树、替换默认变量或改变已构建 Renderer 的 setter；
- 不为具体业务定义变量名；变量名是模板与应用之间的局部契约，不进入全局注册表。

### 5.2 为什么返回字符串而不是 Message 列表

当前 `model` 已明确区分 `Context.SystemPrompt` 与可回放 `Messages`。首版 Prompt 需求只扩展 System Prompt，返回字符串有以下好处：

- 不改变消息角色、历史顺序和 Session 提交语义；
- 不需要定义模板消息与会话历史、用户输入之间的插入顺序；
- Tool Loop 可直接复用同一字符串；
- Prompt 包不需要依赖 `model`；
- 避免为了未来 few-shot 场景提前扩大公开协议。

如果后续出现明确的消息模板需求，应单独设计 Message Renderer 或更高层 Context Assembler，不在 `Renderer` 中追加隐式消息行为。

### 5.3 为什么使用 string Values 而不是 map[string]any

首版变量只允许字符串：

- map 和字符串都可以完整复制，数据所有权明确；
- 不会把函数、channel、指针、带方法对象或不可复制状态暴露给模板反射执行；
- 调用方可以自行使用稳定格式将列表或结构编码为字符串；
- 自定义 Renderer 仍可通过构造函数显式持有类型化应用配置，不需要把所有依赖塞进变量包。

若后续反复出现列表、布尔值或结构化片段需求，应基于实际用例增加受限值类型，而不是直接放宽为任意 `any`。

## 6. 内置 Renderer 语义

### 6.1 Static

`NewStatic(text)` 返回不可变 Renderer：

- 原样返回 `text`，不裁剪空白、不追加换行、不解析占位符；
- 空字符串合法，等价于没有 System Prompt；
- 忽略 `Input.Values`；
- Render 前检查 nil/canceled Context；
- nil `*Static` 调用返回 `ErrNilRenderer`；
- 字符串不可变，因此同一实例可并发复用。

### 6.2 Template

`NewTemplate` 使用标准库 `text/template` 在启动期解析模板：

```go
renderer, err := prompt.NewTemplate(prompt.TemplateConfig{
    Name: "support-agent",
    Text: `You are a support agent for {{.product}}.
Reply in {{.language}}.`,
    Defaults: prompt.Values{
        "language": "zh-CN",
    },
})
```

构建语义：

1. `Name` 必须非空，用于解析和错误定位；
2. `Text` 可以为空；
3. `Defaults` 在构建时复制，调用方之后修改原 map 不影响 Template；
4. 使用 `Option("missingkey=error")`，使 `{{.name}}` 形式的缺失变量立即失败；
5. 不接受调用方自定义模板函数；保留标准语法和内置函数，但将 `index` 覆盖为只接受 `(Values, string)` 的严格版本，使 `{{index . "name-with-dash"}}` 在 key 缺失时同样失败；
6. 解析失败返回同时匹配 `ErrInvalidTemplate` 的错误，并保留标准库错误链；
7. 成功构建后 Template 不再修改，允许并发 Execute。

渲染语义：

1. Render 前检查 Context；
2. 复制 Defaults，再按 key 合并 `Input.Values`；
3. Run 级值覆盖同名 Default，包括使用空字符串显式覆盖；
4. 通过 `{{.name}}` 或严格 `index` 访问的变量缺失、`index` 参数类型不符或其他执行失败，均返回同时匹配 `ErrRender` 的错误；
5. 执行失败时丢弃缓冲区中的部分输出，不返回半成品 Prompt；
6. 执行结束后再次检查 Context，若已取消则优先返回 `ctx.Err()`；
7. 输出保持模板的原始空白，不自动 trim、escape、join 或格式化；
8. 额外但未引用的变量合法并被忽略。

`text/template.Execute` 本身不能在模板执行中途被 Context 强制中断。首版模板来源属于受信任的启动期配置，变量仅为字符串，且不开放自定义函数；因此在执行前后检查 Context。不得把不受信任的文本当作模板源码动态解析。

### 6.3 RendererFunc 与外部实现

`RendererFunc` 用于轻量适配：

```go
renderer := prompt.RendererFunc(func(ctx context.Context, input prompt.Input) (string, error) {
    if err := ctx.Err(); err != nil {
        return "", err
    }
    return buildApplicationPrompt(input.Values), nil
})
```

- nil `RendererFunc` 返回 `ErrNilRenderer`；
- 外部实现必须并发安全、响应 Context、不得保存 Context 或一次 Render 的 Input；
- 外部实现可以通过构造函数接收明确依赖，但不得依赖 Agent Builder 的内部状态；
- Renderer 返回非 nil error 时，调用方忽略同时返回的字符串；
- Prompt 包不恢复 Renderer panic；panic 表示实现破坏编程不变量。

## 7. Agent 集成

### 7.1 Request

`agent.Request` 增加：

```go
type Request struct {
    Messages     []model.Message `json:"messages,omitempty"`
    Session      *SessionInput   `json:"session,omitempty"`
    Options      ModelOptions    `json:"options,omitempty"`
    PromptValues prompt.Values   `json:"promptValues,omitempty"`
}
```

语义：

- `PromptValues` 是调用方为本次 Run 显式提供的受信任 Prompt 变量；
- 它不参与 Messages/Session 二选一校验；
- nil 和空 map 等价；
- Agent 先为规范化 Request 复制 map，再为 Renderer 单独复制一份，Renderer 与调用方及策略输入都不共享可变状态；
- `RunInput.Request.PromptValues` 仍携带独立快照，便于自定义策略观察完整的规范化请求，但内置策略不会读取它；
- PromptValues 不写入 Session，也不进入 `Result.GeneratedMessages`。

`Request` 是公开结构体；新增字段对使用具名字段的代码源码兼容，但会使模块外未遵循 Go 公共 API 惯例、使用非具名复合字面量的代码需要改为具名字段。实现时应在 Request 注释中明确要求使用具名字段。

### 7.2 Builder

Agent Builder 增加：

```go
func (b *Builder) UsePrompt(prompt.Renderer) error
```

并保留：

```go
func (b *Builder) SetSystemPrompt(string) error
```

Builder 语义：

1. Prompt Renderer 是可选组件；未配置时每次 Run 使用空 System Prompt；
2. `UsePrompt` 拒绝 nil 和 typed nil，返回 `ErrNilPromptRenderer`；
3. `UsePrompt` 和 `SetSystemPrompt` 共享同一个单实例配置槽，任意顺序重复配置均返回包装 `ErrConfigAlreadySet` 的错误；
4. `SetSystemPrompt(text)` 内部等价于配置 `prompt.NewStatic(text)`，保持空字符串合法及原样传递行为；
5. 成功 Build 后，`UsePrompt` 和 `SetSystemPrompt` 均返回现有 `ErrBuilderBuilt`；
6. Build 不复制行为组件本身，只保存 Renderer 引用；Renderer 必须并发安全且在注入后不再修改；
7. Builder 不识别 `Static`、`Template` 或自定义 Renderer 的具体类型，不按类型执行分支；
8. Builder 不提供全局默认 Renderer，也不从环境或文件隐式加载 Prompt。

Agent 新增稳定错误：

```go
var (
    ErrNilPromptRenderer = errors.New("agent: nil prompt renderer")
    ErrRenderPrompt      = errors.New("agent: render prompt")
)
```

继续复用 `ErrConfigAlreadySet` 表达单实例 Prompt 槽冲突，避免仅为重复配置增加第二套等价错误，并保持当前重复 `SetSystemPrompt` 的 `errors.Is` 行为。

### 7.3 Run 时机与数据流

`configuredAgent.Run` 调整为：

```text
Agent.Run(ctx, Request)
   │
   ├─校验 Context 和 Messages/Session 输入形态
   ├─合并并校验 ModelOptions
   ├─深拷贝 Messages、SessionInput、Options、PromptValues
   ├─Renderer.Render(ctx, prompt.Input{Values: clone(PromptValues)})  # 恰好一次
   │      └─失败：Run 直接返回，不调用 RunStrategy
   ├─构造 RunInput{
   │      LLM,
   │      SystemPrompt: rendered,
   │      Request: normalizedSnapshot,
   │  }
   └─RunStrategy.Run(ctx, RunInput)
          ├─SingleTurn：一次模型请求
          └─ToolLoop：所有模型轮次复用 rendered
```

具体规则：

1. 基础 Request 和 ModelOptions 校验先于渲染，明显无效请求不调用 Renderer；
2. Renderer 先于 RunStrategy 调用，渲染失败属于 Agent 建流前错误；
3. Renderer 返回后若 Context 已结束，优先返回 `ctx.Err()`；
4. 非 Context 渲染错误包装为 `fmt.Errorf("%w: %w", ErrRenderPrompt, err)`，同时保留 `prompt.ErrRender` 等底层错误链；
5. 渲染成功后才构造 `RunInput.SystemPrompt`；
6. 一次 Run 不重新渲染，保证 Tool Loop 各轮 System Prompt 前缀稳定，也有利于 Provider Prompt Cache；
7. 直接调用 `SingleTurnStrategy.Run` 或 `ToolLoopStrategy.Run` 的模块外代码仍直接提供已经解析好的 `RunInput.SystemPrompt`，策略不依赖 Prompt 包；
8. 自定义策略不应再次解释 `PromptValues`，除非它明确绕过 Agent 的标准渲染语义并自行定义扩展行为。

### 7.4 Session 与 Tool Loop

- Session Load 仍在具体策略中执行；Prompt Renderer 不读取历史，也不会因 Session 内容变化隐式改变；
- Session Append 仍只提交本次输入 Message 和 `GeneratedMessages`；
- PromptValues、模板 Defaults 和渲染结果均不持久化；
- Tool Loop 每轮继续使用相同的 `RunInput.SystemPrompt` 和 Tool Specs；
- Tool Result、模型输出、模型轮次等运行中数据不会回流到 Renderer；
- 如果未来出现“每个模型轮次根据工具结果重建 Prompt”的真实需求，应作为具体 RunStrategy 的显式依赖或新的 Context Assembler 设计，不能偷偷改变首版一次 Run 一次渲染的不变量。

## 8. 数据所有权、并发与生命周期

### 8.1 数据所有权

- `TemplateConfig.Defaults` 在 `NewTemplate` 时复制；
- Agent 在 `Run` 入口为规范化 Request 复制 `Request.PromptValues`，调用 Renderer 时再复制一次；
- Template 每次 Render 创建新的合并 map 和输出 buffer；
- Template 不把 Defaults map 或解析树暴露给调用方；
- Renderer 接收的一次 Input 只在该次调用有效，不得保留；
- `RunInput.SystemPrompt` 是不可变字符串，可以安全传递给策略和多个模型轮次；
- Agent 不缓存某次 Render 的输入或输出到下一次 Run。

### 8.2 并发

- `Static` 无可变状态；
- `Template` 构建后不再调用会修改解析树的方法，使用独立 buffer 执行；标准库解析后的 Template 可并发 Execute；
- 同一已构建 Agent 可以并发 Run，每次 Run 使用独立 PromptValues 快照和输出；
- 自定义 Renderer 和 RendererFunc 必须自行满足并发安全；
- Builder 仍只用于启动期单 goroutine 装配，不承诺并发安全。

### 8.3 生命周期

- Prompt Renderer 没有 `Close`；
- Renderer 不保存一次 Run 的 Context；
- Template 在启动期解析，建造后的 Agent 不支持原地替换或热更新；
- 需要切换 Prompt 时，由应用构建新的 Renderer 和 Agent，再按应用自己的生命周期替换引用。

## 9. 安全与可观测性

### 9.1 信任边界

PromptValues 会被插入 System Prompt，具有高于普通 User Message 的指令位置。因此：

- PromptValues 只应用于受信任的应用配置和经过校验的运行元数据；
- 用户原始输入、网页内容、检索文档、工具结果和工作区文件默认应保留在相应的 User/Tool/Context 消息边界，不应直接提升为 System Prompt；
- Prompt 模板不会自动防止 Prompt Injection，也不会根据变量来源自动加引号或围栏；
- 如业务确需嵌入不受信任内容，应用 Renderer 必须定义清晰的标记、转义、长度限制和信任说明；这仍不能视为完整安全隔离；
- API Key、Token、密码、私钥等凭据不得作为 PromptValues，因为渲染结果会发送给模型 Provider。

### 9.2 错误与日志

- Agent 错误只包含操作名和底层错误链，不包含模板全文、变量值或渲染结果；
- 模板错误可以包含 Template Name 和缺失变量名，以便定位，但不得拼接变量值；
- 首版不发布 PromptRendered 事件，不把 Prompt 内容加入 `agent.Event`；
- 如未来增加观测，只允许默认记录 Renderer 类型、模板逻辑名称、耗时、结果字节数和成功/失败等低敏感元数据，完整内容必须显式开启并经过脱敏设计。

### 9.3 大小与预算

首版不设置 Prompt 专属字节上限，也不估算 Token，原因是当前 `model.Message` 同样没有统一上下文预算，单独限制 System Prompt 会形成不完整治理。风险是调用方可能提供过大的变量值。实现和文档需明确：

- 应用应在请求边界校验 PromptValues 长度；
- Renderer 可以按业务需要返回自定义“输出过大”错误；
- 已实现的 [Context Window 模块](context-window-module-design.md)统一预算 System Prompt、Messages、Tool Specs 和预留输出 Token；Prompt Renderer 本身仍不承担窗口治理；
- 不在首版随意选择一个看似安全但缺少模型依据的固定上限。

## 10. 兼容性与迁移

### 10.1 保持不变

- `Agent.Run`、`RunStrategy.Run`、`RunInput.SystemPrompt`、Agent Stream 和 Result 契约不变；
- `SetSystemPrompt("...")` 仍可使用，内容仍原样进入每一轮 `model.Context.SystemPrompt`；
- 不配置 Prompt 时仍使用空 System Prompt；
- SingleTurn、ToolLoop、Session、Tool、Event 和 Provider 的运行语义不变；
- 模块外自定义 RunStrategy 不需要导入 `prompt` 包。

### 10.2 新用法

```go
renderer, err := prompt.NewTemplate(prompt.TemplateConfig{
    Name: "assistant",
    Text: "You are the {{.role}} assistant. Reply in {{.language}}.",
    Defaults: prompt.Values{
        "language": "zh-CN",
    },
})
if err != nil {
    return err
}

builder := agent.NewBuilder()
if err := builder.UseLLM(llm); err != nil {
    return err
}
if err := builder.UseRunStrategy(agent.NewSingleTurnStrategy()); err != nil {
    return err
}
if err := builder.UsePrompt(renderer); err != nil {
    return err
}
value, err := builder.Build()
if err != nil {
    return err
}

result, err := agent.Complete(ctx, value, agent.Request{
    Messages: userMessages,
    PromptValues: prompt.Values{
        "role": "billing support",
    },
})
```

### 10.3 有意限制

- `UsePrompt` 与 `SetSystemPrompt` 不能同时配置，避免不明确的 replace/append 顺序；
- 不提供 `AppendSystemPrompt`，需要组合时由一个 Renderer 明确产生完整结果；
- Prompt Renderer 不自动获得 LLM、Tool Specs、Session History 或当前时间；应用必须显式提供稳定变量或实现自己的 Renderer；
- Prompt 一次 Run 只渲染一次，不随 Tool Loop 状态动态变化。

## 11. 未采用方案

### 11.1 在 model.Context 中实现模板

`model.Context` 是 Provider 无关的一次生成数据，不应同时承担模板解析和运行时变量加载。这样还会迫使 Provider 理解模板，破坏协议层职责。

### 11.2 每个 RunStrategy 自行渲染

会在 SingleTurn、ToolLoop 和未来策略中重复配置、校验和错误语义，也会使相同 Agent 的 Prompt 行为随策略实现漂移。

### 11.3 将 Renderer 注入每个具体 Strategy Builder

Prompt 是当前所有模型驱动策略共享的 Agent 级配置。分别注入会重复组装，并使自定义策略难以复用统一入口。只有未来出现明确的“每轮动态 Prompt”策略时，才由该策略额外接收自己的组件。

### 11.4 使用 context.Value 传变量

这会隐藏依赖、缺少序列化和复制边界，并容易与取消 Context 生命周期混淆。PromptValues 必须作为 Request 的显式字段传入。

### 11.5 直接使用 map[string]any

虽然表达力更高，但会引入不可复制值、反射方法调用、并发可变对象和不明确序列化语义。首版字符串变量已经覆盖固定配置和显式片段插值。

### 11.6 首版提供有序 Section 注册表

Section 名称、覆盖、优先级、作用域、动态生命周期和冲突处理都会扩大稳定 API。当前只有一个 Agent Builder Prompt 槽，没有多插件动态贡献的真实需求；外部可先通过自定义 Renderer 完成组合。

### 11.7 只在 agent 包增加模板函数

这会让模板成为 Agent 私有实现，模块外无法以稳定契约替换，也不利于在其他模型编排场景独立测试和复用。

## 12. 参考项目与本地化取舍

本方案于 2026-08-25 查阅以下上游主分支内容，只借鉴职责和契约，不复制代码：

1. [CloudWeGo Eino `prompt.ChatTemplate`](https://github.com/cloudwego/eino/blob/main/components/prompt/interface.go)（`main`，Apache-2.0）将 Prompt 作为可独立组合的组件，并通过 Context 和显式变量格式化消息。本方案采用“Prompt 是独立可替换组件”和显式 Context；但当前 `acore` 已单独建模 System Prompt，首版不采用 `map[string]any` 或 Message 列表输出。
2. [DeepSeek Harness System Prompt Assembly](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/core/system-prompt/README.md)（`master`，MIT，仓库声明处于 developer preview）展示了严格变量、确定性组装以及一次模型步骤的 Prompt 生成。本方案采用缺失变量失败和确定性渲染；但当前项目没有插件作用域和动态贡献生命周期，因此不引入全局/Scope 注册表、有序 Section 或 waterfall。
3. [pi `buildSystemPrompt`](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/core/system-prompt.ts)（`main`，MIT）由应用层结合工具、工作目录、项目上下文和 Skills 构造具体编码 Agent Prompt。本方案据此把这些应用知识保留在 `agent/` 应用或自定义 Renderer 中，不固化到公开 `acore/prompt` 包。

本地化原则：`acore` 提供最小稳定契约和通用实现，`agent` 应用负责 Prompt 内容、变量来源及场景化组合；现有 `model.Context.SystemPrompt`、RunStrategy 和 Session 边界优先于上游项目的数据模型。

## 13. 测试与验证计划

### 13.1 prompt 包

至少覆盖：

1. `Static` 原样返回文本，空文本合法；
2. Static、Template 和 RendererFunc 的 nil receiver/typed nil 行为；
3. nil Context、调用前取消和执行后取消；
4. 模板启动期解析错误匹配 `ErrInvalidTemplate`；
5. Defaults、Run Values、同名覆盖和空字符串覆盖；
6. 直接字段访问和严格 `index` 的缺失变量均匹配 `ErrRender`，额外变量被忽略；
7. 执行错误不返回部分 Prompt；
8. 模板不自动裁剪或转义；
9. 构建后修改 Defaults 原 map 不影响结果；
10. Renderer 修改一次 Input map 不影响调用方和其他 Run；
11. 同一 Static/Template 并发 Render 结果隔离；
12. 模块外自定义类型可实现 Renderer，RendererFunc 可直接适配函数。

### 13.2 agent 集成

至少覆盖：

1. Builder 不配置 Prompt 时仍构建成功并传递空 System Prompt；
2. `UsePrompt` 拒绝 nil/typed nil；
3. `UsePrompt`、`SetSystemPrompt` 的互斥、重复和 Build 后冻结；
4. 现有 `SetSystemPrompt` 测试行为不变；
5. PromptValues 正确传入 Renderer，模板结果正确进入 `RunInput.SystemPrompt`；
6. 调用方在 Run 返回后修改原 map 不影响已建立的策略输入；
7. Renderer 修改收到的 map 不影响调用方 Request、策略输入和并发 Run；
8. 每次 Run 恰好调用 Renderer 一次，Tool Loop 多模型轮次不重复渲染；
9. 渲染失败时不调用 RunStrategy 或 LLM，错误同时匹配 `ErrRenderPrompt` 和底层错误；
10. Renderer 返回后发生 Context 取消时优先返回 `ctx.Err()`；
11. Session Run 不将 PromptValues 或渲染结果写入 Session；
12. 同一 Agent 并发 Run 的变量和输出互不污染。

实现后在 `acore` 模块目录执行：

```bash
gofmt -w prompt/*.go agent/agent.go agent/builder.go agent/run.go agent/clone.go \
    agent/builder_test.go agent/agent_test.go agent/example_test.go
go test ./prompt ./agent
go test ./...
go vet ./...
go test -race ./prompt ./agent
```

测试使用 Static、Template、fake Renderer、fake RunStrategy、fake LLM 和 Memory Session，不访问网络。

## 14. 实施顺序

实际按以下最小顺序实施：

1. 新增 `prompt` 公开契约、Static、Template、错误和单元测试；
2. 在 Agent Request 增加 PromptValues 及复制边界；
3. 在 Agent Builder 增加 Renderer 槽和 `UsePrompt`，把 `SetSystemPrompt` 改为 Static 便捷入口；
4. 在 `configuredAgent.Run` 增加一次性渲染和错误边界；
5. 补充 Agent 集成、Session 不持久化和并发测试；
6. 更新相关示例以及当前 Agent/模块路线文档中的 Prompt 状态；
7. 执行格式化、最小测试、全量测试、vet 和 race 检查。

若实现中发现必须让 Renderer 读取 Session History、Tool Catalog 或每轮运行状态，应先停止实现并更新本设计，因为这会改变模块依赖和“一次 Run 一次渲染”的核心边界。

## 15. 验收标准

- 模块外可以只依赖公开 API 实现并注入自定义 Prompt Renderer；
- Static 和 Template 均不可变、可并发复用，并具有明确的 Context 和错误语义；
- 模板缺失变量失败，不产生 `<no value>` Prompt；
- `SetSystemPrompt` 的既有行为保持不变；
- Agent 每次 Run 恰好渲染一次，所有策略收到相同的最终 System Prompt；
- PromptValues、Defaults、渲染输出和并发 Run 之间没有可变数据泄漏；
- Prompt 不读取或持久化 Session，不复制 Tool Catalog，不侵入模型 Provider；
- 渲染错误阻止策略和模型调用，并保留稳定错误链；
- 不引入第三方依赖、全局状态、文件发现或动态注册表；
- 相关单元测试、全量测试、vet 和 race 检查通过，或如实记录环境阻碍。

## 16. 实施与验证结果

2026-08-25 已完成以下实现：

- 新增 `acore/prompt` 的公开 Renderer 契约、Static、严格 Template、函数适配器、稳定错误、单元测试和外部示例；
- Agent Request、Builder、Run 和复制边界已接入 Prompt Renderer 与 PromptValues；
- 新增静态兼容、模板渲染、Builder 冲突/冻结、错误链、Context 优先、Tool Loop 单次渲染、Session 不持久化和并发 Run 测试；
- 未增加第三方依赖，也未改变 RunStrategy、模型、工具、Session 或 Provider 方法签名。

实际检查：

```text
go test ./prompt                                      通过
go test ./agent                                       通过
GOCACHE=/tmp/acore-prompt-go-cache go test -count=1 ./... 通过
GOCACHE=/tmp/acore-prompt-go-cache go vet ./...       通过
```

直接执行 `go test ./...` 时，默认 `/home/lam/.cache/go-build` 为只读文件系统；切换到任务专用 `/tmp` 缓存后全量测试通过。

Race 检查未能在当前环境执行：默认环境关闭 CGO；以 `CGO_ENABLED=1` 重试后，`go test -race ./prompt ./agent` 因系统未安装 `gcc` 在 `runtime/cgo` 构建阶段失败。该失败发生在竞态测试启动前，不代表发现数据竞争；需在安装 C 编译器的环境补跑。
