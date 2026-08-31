# acore 工具系统设计方案

## 1. 背景与结论

当前 `acore` 已有：

- `model`：定义模型侧 `ToolSpec`、`ToolCall` 和 `RoleTool` 消息协议；
- `event`：提供同步、进程内事件发布能力；
- 尚无当前生效的工具注册与执行模块。

本方案在 `acore` 中新增独立的 `tool` 包，使用“**启动期 Builder + 构建后不可变 System + 全局 Proxy 链**”组织工具系统：

```text
Agent Builder / 应用装配层
          │ 注册 Tool、Proxy
          ▼
      tool.Builder
          │ Build
          ▼
      tool.System ── 实现 tool.Service
          │
          ├── Specs：向 Agent Loop 暴露工具目录
          └── Execute：校验、解析、经过 Proxy 链后执行 Tool
```

工具系统只负责工具定义、注册、发现和一次调用的执行，不负责驱动模型多轮对话。未来 Agent Loop 负责在 `model.ToolSpec`、`model.ToolCall`、`model.Message` 与工具系统协议之间显式转换。

## 2. 设计目标

首版目标：

1. 允许框架外实现并注册自定义工具；
2. 使用 Builder 在 Agent 启动阶段完成工具和 Proxy 装配；
3. 工具名称唯一，工具定义和调用参数具有基础校验；
4. 向 Agent Loop 暴露最小的工具发现、执行接口；
5. 通过 Proxy 链扩展审批、鉴权、限流、超时、重试、缓存和可观测性等横切能力；
6. 构建后的工具系统不可变，可供多个 Agent Run 并发调用；
7. 明确 Context、错误、数据所有权和 Proxy 顺序语义；
8. 不依赖具体模型 Provider，不把工具系统与 Runtime 或 Event Bus 绑定。

## 3. 非目标

首版不直接实现：

- 模型与工具之间的多轮 Tool Calling Loop；
- 动态注册、注销及运行期热更新工具；
- 自动生成 JSON Schema 或基于泛型的参数绑定；
- 完整 JSON Schema Draft 校验；
- 默认重试、超时、缓存、审批、权限或事件发布策略；
- 工具进程隔离、容器沙箱及操作系统权限控制；
- 工具调用持久化、断点恢复和分布式执行；
- 多模态、文件或流式工具结果；
- MCP 等外部工具协议适配。

这些能力可以在核心契约稳定后，通过 Proxy、Tool 适配器或独立集成包扩展，不进入首版核心。

## 4. 模块边界与依赖方向

计划新增：

```text
acore/tool/
├── tool.go         # Spec、Call、Result、Tool、Service
├── proxy.go        # Invocation、Next、Proxy 和代理链
├── builder.go      # 启动期注册、校验、快照和构建
├── system.go       # 不可变目录、调用解析和执行
├── builder_test.go # 注册、构建及边界测试
└── system_test.go  # 执行、代理、错误和并发测试
```

依赖方向：

```text
model       event
  ▲           ▲
  │ 显式转换   │ 可选事件 Proxy 显式依赖 Publisher
  │           │
Agent Loop / 应用集成层
          │
          ▼
      tool.Service
          ▲
          │
      tool.System
          │
     Proxy chain
          │
          ▼
         Tool
```

约束：

- `tool` 首版只依赖 Go 标准库；
- `tool` 不依赖 `model`、`event`、未来的 Runtime 或 Agent 包；
- `model.ToolSpec` 是模型请求协议 DTO，`tool.Spec` 是可执行工具目录的描述符，两者职责不同；
- Agent Loop 在编排边界显式把 `tool.Spec` 转换为 `model.ToolSpec`，避免基础模块反向耦合；
- 需要发布工具事件时，由应用实现接收 `event.Publisher` 的 Proxy，而不是让 Tool System 隐式持有 Event Bus；
- Runtime 或 Agent Builder 可以持有 `tool.Service`，但不能暴露 System 内部代理链供运行期修改。

## 5. 核心公开契约

建议 API：

```go
package tool

import (
    "context"
    "encoding/json"
)

// Spec 描述一个可展示给模型的工具。
type Spec struct {
    Name        string
    Description string
    Parameters  json.RawMessage // JSON Schema
}

// Call 表示调用方发起的一次工具调用。
type Call struct {
    ID        string
    Name      string
    Arguments json.RawMessage
}

// Result 是一次成功调用的文本结果。
type Result struct {
    Content string
}

// Tool 实现一个具体工具。
type Tool interface {
    Spec() Spec
    Execute(context.Context, json.RawMessage) (Result, error)
}

// Service 是 Agent Loop 可见的最小工具能力。
type Service interface {
    Specs() []Spec
    Execute(context.Context, Call) (Result, error)
}
```

### 5.1 契约说明

- `Call.ID` 用于关联模型 Tool Call、事件和 Trace；允许为空，以支持非模型调用方；
- 工具名称由 `Spec.Name` 唯一标识；`Call.Name` 必须精确匹配，不做大小写转换或空白修剪；
- `Parameters` 和 `Arguments` 使用 `json.RawMessage`，避免 `map[string]any` 带来的数字精度和序列化变化；
- 具体 Tool 只接收参数，不接收完整 System，也不能绕过代理链调用其他已注册工具；若工具编排确有需求，应由上层显式注入最小依赖；
- `Result` 首版只包含文本，能直接转换为 `RoleTool` 的文本内容；工具失败通过 Go `error` 返回，不伪装成成功结果；
- `Service` 不提供 `Add`、`Remove`、`LookupTool` 或 Proxy 访问能力，避免 Loop 绕过统一执行入口。

## 6. Builder 与工具目录

建议 Builder API：

```go
type Builder struct {
    // 内部状态不公开。
}

func NewBuilder() *Builder
func (b *Builder) AddTool(Tool) error
func (b *Builder) UseProxy(Proxy) error
func (b *Builder) Build() (*System, error)
```

行为：

1. `AddTool` 立即读取并校验 `Tool.Spec()`；
2. Builder 保存 Spec 深拷贝和 Tool 引用，不在执行期间重复调用 `Spec()`；
3. 工具名称必须唯一，重复注册返回稳定错误；
4. `UseProxy` 按调用顺序追加 Proxy；同一个 Proxy 可被有意注册多次，因此首版不要求 Proxy ID；
5. 空 Builder 可以成功构建，得到没有工具但行为完整的 Service；
6. `Build` 深拷贝目录和 Proxy 列表，构造不可变执行链；
7. 首次成功 `Build` 后 Builder 冻结，后续 `AddTool`、`UseProxy` 和 `Build` 返回 `ErrBuilderBuilt`；
8. Builder 仅用于单 goroutine 启动装配，不承诺并发安全。

`Specs()` 按 **AddTool 注册顺序** 返回深拷贝。该顺序可预测、由应用控制，也能使传给模型的工具列表保持稳定。System 内部同时维护名称索引，以便 O(1) 查找工具。

### 6.1 注册期校验

首版执行以下基础校验：

- Tool 不能是 nil 或带类型的 nil；
- `Spec.Name` 不能为空；
- `Spec.Parameters` 必须是合法 JSON，且顶层必须是 JSON 对象；
- 工具名称不能重复；
- Proxy 不能是 nil 或带类型的 nil。

首版不自行实现 JSON Schema 标准。注册期只验证 Schema 载体合法，运行期只验证参数是合法 JSON 对象；字段类型、必填项、取值范围和业务权限仍由具体 Tool 负责。后续若引入 JSON Schema Validator，需单独评估 Draft、第三方依赖、错误格式和性能，不实现不完整的自定义 Schema 解析器。

## 7. Proxy 扩展机制

建议公开契约：

```go
// Invocation 是已解析且不可直接修改的工具调用。
type Invocation struct {
    // 字段不导出，由 System 创建。
}

func (i Invocation) ID() string
func (i Invocation) Name() string
func (i Invocation) Arguments() json.RawMessage
func (i Invocation) Spec() Spec
func (i Invocation) WithArguments(json.RawMessage) Invocation

// Next 表示代理链剩余部分。
type Next interface {
    Execute(context.Context, Invocation) (Result, error)
}

// NextFunc 将函数适配为 Next。
type NextFunc func(context.Context, Invocation) (Result, error)

func (f NextFunc) Execute(context.Context, Invocation) (Result, error)

// Proxy 包装所有已经成功解析的工具调用。
type Proxy interface {
    Execute(context.Context, Invocation, Next) (Result, error)
}
```

### 7.1 执行顺序

若按 A、B、C 注册，顺序固定为：

```text
A before
  B before
    C before
      Tool
    C after
  B after
A after
```

即请求方向按注册顺序进入，结果方向逆序返回。

### 7.2 Proxy 能力与约束

- Proxy 可以检查调用 ID、工具名称、Spec 和参数；
- `Arguments()`、`Spec()` 返回副本，不能修改 System 内部快照；
- Proxy 需要规范化或替换参数时，必须使用 `WithArguments` 创建新的 Invocation；
- Proxy 不能更改工具名称或底层 Tool，避免鉴权后重定向到其他工具；
- Proxy 可以不调用 `Next`，用于审批拒绝、缓存命中等短路场景；
- Proxy 正常情况下调用一次 `Next`；有明确边界的重试 Proxy 可以多次调用；
- Proxy 多次或并发调用 `Next` 意味着底层 Tool 可能执行多次，幂等性与重试边界由该 Proxy 负责；
- System 不自动恢复 Proxy 或 Tool 的 panic，panic 视为程序错误；
- Proxy 不应创建脱离本次调用 Context 生命周期的 goroutine。

只有名称、参数和工具解析均成功的调用才进入 Proxy 链。空名称、非法 JSON 和不存在的工具在进入链前返回。这样 Proxy 始终处理结构完整、已解析的 Invocation；无效调用审计应由调用入口或未来专用审计层处理。

## 8. 调用数据流

`System.Execute` 执行步骤：

1. 检查 `ctx.Err()`；
2. 校验 `Call.Name` 非空；
3. 校验 `Call.Arguments` 是合法 JSON 对象；
4. 按名称解析已注册 Tool，不存在则返回 `ErrToolNotFound`；
5. 复制调用参数和 Spec，构造不可变 Invocation；
6. 从第一个 Proxy 开始执行代理链；
7. 每个代理节点执行前再次检查 Context；
8. 终端节点检查 Context 和 Proxy 替换后的参数；
9. 调用具体 `Tool.Execute`；
10. 包装并返回 Tool 或 Proxy 错误，同时保留错误链。

流程示意：

```text
Call
 │
 ├─ context / name / JSON object 校验
 ├─ 名称解析
 ▼
Invocation snapshot
 │
 ▼
Proxy A → Proxy B → ... → terminal validation → Tool.Execute
 │
 ▼
Result 或 error
```

终端再次校验参数是必要的，因为 Proxy 可以通过 `WithArguments` 替换参数。

## 9. 错误语义

计划提供可供 `errors.Is` 判断的稳定错误：

- `ErrBuilderBuilt`：成功构建后继续修改或重复构建；
- `ErrNilTool`：注册 nil Tool；
- `ErrEmptyToolName`：工具定义或调用名称为空；
- `ErrInvalidSchema`：参数 Schema 不是合法 JSON 对象；
- `ErrDuplicateTool`：工具名称重复；
- `ErrNilProxy`：注册 nil Proxy；
- `ErrInvalidArguments`：调用参数不是合法 JSON 对象；
- `ErrToolNotFound`：工具不存在；
- `ErrInvalidInvocation`：Proxy 向 Next 传入伪造或损坏的 Invocation。

错误规则：

- 错误信息使用小写开头并包含操作和工具名称；
- Tool 与 Proxy 返回的错误使用 `%w` 包装，调用方可用 `errors.Is/As` 判断原始错误；
- Context 取消返回 `context.Canceled` 或 `context.DeadlineExceeded`，并保留错误链；
- System 不自动重试，不把错误转换成 `Result{Content: ...}`；
- 是否把某类工具错误反馈给模型、终止 Run 或触发恢复，由 Agent Loop 决定；
- 错误文本可能包含内部信息，Loop 不应未经分类和脱敏直接发送给模型。

## 10. 并发、不可变性与所有权

- 构建后的 System 不再修改工具目录和代理链，本身无需为目录读操作加锁；
- 同一个 System 可以被多个 goroutine 并发调用；
- 注册的 Tool 和 Proxy 可能被并发调用，其实现必须自行保证并发安全；
- `json.RawMessage` 在注册、调用、访问 Invocation 和返回 Specs 时均进行复制；
- `Spec()` 只在注册时读取一次，Tool 后续改变自身 Spec 不影响已构建 System；
- System 不取得 Tool、Proxy 或其外部资源的生命周期所有权，不负责调用 `Close`；
- 如果 Tool 持有 HTTP Client、数据库连接等资源，由应用创建、注入并在应用生命周期结束时释放；
- Context 不保存在 System、Tool 或 Proxy 中，只沿单次调用传递。

动态工具目录需要额外定义读写并发、正在执行调用、模型目录快照和注销后的生命周期，首版采用不可变快照以避免这些复杂语义。

## 11. 与 model、event 和 Agent Builder 的集成

### 11.1 与 model 集成

未来 Agent Loop 执行以下显式转换：

```text
tool.Service.Specs()
        │
        └── 转换为 []model.ToolSpec，放入 model.Context.Tools

model.ToolCall
        │
        └── 转换为 tool.Call，调用 tool.Service.Execute

成功 tool.Result
        │
        └── 转换为 RoleTool + ToolCallID 消息

error
        │
        └── 按 Loop 策略终止、脱敏反馈模型或进入恢复流程
```

`tool` 包不直接生成 `model.Message`，因为模型对话推进属于 Loop，而不是一次工具执行。

### 11.2 与 event 集成

核心 System 不自动发布事件。可选事件 Proxy 可以发布：

- `ToolStartedEvent`；
- `ToolCompletedEvent`；
- `ToolFailedEvent`。

事件 Proxy 必须显式接收 `event.Publisher`，并明确：

- 发布失败是中断调用还是仅记录；
- 工具错误和事件错误同时发生时的优先级；
- 是否隐藏完整参数和结果；
- Run ID、Call ID 等关联信息从何处获得。

在运行身份协议尚未确定前，不在 `tool` 核心中预置这些事件。

### 11.3 与 Agent Builder 集成

建议装配关系：

```go
toolBuilder := tool.NewBuilder()
if err := toolBuilder.AddTool(searchTool); err != nil {
    return err
}
if err := toolBuilder.UseProxy(authProxy); err != nil {
    return err
}
tools, err := toolBuilder.Build()
if err != nil {
    return err
}

// 未来由更高层 Agent Builder 接收只读能力。
agent, err := agent.NewBuilder().UseTools(tools).Build()
```

高层 Agent Builder 可以提供添加工具的便捷入口，但内部仍应委托 `tool.Builder`，不重复实现注册、校验和代理链逻辑。

## 12. 安全与可观测性约束

- 模型生成的工具名称和参数全部视为不可信输入；
- JSON 语法合法不等于业务合法，Tool 必须校验字段、长度、范围、权限和资源标识；
- 高风险 Tool 应通过 Proxy 或 Tool 自身实施显式授权，不能把“模型选择了工具”视为用户授权；
- Shell、文件、网络和数据库工具必须自行限定作用域；核心代理链不等同于安全沙箱；
- 默认不得记录完整参数、结果、Token、密钥或个人敏感信息；
- 超时应由调用方 Context 或超时 Proxy 设置，核心不使用隐藏的统一超时；
- 重试必须有次数、退避和幂等约束，默认不开启；
- Call ID 仅用于关联，不自动视为可信幂等键。

## 13. 关键取舍

1. **启动期构建而非动态注册**：符合 Agent 建造者模式，换取简单、无锁且可预测的执行目录。
2. **Service 最小接口而非暴露 System**：Loop 只能发现和执行工具，不能改变注册表或绕过 Proxy。
3. **全局 Proxy 链而非内置治理策略**：统一覆盖每个有效调用，同时让审批、缓存和可观测性按应用需求组合。
4. **独立 tool.Spec 而非复用 model.ToolSpec**：保持工具执行域与模型协议域解耦，在 Loop 边界承担一次简单转换。
5. **原始 JSON 而非首版泛型绑定**：协议清晰且不引入 Schema 生成依赖；类型化适配器可在核心稳定后增加。
6. **仅做基础 JSON 校验而非自研 JSON Schema Validator**：避免不完整实现制造错误安全感，业务参数由 Tool 继续严格校验。
7. **Go error 表示失败而非 Result.IsError**：成功值与执行失败语义分离，模型反馈策略由 Loop 统一决定。
8. **文本 Result 而非首版多模态结果**：与当前最小 Tool 消息链路匹配，后续根据实际 Provider 能力扩展。
9. **Proxy 可短路和有界重入 Next**：支持缓存、审批与重试，但重复执行风险由 Proxy 明确承担。

## 14. 实施计划

用户确认本方案后，按以下顺序实施：

1. 新增 `tool/tool.go`，定义基础协议；
2. 新增 `tool/proxy.go`，实现 Invocation 副本语义和代理节点；
3. 新增 `tool/builder.go`，实现注册校验、冻结和不可变快照；
4. 新增 `tool/system.go`，实现 Specs 与 Execute；
5. 补充正常、边界、错误、取消及并发测试；
6. 根据实际 API 补充包文档和最小使用示例；
7. 不在本阶段修改 `model`、`event` 或实现 Agent Loop。

## 15. 测试与验证计划

单元测试至少覆盖：

1. 注册并执行自定义 Tool；
2. 空工具系统可以构建，无工具时 Specs 为空；
3. Specs 保持注册顺序并返回深拷贝；
4. Tool Spec 在注册时被快照；
5. nil Tool、空名称、非法 Schema 和重复名称被拒绝；
6. nil Proxy 被拒绝；
7. 成功 Build 后 Builder 冻结；
8. Proxy 按注册顺序进入、逆序返回；
9. Proxy 可以读取调用、替换参数和短路执行；
10. 终端拒绝 Proxy 替换出的非法参数；
11. Proxy 可以实现明确且有界的重试；
12. 空调用名、非法参数及不存在工具返回稳定错误；
13. Tool 和 Proxy 错误保留 `errors.Is/As` 链；
14. 调用前和代理链中的 Context 取消得到正确错误；
15. 输入参数和内部 Schema 不会被 Tool、Proxy 或调用方反向修改；
16. 同一个 System 可并发执行且无数据竞争。

在 `acore` 模块目录执行：

```bash
gofmt -w tool/*.go
go test ./tool
go test ./...
go vet ./...
go test -race ./tool
```

## 16. 验收标准

- 外部项目无需修改 `acore` 即可实现并注册 Tool 和 Proxy；
- Agent Loop 只能通过 `tool.Service` 获取定义和执行调用；
- 每个有效调用都按确定顺序经过完整 Proxy 链，除非 Proxy 明确短路；
- Builder 构建后的工具目录和代理链不可变；
- 工具定义、参数、错误、取消、并发和数据副本语义符合本文档；
- 不新增第三方依赖，不修改现有 `model` 和 `event` 行为；
- 单元测试、全模块测试、静态检查及 `tool` 竞态检测通过。

## 17. 风险与后续演进

- **JSON Schema 未完整执行**：首版由 Tool 做业务校验；确认真实需求后再选择标准实现，不自研子集。
- **错误反馈模型的策略未定义**：必须在 Tool Calling Loop 设计中明确错误分类、脱敏和终止条件。
- **Result 仅支持文本**：出现图片、文件或结构化结果需求时，应与 `model.ContentBlock` 能力一起设计兼容扩展。
- **Proxy 重试可能重复副作用**：默认不提供重试；有副作用工具应配合幂等设计和明确策略。
- **不可变目录不支持热插拔**：如未来确有动态工具需求，应设计独立的版本化 Catalog，而不是直接给当前 System 加锁和 Remove 方法。
- **高风险工具不具备天然隔离**：Proxy 只能做调用治理，不能替代操作系统或容器级沙箱。

本方案依据当前工作区中的 `model`、`event` 接口和 Agent Builder 目标制定，未假设尚不存在的 Runtime、Loop 或第三方工具协议实现。