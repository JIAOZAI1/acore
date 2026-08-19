# Runtime 设计

## 1. 定位

`runtime` 是进程级共享能力的组合中心。它通过稳定、最小的接口向编排层提供模型寻址、工具调用和事件发布能力，使 Looper 不依赖具体 Provider、ToolSystem、Registry 或 EventBus。

Runtime 不是通用 Service Locator，不支持 `Register(any)`、字符串服务查找、反射注入或可变全局实例。领域协议由 `model`、`event` 和 `tool` 公共包定义。

## 2. 依赖方向

```text
model / event / tool contracts
              ▲
              │
           runtime
              ▲
              │
            looper
              ▲
              │
      application bootstrap
```

Runtime 不依赖 Looper。Loop 是 Runtime 能力的消费者和编排策略，不作为 Runtime 服务注册。需要多个具名 Loop 时，应由 `looper` 或更上层 Agent 组件管理。

## 3. 构建和冻结

应用启动层使用类型化 Builder 装配 Runtime：

```go
builder := runtime.NewBuilder()
if err := builder.AddProvider(provider); err != nil {
    return err
}
if err := builder.UseTools(toolSystem); err != nil {
    return err
}
if err := builder.UseEvents(bus); err != nil {
    return err
}
rt, err := builder.Build()
```

- Provider 是多实例能力，按唯一 Provider ID 注册；
- `tool.Service` 和 `event.Publisher` 是单实例能力，重复设置返回错误；
- Tool 和执行 Proxy 由 ToolSystem 自己注册，Runtime 只接收构建完成的 `tool.Service`；
- `Build` 验证必需能力；
- 构建失败后可以补充缺失能力并重试；
- 构建成功后 Builder 冻结，Runtime 仅暴露不可变的窄接口；
- 首版不支持运行期间动态注册或卸载。

## 4. 生命周期和并发

Builder 只用于应用启动期的单 goroutine 装配。构建后的 Runtime 可以由多个 Agent Run 并发共享；具体 Provider、`tool.Service` 和 `event.Publisher` 必须遵守各自接口的并发约定。

注入组件仍由应用启动层拥有并负责释放。Runtime 不保存 `context.Context`，也不隐式启动或关闭组件。取消和超时始终通过调用方法的第一个参数传递。

## 5. Runtime 与 RunContext

Runtime 保存进程级服务；每次 Looper 运行创建独立 RunContext，绑定该次运行选择的 LLM，并向 Loop 暴露模型生成、不可见内部代理的 `tool.Service` 和事件发布能力。对话、Run ID 和临时结果不进入 Runtime。

工具注册、发现和代理链执行完全封装在 ToolSystem 内。Runtime 不提供 `AddTool`、`UseProxy` 或代理查询接口；详见 `tool-design.md`。

Event Publisher 也可以由应用启动层直接注入需要它的 Provider 装饰器、工具 Proxy 或具体业务组件。组件不得为了发布事件而持有完整 Runtime；Runtime 与其他组件共享 Publisher，不垄断其使用权。

## 6. 后续扩展

统一 `Start/Close`、热插拔和动态配置均不在首版范围；扩展前必须先定义所有权、启动失败回滚和关闭顺序。
