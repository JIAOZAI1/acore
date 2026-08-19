# 工具系统设计

## 1. 定位

`tool` 包负责工具定义、启动期注册、发现和执行。所有通过 `tool.Service.Execute` 发起的有效工具调用都会经过工具系统内部的不可变代理链。代理链对 Loop、模型和 Runtime 不可见。

工具系统核心不负责模型多轮推进、重试默认策略、人工审批协议、持久化、沙箱或事件发布。重试、审批、权限和缓存可以按需实现为自定义 Proxy；工具执行通知和可观测性可以由显式接收 `event.Publisher` 的外部 Proxy 实现。Tool Loop 决定如何把最终结果或错误反馈给模型。

## 2. 依赖和封装

```text
Loop → tool.Service → tool.System → Proxy chain → Tool
```

- Tool 只实现具体业务，不依赖 Runtime、Looper 或 EventBus；
- System 管理工具目录、参数基础校验、代理链和最终执行；
- Runtime 只持有 `tool.Service` 接口，不注册 Tool 或 Proxy，也不执行代理链；
- Loop 只能发现工具和调用 Service，不能读取或改变代理链；
- 模型只能看到转换后的工具定义、调用参数和 Tool Loop 选择反馈的最终结果。

## 3. 构建和执行

Tool Builder 使用 `AddTool` 和 `UseProxy` 完成启动期注册。工具名称和 Proxy ID 必须唯一；成功 Build 后 Builder 冻结。空工具系统合法，适用于不启用工具的 Agent。

Proxy 按注册顺序进入、按逆序返回。例如 A、B、C 的执行顺序为 `A → B → C → Tool → C → B → A`。Proxy 可以不调用 Next 以短路执行，也可以为明确且有界的重试多次调用 Next；普通 Proxy 应调用一次。每次有效 Tool Call 都单独遍历代理链。

System 在进入代理链前检查 context、工具名、JSON 参数并解析 Tool。Invocation 使用访问器提供参数和 Spec 的副本；参数转换必须通过 `WithArguments`。终端节点再次检查 context 和代理可能替换的参数，再执行已解析 Tool。

## 4. 错误和并发

注册、查找和参数错误提供可供 `errors.Is` 判断的哨兵错误。Tool 错误使用 `%w` 包装；System 不自动将错误转换为成功 Result，不重试也不恢复 panic。

构建后的 System 本身不可变，可并发发现和执行。被注册的 Tool 和 Proxy 可能被并发调用，具体实现必须自行保证并发安全并响应 context。Proxy 不得启动脱离调用生命周期的 goroutine，也不得默认记录完整参数、结果或敏感信息。

## 5. Runtime 与 Looper

应用先独立构建 ToolSystem，再通过 `runtime.Builder.UseTools` 注入。Runtime 不取得组件所有权。运行级 `looper.Run.Tools` 只返回 `tool.Service`，因此自定义 Loop 可以调用工具，但无法观察其内部代理。

Tool Loop 后续负责将 `tool.Spec` 转换为 `model.ToolSpec`、识别模型 Tool Call、调用工具服务、构造 RoleTool 消息和控制最大轮次。`tool` 包不依赖 `model`，两种 Spec 在编排层显式转换。

若需要统一发布 ToolStarted、ToolCompleted 等通知，应用可将同一个 Publisher 分别注入 Runtime 和事件 Proxy，再把 Proxy 注册到 ToolSystem。ToolSystem 和普通 Tool 不应反向依赖 Runtime。事件发布是同步且可能失败的，Proxy 必须明确发布失败是否中断工具调用以及与工具错误的优先级。
