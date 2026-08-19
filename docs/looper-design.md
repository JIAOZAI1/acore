# Looper 设计

## 1. 职责

`looper` 驱动一次 Agent 运行。一次运行可以由 Loop 策略发起任意多轮模型调用、执行工具并推进上下文。

核心只约定：

- 可替换的 `Loop` 策略；
- 每次运行通过 Runtime 解析并调用指定的 `model.LLM`；
- 通过不暴露内部代理链的 `tool.Service` 发现和执行工具；
- 通过进程内事件发布流式输出和自定义事件；
- 使用 `context.Context` 取消；
- 模型、事件处理器和策略错误沿调用栈返回；
- 同一个 `Looper` 可并发发起相互隔离的运行。

核心暂不约定重试、持久化、人工审批、断点或恢复协议。这些能力待语义稳定后以 Loop、装饰器或更上层运行时组合，不加入当前接口。

## 2. 主要接口

```go
type Loop interface {
    Run(context.Context, Run, Input) error
}

type Run interface {
    Generate(context.Context, model.Request) (model.Stream, error)
    Tools() tool.Service
    Publish(context.Context, event.Event) error
}
```

`Input` 携带 Provider ID、Model ID 和初始请求。Looper 在每次运行开始时通过 Runtime 解析并绑定 LLM。`Run` 是仅在本次调用中使用的能力接口：自定义 Loop 可以多次调用 `Generate`，通过 `Tools` 调用工具服务，消费流并推进自己的消息上下文，也可以发布任意实现 `event.Event` 的事件。工具代理注册、顺序和执行均封装在 ToolSystem 内，对 Loop 不可见。

`LoopFunc` 让简单策略可以直接由函数实现。

## 3. 流、错误与取消

事件发布是同步的：每个模型增量或自定义事件在 `Publish` 返回前已被当前订阅者处理，因此无需核心维护无界队列，并天然形成背压。

错误不编码成成功事件：

- 建立模型流失败，由 `Generate` 返回；
- 流建立后的失败，从 `model.Stream` 返回；
- 事件处理器失败，由 `Publish` 返回；
- 策略返回的错误，由 `Looper.Run` 包装并保留错误链；
- 取消返回 `context.Canceled` 或 `context.DeadlineExceeded`。

Loop 必须在错误或取消后停止，不应创建脱离运行生命周期的 goroutine。

## 4. 并发和状态所有权

`Looper` 持有进程级 Runtime，但不保存每次运行的可变状态；每次 `Run` 都解析模型并创建独立的运行能力对象。只要注入的 `Loop`、Provider 和 Runtime 服务支持并发，同一个 `Looper` 就可以并发使用。

对话状态属于具体 Loop。`Input.Request` 内可达的切片属于调用方；策略需要修改上下文时应先复制，避免并发运行之间共享可变数据。

## 5. 内置策略

`SingleTurnLoop` 是最小参考实现：执行一次模型生成，并将所有 `model.Event` 包装为 `ModelEvent` 发布。它不包含工具执行、多轮推进或重试。

工具循环应作为新的 `Loop` 实现：将 `tool.Spec` 转换为 `model.ToolSpec`，消费模型的 tool call，通过 `run.Tools().Execute` 调用工具，将结果追加为 tool message，再开始下一轮。每次工具执行会在 ToolSystem 内经过完整代理链；Loop 和模型无法观察代理。这样不会将尚未确定的工具并发、审批和恢复语义固化到 Looper 核心。

## 6. Runtime 与公共包边界

`looper` 依赖公共 `runtime` 包提供的模型寻址、工具服务和事件发布能力，不持有具体 ProviderRegistry、ToolSystem 或 EventBus。Runtime 不反向依赖或注册 Loop；Loop 由 Looper 或更上层 Agent 组件选择，避免循环依赖。每次运行使用的标准库 `context.Context` 和运行状态不存入进程级 Runtime。

`model`、`event`、`runtime` 和 `looper` 都是公共包。仓库外的使用者可以实现自己的 Provider、LLM、Loop 和事件类型；具体厂商适配仍可保留在 `internal/provider`，直到其配置 API 稳定。
