# Agent 事件模块设计方案

## 1. 背景与结论

在 `acore` 中新增独立的 `event` 包，提供进程内、同步、类型安全的发布/订阅能力。Agent 运行流程、工具代理及其他组件通过事件解耦；用户可以在自己的包中定义事件类型并订阅消费。

首版采用“按事件的精确 Go 类型路由 + 订阅者同步顺序消费”的方案。它实现简单、错误和背压语义明确，也不需要后台 goroutine 或额外依赖。

## 2. 目标

- 支持框架内置事件和用户自定义事件；
- 支持同一事件的多个订阅者；
- 支持发布事件并由订阅者消费；
- 提供类型安全的订阅 API，避免业务侧手工类型断言；
- 支持取消订阅；
- 支持并发发布、订阅和取消订阅；
- 使用 `context.Context` 传递取消信号；
- 通过最小发布接口与 Agent 运行组件解耦。

## 3. 非目标

首版不支持以下能力：

- 异步队列或并行消费；
- 跨进程传输和事件持久化；
- 自动重试、死信队列和至少一次投递；
- 订阅优先级、通配符及按名称匹配；
- 历史事件回放；
- 使用事件处理器实现审批、重试等会改变 Agent 控制流的协议；
- 全局默认 Bus 或 Service Locator。

这些能力需要额外定义投递、顺序、生命周期和失败恢复语义，不放入当前最小实现。

## 4. 模块边界

计划新增：

```text
acore/event/
├── event.go       # Event、Publisher、HandlerFunc、Subscription 等公开契约
├── bus.go         # 进程内同步 Bus 实现
└── bus_test.go    # 行为、边界及并发测试
```

依赖方向：

```text
用户自定义事件 / Agent 领域事件
                 │
                 ▼
            acore/event
                 ▲
                 │ Publisher
       Agent Loop / Tool Proxy / Observer
```

约束：

- `event` 只依赖 Go 标准库，不依赖 `model` 或未来的 Runtime、Looper；
- `event` 包只提供事件协议和分发机制，不集中定义所有领域事件；
- 标准运行事件应定义在产生它的领域包中，并实现 `event.Event`；
- 当前 `model.Event` 仍表示模型生成流事件，与运行时的 `event.Event` 职责不同，通过包名区分；后续可由 Looper 将模型流事件包装成运行级事件后发布。

## 5. 核心 API

建议公开 API：

```go
package event

import "context"

// Event 是可发布的事件。Name 用于日志和追踪，不参与路由。
type Event interface {
    Name() string
}

// Publisher 是事件生产方依赖的最小接口。
type Publisher interface {
    Publish(context.Context, Event) error
}

// HandlerFunc 是 E 类型事件的消费函数。
type HandlerFunc[E Event] func(context.Context, E) error

// Subscription 控制一个订阅的生命周期。
type Subscription interface {
    Unsubscribe()
}

type Bus struct {
    // 内部状态不对外暴露。
}

func NewBus() *Bus
func (b *Bus) Publish(context.Context, Event) error
func Subscribe[E Event](b *Bus, handler HandlerFunc[E]) (Subscription, error)
```

`Subscribe` 使用包级泛型函数，而不是 Bus 的泛型方法，因为 Go 不支持方法声明自己的类型参数。生产组件只依赖 `Publisher`；应用装配层持有具体 `*Bus` 并完成订阅，不向业务组件暴露完整事件总线。

### 5.1 自定义事件示例

```go
type TaskCompleted struct {
    TaskID string
}

func (TaskCompleted) Name() string { return "task.completed" }

bus := event.NewBus()
subscription, err := event.Subscribe(bus, func(ctx context.Context, e TaskCompleted) error {
    return recordTask(ctx, e.TaskID)
})
if err != nil {
    return err
}
defer subscription.Unsubscribe()

if err := bus.Publish(ctx, TaskCompleted{TaskID: "task-1"}); err != nil {
    return err
}
```

用户只需定义实现 `Name() string` 的普通 Go 类型，不需要向中心注册事件类型。

## 6. 路由与消费语义

### 6.1 路由规则

- 按事件的精确 Go 类型路由；
- `TaskCompleted`、`*TaskCompleted` 以及其实现的接口是不同的订阅类型；
- `Event.Name()` 只用于可观测性，不作为路由键；
- 不允许订阅 `event.Event` 这类接口类型，避免引入隐式通配符和不确定的匹配顺序；
- 发布没有订阅者的事件成功返回，不视为错误。

采用精确类型路由可以避免字符串重名、载荷类型不一致和消费侧断言错误，并允许用户事件与框架事件使用同一机制。

### 6.2 执行顺序

- `Publish` 在调用 goroutine 中同步执行处理器；
- 同一事件类型的处理器按订阅顺序串行执行；
- `Publish` 返回时，本次快照中的处理器已消费完成或因 Context 取消而停止；
- 同步消费形成自然背压，慢处理器会延长 `Publish` 耗时，这是首版的明确行为。

### 6.3 错误处理

- 单个处理器返回错误时，继续执行后续处理器；
- `Publish` 使用 `errors.Join` 汇总处理器错误，调用方可使用 `errors.Is/As` 判断；
- 每个错误包装事件类型和订阅 ID，同时保留原始错误链；
- 发布前 Context 已取消时直接返回 `ctx.Err()`；
- 消费过程中 Context 取消时，不再调用后续处理器，并将取消错误与之前的处理器错误合并返回；
- Bus 不恢复处理器 panic，panic 表示编程错误并沿调用栈传播，避免静默隐藏异常。

### 6.4 取消订阅

- `Unsubscribe` 幂等，多次调用安全；
- 取消只影响之后获取的发布快照；
- 已经取得快照的并发 `Publish` 仍可能调用该处理器；
- 处理器可以在消费期间取消自己，不会造成死锁。

## 7. 并发与内部实现

Bus 内部按事件类型保存订阅列表：

```text
map[reflect.Type][]subscriptionEntry

subscriptionEntry:
- id：Bus 内唯一订阅编号
- handler：已适配为接收 event.Event 的内部函数
```

实现策略：

1. `Subscribe` 使用 `reflect.TypeFor[E]()` 获取类型并校验其为具体事件类型；
2. 使用互斥锁保护订阅表和订阅 ID；
3. `Publish` 在读锁内复制目标类型的订阅列表，随后立即释放锁；
4. 在锁外按顺序调用快照中的处理器；
5. `Unsubscribe` 在写锁内按订阅 ID 删除条目；
6. Bus 零值可用，内部 map 在首次订阅时延迟初始化；`NewBus` 作为推荐构造方式；
7. Bus 不创建 goroutine，也没有 `Close` 生命周期。

锁外执行处理器避免慢消费者长期占锁，也允许处理器安全地订阅或取消订阅。多个 goroutine 可以并发调用 `Publish`，因此同一处理器也可能被并发调用；处理器自身必须保证并发安全。

`Bus` 含锁，首次使用后不得复制。

## 8. 参数校验和稳定错误

计划提供可供 `errors.Is` 判断的包级错误：

- `ErrNilBus`：订阅或发布使用 nil Bus；
- `ErrNilEvent`：发布 nil 或带类型的 nil 指针事件；
- `ErrNilHandler`：订阅 nil 处理函数；
- `ErrInvalidEventType`：订阅类型是接口或不是可分发的具体事件类型。

不强制 `Event.Name()` 非空，也不要求名称全局唯一，因为名称不参与投递。名称规范可由具体领域事件自行约束。

## 9. 与 Agent 的集成方式

- 应用启动层创建一个 Bus，先注册订阅者，再将其作为 `event.Publisher` 注入 Agent 运行组件；
- 需要发布事件的组件只保存 `event.Publisher`，不依赖具体 `*event.Bus`；
- 具体运行事件应由未来负责运行语义的领域模块定义和发布；Tool Proxy 或其他独立领域组件仍可按自身契约发布事件。标准运行事件的独立定义方案见[标准运行事件定义模块设计方案](run-event-module-design.md)，当前仍未接入发布流程；
- 普通通知可以使用事件；需要订阅者返回决策并改变执行路径的能力，应设计显式策略或控制接口，不滥用事件系统；
- 首版不在 Bus 内预置 Agent 事件。运行开始、模型增量、工具完成等标准事件随对应运行模块设计后分别补充。

## 10. 测试与验证计划

单元测试至少覆盖：

1. 用户自定义事件可以订阅、发布并读取载荷；
2. 多个处理器按订阅顺序消费；
3. 值类型、指针类型和其他事件类型精确隔离；
4. 无订阅者时发布成功；
5. 一个处理器失败不阻塞其他处理器，返回值包含全部错误；
6. 发布前取消及消费期间取消会返回 Context 错误并停止后续消费；
7. 取消订阅幂等，取消后不再收到新快照中的事件；
8. 处理器在消费时取消自己不会死锁；
9. nil Bus、nil 事件、带类型 nil 事件、nil 处理器和接口订阅类型被拒绝；
10. Bus 零值可用；
11. 并发发布、订阅和取消订阅无数据竞争。

实现后在 `acore` 模块目录执行：

```bash
gofmt -w event/event.go event/bus.go event/bus_test.go
go test ./event
go test ./...
go vet ./...
go test -race ./event
```

## 11. 验收标准

- 外部用户无需修改 `acore` 即可定义和发布自己的事件；
- 订阅处理器以具体事件类型接收数据，无需手工类型断言；
- 发布、消费、错误、取消订阅和 Context 取消语义符合本文档；
- 并发测试和竞态检测通过；
- 不引入第三方依赖，也不修改当前 `model` 模块行为。

## 12. 关键取舍

- **同步而非异步**：首版保证行为可预测、错误可返回并提供自然背压；异步能力留待出现明确需求后单独设计。
- **精确类型而非事件名称路由**：优先类型安全，避免字符串冲突和载荷不一致。
- **通知而非控制协议**：保持事件模块单一职责，审批、恢复等控制流程使用专门接口。
- **显式注入而非全局 Bus**：便于测试和多 Agent 隔离，避免隐藏依赖。
- **领域包定义具体事件**：避免 `event` 包演变为依赖所有模块的事件集合。
