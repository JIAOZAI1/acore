# Agent 运行策略抽象重构方案

> 后续演进：本文档记录 RunStrategy 抽象里程碑。当前 Session Service 已由各具体策略 Builder 直接注入，并由建造后的策略实例持有；Agent Builder 和 `RunStrategy` 接口均不感知 Session。详见 [Session 模块设计方案](session-module-design.md)。

## 1. 背景与结论

重构前 `acore/agent.Builder.Build` 直接构造非导出的 `singleTurnAgent`。虽然对外返回的是 `Agent` 接口，但运行策略选择仍固化在 Builder 内部：增加 Tool Loop、多轮反思、规划执行或其他策略时，都必须修改 Builder 并不断扩大同一个具体 Agent。

本方案将结构调整为：

```text
Builder
  ├── model.LLM
  ├── RunStrategy
  ├── System Prompt
  └── ModelOptions
          │
          ▼ Build
    非导出 configuredAgent
          │ RunInput
          ▼
      RunStrategy
```

核心结论：

1. 新增可由模块外实现的公开 `RunStrategy` 接口；
2. Builder 通过 `UseRunStrategy` 显式持有并组装运行策略，不再选择具体策略；
3. `model.LLM` 与 `RunStrategy` 均为必填组件；
4. 当前单轮逻辑迁移为公开、可独立注入的 `SingleTurnStrategy`；
5. Builder 产出非导出的通用 `configuredAgent`，只负责输入规范化、边界复制、策略调用和 Agent Stream 契约保护；
6. 原有 `Agent`、`Request`、`Result`、`Event`、`Stream` 和 `Complete` 契约保持不变。

## 2. 目标与非目标

### 2.1 目标

1. 新增运行策略时不修改 Builder 和通用 Agent；
2. 模块外调用方可以实现并注入自定义策略；
3. 每次 Run 向策略传递完整、已校验、已合并默认值且与调用方隔离的输入快照；
4. 单轮策略保持当前事件顺序、结果映射、错误、取消和早停释放行为；
5. Builder 对策略执行 nil/typed-nil、重复配置、缺失组件和构建后冻结校验；
6. 建造后的 Agent 可并发 Run，所有单次运行状态仍位于策略的 Stream generator 内。

### 2.2 非目标

本次不实现：

- Tool Loop 或第二种业务运行策略；
- 策略注册表、按请求动态选策略或策略链；
- Strategy Builder、插件发现或反射构造；
- `tool.Service`、`event.Publisher`、Session 或 Checkpoint 注入；
- 放宽 LLM 必填约束；当前 Agent 仍定位为模型驱动 Agent；
- 修改模型、工具、事件或 Provider 包。

## 3. 公开契约

新增以下公开类型：

```go
// RunInput is the immutable per-run snapshot passed to a RunStrategy.
// Callers should use keyed fields when constructing it directly.
type RunInput struct {
    LLM          model.LLM
    SystemPrompt string
    Request      Request
}

// RunStrategy executes one normalized agent run.
type RunStrategy interface {
    Run(context.Context, RunInput) (Stream, error)
}

// SingleTurnStrategy executes exactly one model generation.
type SingleTurnStrategy struct {
    // 无导出状态。
}

func NewSingleTurnStrategy() *SingleTurnStrategy
func (s *SingleTurnStrategy) Run(context.Context, RunInput) (Stream, error)

func (b *Builder) UseRunStrategy(RunStrategy) error
```

新增稳定错误：

```go
var (
    ErrNilRunStrategy        = errors.New("agent: nil run strategy")
    ErrRunStrategyAlreadySet = errors.New("agent: run strategy already set")
    ErrMissingRunStrategy    = errors.New("agent: missing run strategy")
)
```

### 3.1 可导出性边界

- `RunStrategy`、`RunInput`、`SingleTurnStrategy`、构造函数、Builder 注入方法和稳定错误全部导出；
- `RunStrategy` 的方法只使用标准库和 `agent` 已导出类型，仓库外可完整实现；
- `SingleTurnStrategy` 作为可组合组件导出，不再把单轮策略隐藏为 Agent 的具体类型；
- Builder 产出的 `configuredAgent`、输入准备、策略流保护和复制函数保持非导出；
- `RunInput` 是运行时依赖容器，不定义 JSON 序列化契约；新增策略应使用具名字段读取或构造它。

## 4. 职责划分

### 4.1 Builder

Builder 只负责启动期组装：

1. `UseLLM` 注入模型；
2. `UseRunStrategy` 注入运行策略；
3. `SetSystemPrompt` 和 `SetModelOptions` 注入共享配置；
4. `Build` 校验组件、快照配置并构造 `configuredAgent`；
5. Builder 不判断策略类型，不使用类型断言选择分支，也不为单轮策略设置隐式默认值。

Builder 的策略语义：

- nil/typed-nil 返回 `ErrNilRunStrategy`；
- 第二次注入返回 `ErrRunStrategyAlreadySet`；
- Build 缺少策略返回 `ErrMissingRunStrategy`；
- 同时缺少 LLM 和策略时，先返回现有 `ErrMissingLLM`；
- 缺少组件导致的 Build 失败不冻结 Builder；
- 首次成功 Build 后，`UseRunStrategy` 也返回 `ErrBuilderBuilt`。

### 4.2 configuredAgent

通用 Agent 不包含具体运行算法，只执行以下固定边界逻辑：

1. 校验 nil/canceled Context；
2. 校验 Request 至少有一条 Message；
3. 将 Run 级 ModelOptions 与 Builder 默认值逐字段合并并校验；
4. 深拷贝 Message、Content、ToolCall.Arguments、Signature 和 Options；
5. 构造每次 Run 独立的 `RunInput`；
6. 调用注入的 `RunStrategy.Run`，保留策略错误链；
7. 拒绝 nil Stream；
8. 包装策略 Stream，保证 Context 错误优先、错误后终止、静默结束报告 `ErrUnexpectedStreamEnd`、RunDone 必须包含 Result，并对向调用方暴露的 Event/Result 再做快照。

通用 Agent 不生成模型事件、不计算 Usage/ToolCalls，也不判断何时继续模型轮次，这些属于具体策略。

### 4.3 RunStrategy

策略负责运行算法和完整 Agent 事件语义：

- 产生一次 `EventRunStart`；
- 产生零到多个模型、工具或未来扩展事件；
- 成功时产生唯一 `EventRunDone`；
- 运行期失败通过 Stream error 返回；
- 响应 Context 取消；
- 调用方提前停止时释放已建立的底层资源；
- 不修改或保存 `RunInput` 之外的 Agent 共享状态；
- 同一策略实例必须允许并发 Run，或在自身内部正确同步。

`RunInput.Request.Options` 已是 Builder 默认值与 Run 覆盖值合并后的最终值。策略不再读取 Builder，也不需要知道选项来源。

## 5. SingleTurnStrategy 迁移

当前 `singleTurnAgent` 拆分为：

```text
configuredAgent.Run
  └── 校验、默认值合并、输入快照
        └── SingleTurnStrategy.Run
              ├── 构建 model.Request
              ├── model.LLM.Generate
              ├── 包装 model.Event
              ├── 构建 agent.Result
              └── 管理取消与早停资源释放
```

`SingleTurnStrategy` 保留现有行为：

```text
EventRunStart
    → EventModel(ModelTurn=1, ModelEvent=<model.Event...>)
    → EventRunDone(Result=<agent.Result>)
```

- 模型 Done 缺少 Result 返回 `ErrInvalidModelDoneEvent`；
- 模型流静默结束返回 `ErrUnexpectedModelStreamEnd`；
- Tool Call 原样进入 Output 和 GeneratedMessages，只计数、不执行；
- `ModelTurns` 固定为 1；
- 模型事件与终端结果保持独立深拷贝；
- `SingleTurnStrategy.Run` 直接调用时也校验 Context、LLM、Messages 和 Options，不依赖只能由 Builder 调用的隐含前提。

## 6. 数据所有权与并发

- Builder 在配置时和 Build 时复制 ModelOptions 指针值；
- `configuredAgent` 每次 Run 创建新的 `RunInput`，不在 Agent 实例保存 Request、Context、Stream 或 Result；
- 策略收到的 Messages、Options 与调用方及 Builder 隔离，可以作为本次 Run 的私有输入使用；
- 通用 Agent 在策略输出边界再次复制 Event Block、模型 Result 和 Agent Result，外部修改不影响策略内部状态；
- LLM 和 RunStrategy 是共享行为组件，不被复制；两者必须支持并发调用；
- `SingleTurnStrategy` 无可变字段，可安全复用。

## 7. 兼容性与取舍

### 7.1 保持不变

- `Agent.Run` 签名不变；
- Request、Result、Event 和 Complete 不变；
- 单轮运行的事件、错误、计数和结果行为不变；
- 现有 LLM、Provider、Tool 和 Event Bus 不受影响。

### 7.2 有意变更

原调用：

```go
builder := agent.NewBuilder()
_ = builder.UseLLM(llm)
value, err := builder.Build()
```

调整为显式选择策略：

```go
builder := agent.NewBuilder()
_ = builder.UseLLM(llm)
_ = builder.UseRunStrategy(agent.NewSingleTurnStrategy())
value, err := builder.Build()
```

未注入策略时 Build 返回 `ErrMissingRunStrategy`。本包目前尚未发布，此时采用显式必填策略可以避免把 single-turn 继续固化成无法移除的默认行为。

### 7.3 未采用的方案

1. **Builder 默认使用单轮策略**：调用简单，但仍形成隐式策略选择，不符合显式组合目标；
2. **把 LLM 和配置预先构造进 Strategy**：会让每种策略重复定义组件组装，并削弱统一 Builder 的价值；
3. **Strategy.Build 返回 Agent**：Strategy 会同时承担构造与执行，且 Builder 退化为转发工厂；
4. **仅新增多个具体 Agent Builder**：调用方可选策略，但公共组件、默认参数和冻结逻辑会重复。

## 8. 文件影响

计划调整：

```text
acore/agent/
├── agent.go          # 新增 RunInput、RunStrategy 和稳定错误
├── builder.go        # 持有/校验 RunStrategy，构建 configuredAgent
├── run.go            # 通用 configuredAgent 与策略 Stream 契约保护
├── single_turn.go    # 公开 SingleTurnStrategy 的单轮算法
├── clone.go          # 增加 Agent Event 和 RunInput 复制
├── agent_test.go     # 通用 Agent、外部 Strategy 和 Stream 契约
├── builder_test.go   # 策略缺失、typed nil、重复与冻结
├── single_turn_test.go
└── example_test.go   # 显式组装 SingleTurnStrategy
```

原 `run.go` 中的单轮逻辑迁移到 `single_turn.go`，不保留 `singleTurnAgent` 兼容壳。

## 9. 测试与验证

至少覆盖：

1. 仓库外类型可实现 `RunStrategy`；
2. Builder 拒绝 nil/typed-nil、重复和缺失策略；
3. Build 失败后可补策略，成功后策略配置冻结；
4. Builder 实际调用注入策略，不包含单轮类型分支；
5. `RunInput` 包含正确 LLM、System Prompt、完整 Messages 和合并后的 Options；
6. 调用方修改原 Request 不影响策略输入；策略/调用方修改事件不交叉污染；
7. 策略建流错误、nil Stream、Stream error、静默结束、无 Result RunDone 和 Context 取消；
8. SingleTurnStrategy 保持当前模型事件、结果映射和 Tool Call 计数；
9. 在 RunStart 和模型事件处早停均释放模型 generator；
10. 同一 Agent + Strategy 并发 Run 隔离。

验证命令：

```bash
gofmt -w agent/*.go
go test ./agent
go test ./...
go vet ./...
go test -race ./agent
```

测试继续使用 fake LLM 和 fake RunStrategy，不访问网络。若环境仍缺少 C 编译器导致 race 无法执行，将如实记录。

## 10. 验收标准

- Builder 不引用或构造任何具体运行策略；
- Builder 同时持有公开 `model.LLM` 与公开 `RunStrategy` 抽象；
- `SingleTurnStrategy` 可由仓库外代码显式构造和注入；
- 新策略只需实现 `RunStrategy`，无需修改 Builder 或 `configuredAgent`；
- 通用输入校验、默认值合并、数据快照和 Stream 契约不在不同策略中重复；
- 原单轮 Agent 行为和验证结果保持不变；
- 文档、示例和测试全部使用显式策略组装。

本文档已确认并实现，`acore/agent` 已使用公开 RunStrategy 和 SingleTurnStrategy。

后续策略清单、依赖顺序和统一验收要求见 `agent-run-strategy-roadmap.md`。
