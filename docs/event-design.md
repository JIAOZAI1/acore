# 事件系统设计

## 定位

`event` 包提供进程内事件通知，用于解耦 Agent Loop、工具、审批和可观测性等模块。它不是消息队列，不提供跨进程传输、持久化、重试或至少一次投递保证。

## 核心语义

- Bus 通过显式构造和依赖注入传递，不提供全局实例。
- 事件按精确 Go 类型路由；值、指针和接口类型互不等价。
- `Event.Name` 仅用于日志、追踪等可观测性，不参与路由。
- Handler 按订阅顺序同步执行。
- 单个 Handler 失败不阻止后续 Handler；`Publish` 使用 `errors.Join` 返回全部错误。
- 每次调用 Handler 前检查 Context；取消后停止调用后续 Handler。
- Bus 支持并发发布、订阅和取消。Handler 可能被并发调用，Handler 自身负责并发安全。
- 取消订阅是幂等操作，只影响之后创建的发布快照；已经取得快照的发布仍可能调用该 Handler。
- Handler panic 不由 Bus 恢复。业务 Handler 不应使用 panic 表示普通错误。

## 基本用法

```go
type TaskCompleted struct {
    TaskID string
}

func (TaskCompleted) Name() string { return "task.completed" }

bus := event.NewBus()
subscription, err := event.Subscribe(bus, func(ctx context.Context, e TaskCompleted) error {
    return recordCompletion(ctx, e.TaskID)
})
if err != nil {
    return err
}
defer subscription.Unsubscribe()

if err := bus.Publish(ctx, TaskCompleted{TaskID: "task-1"}); err != nil {
    return err
}
```

## 首版明确不支持

- 异步或并行消费
- Handler 优先级和通配符订阅
- 按事件名称路由
- 自动重试和死信处理
- 事件持久化及跨进程通信
- 中间件链

这些能力应在出现明确业务需求和错误语义后单独设计，不加入当前 Bus。
