# CloudWeGo Eino Checkpoint 持久化抽象分析

## 1. 分析范围与结论

本文基于 CloudWeGo Eino `v0.9.15`（commit `ebd616c8291e957684ea6ca99dd54225d04e0438`）源码分析其持久化设计。上游采用 Apache-2.0 许可证。

Eino 核心仓库中的“持久化抽象”主要指 **Interrupt/Resume 使用的 Checkpoint 机制**，不是通用数据库 ORM、会话记忆或事件溯源系统。

核心结论：

1. Eino 将持久化后端压缩为一个只读写 `[]byte` 的最小 KV 接口；运行时快照结构和序列化均由框架控制。
2. Checkpoint 是一次执行的 **Memento（控制流快照）**，记录恢复执行所需的状态、待执行任务、通道值、嵌套子图和中断状态。
3. `checkpoint ID` 标识整次执行的持久化槽位；`interrupt ID` 标识一次快照中的具体中断点。两者职责不同。
4. Resume 不是恢复 goroutine 或程序计数器，而是重建运行状态，并从安全点继续或重跑节点。
5. 该设计很适合协作式中断、人工审批和可控的图恢复，但不提供 exactly-once、副作用事务、并发 Resume 防重、CAS、租约或通用崩溃恢复。
6. 对 `acore` 后续设计，最值得借鉴的是“Store、Codec、运行时 Memento、Resume 路由分层”；不应把面向调用方的结果快照直接当作可恢复的运行时快照。

## 2. 上游核心接口

Eino 在 `internal/core/interrupt.go` 定义实际接口，并分别在 `compose`、`adk` 中通过类型别名公开：

```go
type CheckPointStore interface {
    Get(ctx context.Context, checkPointID string) ([]byte, bool, error)
    Set(ctx context.Context, checkPointID string, checkPoint []byte) error
}
```

另有可选能力：

```go
type CheckPointDeleter interface {
    Delete(ctx context.Context, checkPointID string) error
}
```

这个接口有几个明确取舍：

- Store 不理解 Checkpoint 结构，只保存 opaque bytes；
- `Get` 用 `existed bool` 区分“不存在”和存储错误；
- 序列化不属于 Store；
- 基础接口没有 List、TTL、版本、CAS、事务或锁；
- Delete 是可选扩展，而且当前主要由 ADK TurnLoop 使用，Compose Graph 和普通 Runner 不会统一清理成功后的旧快照。

这使 Redis、数据库、对象存储或内存 Map 都容易适配，但一致性、生命周期和多租户安全均留给实现方或上层。

## 3. 分层结构

```text
调用层
  Compose Runnable / ADK Runner
          │
          │ checkpoint ID、resume targets
          ▼
运行控制层
  Graph Runner / Agent Runner
  - 识别 Interrupt
  - 选择恢复或新运行
  - 决定重跑哪些任务
          │
          ▼
快照层
  private checkpoint / ADK serialization
  - 运行状态
  - 待执行输入
  - 调度通道
  - 中断地址和局部状态
          │
          ▼
Codec 层
  Compose Serializer / ADK gob
          │
          ▼
Store 层
  Get(key) / Set(key, []byte) / optional Delete(key)
```

### 3.1 Store 与 Codec 分离

Compose 提供独立的：

```go
type Serializer interface {
    Marshal(v any) ([]byte, error)
    Unmarshal(data []byte, v any) error
}
```

默认 `InternalSerializer` 使用带类型信息的内部结构并通过 Sonic 编码。自定义状态或放入接口字段的具体类型需要通过 `schema.Register` 或 `schema.RegisterName` 注册。

ADK 虽复用同一个 `CheckPointStore`，但其顶层快照直接使用 `encoding/gob`。因此 Eino 实际统一的是 **blob storage contract**，并没有统一 Compose 与 ADK 的 payload schema。

### 3.2 快照结构保持私有

Compose 的私有 `checkpoint` 主要包含：

- `Channels`：图调度通道及依赖值；
- `Inputs`：恢复时待执行节点的输入；
- `State`：图级本地状态；
- `SkipPreHandler`：恢复子图时是否跳过已执行的前置处理；
- `RerunNodes`：恢复后需要重跑的节点；
- `SubGraphs`：递归嵌套的子图快照；
- `InterruptID2Addr`：中断 ID 到层次化执行地址的映射；
- `InterruptID2State`：中断 ID 到组件局部状态的映射。

快照不作为公共业务模型暴露，避免 Store 实现依赖运行引擎内部结构。这是合理的封装，但也意味着持久化格式升级完全由框架负责。

## 4. ID 与执行地址设计

Eino 同时使用三类标识：

| 标识 | 作用 | 产生方 |
|---|---|---|
| Checkpoint ID | Store 中整次执行快照的 Key | 调用方，例如 session/run ID |
| Interrupt ID | 一次快照内可被定向恢复的中断点 | 框架生成 UUID |
| Address | 中断点在嵌套执行结构中的位置 | 框架按运行路径构造 |

Address 是层次化路径，例如：

```text
runnable:root;node:tools;tool:book_ticket:call_1
```

`ResumeWithData`/`BatchResumeWithData` 使用 interrupt ID 选择恢复目标，并附带用户输入；框架再通过已持久化的 `interrupt ID -> Address` 映射，将恢复状态和数据路由到具体节点、工具或子 Agent。

这个区分非常重要：

- Checkpoint ID 回答“加载哪次 Run”；
- Interrupt ID 回答“这次 Run 中恢复哪个暂停点”；
- Address 回答“恢复数据应沿哪条嵌套路径传播”。

源码部分注释把 Interrupt ID 描述为地址字符串，但当前实现实际生成 UUID，并将 Address 单独保存。使用时应以实际类型和实现为准。

## 5. 保存与恢复流程

### 5.1 保存

```text
节点执行或外部取消
        │
        ▼
产生 InterruptSignal 树
        │
        ├── 用户信息 Info（用于本次中断响应）
        ├── 组件局部 State
        ├── 当前 Address
        └── 子中断 Signals
        │
        ▼
Graph/Runner 在安全点收敛并发任务
        │
        ▼
构建控制流快照
  - 已完成输出进入 Channels
  - 未完成节点进入 RerunNodes
  - 待执行输入进入 Inputs
  - 复制图级 State
  - 嵌套子图快照递归嵌入
        │
        ▼
序列化为 []byte
        │
        ▼
Store.Set(checkpointID, bytes)
```

Compose 仅在中断边界保存，并非每一步持续落盘。ADK 也主要在 Interrupt 或支持 Checkpoint 的 Cancel 路径保存。

### 5.2 恢复

```text
调用方使用 checkpoint ID 再次 Invoke，或 Runner.Resume
        │
        ▼
Store.Get(checkpointID)
        │
        ▼
反序列化快照
        │
        ├── 恢复 Channels 和图级 State
        ├── 恢复待执行 Inputs / RerunNodes
        ├── 将子快照逐层转发给 SubGraph
        └── 将 interrupt 地址和状态注入恢复上下文
        │
        ▼
按 Address 将 State 和 ResumeData 路由到组件
        │
        ▼
组件通过 GetInterruptState / GetResumeContext 判断行为
        │
        ▼
继续执行或再次 Interrupt
```

Resume 的本质是“恢复数据后重新进入执行器”，不是恢复原进程栈。

## 6. 关键语义

### 6.1 基本中断与有状态中断

- `Interrupt(ctx, info)`：通知调用方暂停原因，但不保存组件局部状态；
- `StatefulInterrupt(ctx, info, state)`：额外持久化组件恢复所需状态；
- `CompositeInterrupt(...)`：聚合工具并行、嵌套图或多 Agent 的多个中断点。

`info` 主要面向用户交互，`state` 面向恢复执行。两者分离能避免把 UI 数据误当运行状态。

### 6.2 定向恢复

恢复组件读取：

- `GetInterruptState[T]`：自己是否曾中断，以及原中断保存的状态；
- `GetResumeContext[T]`：自己或后代是否是恢复目标，以及用户新提供的数据。

未被选中的叶子中断点应再次中断，以保留未处理状态；复合组件则继续向下执行，让恢复信号到达目标后代。

### 6.3 重跑而非继续机器指令

发生动态中断或超时取消时，运行器会把相关节点放进 `RerunNodes`。因此节点可能重新执行：

- 已完成节点的结果尽量先写入 Channels；
- 未完成或产生中断的节点通常重新进入；
- 子图通过嵌套 Checkpoint 从更细粒度位置恢复；
- 外部中断默认等待运行中任务结束，超时后未完成任务在恢复时重跑。

这要求有外部副作用的节点自行保证幂等或使用业务幂等键。Eino 的 Store 抽象本身不解决 exactly-once。

### 6.4 输入保存策略并不统一

- 图外部中断可能发生在任意时刻，因此 `WithGraphInterrupt` 会自动保存节点输入；
- 节点内部主动调用 `Interrupt` 时，框架默认不统一保存原输入；组件应使用图 State 或 `StatefulInterrupt` 显式保存恢复所需数据。

这是兼容性和存储成本之间的取舍，也说明 Checkpoint 不是简单地“把所有内存对象全部序列化”。

### 6.5 嵌套图采用单 blob 原子快照

子图 Checkpoint 递归嵌入父图，最终由根图一次 `Set` 写入：

优点：

- 根执行只有一个 Key；
- 不需要跨多个 Store Key 的事务；
- 父子状态天然来自同一个保存点。

代价：

- 快照越深、状态越大，写放大越明显；
- 无法局部读取或更新子图；
- 任一嵌套类型不兼容都会导致整体恢复失败。

### 6.6 流式数据需要物化

Checkpoint 不能直接保存活跃 StreamReader。Compose 会先把流片段 concat 成普通值，保存后再恢复成 StreamReader。自定义流类型需要注册 concat 逻辑。

这会带来额外内存和延迟成本，也意味着“恢复后的流”是物化值的重放，不是原网络流的继续读取。

## 7. 设计优点

1. **后端接口极小**：任何能可靠保存 bytes 的系统都可接入。
2. **存储与运行时解耦**：Store 不依赖 Graph、Agent 或用户状态类型。
3. **快照属于执行器**：运行器最了解任务、通道和安全点，不把恢复逻辑下沉到数据库适配器。
4. **支持嵌套与多中断点**：Signal 树、Address 和 SubGraphs 可以表达复杂执行结构。
5. **用户信息、旧状态、新恢复数据分离**：HITL 语义清晰。
6. **读写 Key 可分离**：`WithWriteToCheckPointID` 支持从旧快照读取、向新 Key 写入，类似执行分支。
7. **允许强制新运行**：`WithForceNewRun` 可忽略同 Key 的历史快照。
8. **有迁移入口**：`MigrateCheckpointState` 可递归迁移根图和子图的 State。

## 8. 局限与生产风险

### 8.1 没有并发控制

`Get + Set` 没有 revision/CAS。两个进程同时 Resume 同一 Checkpoint 时可能：

- 重复执行工具或外部副作用；
- 后写覆盖先写；
- 从同一旧状态分叉却写回同一个 Key。

Store 实现即使内部加锁，也很难在现有接口中表达“仅当 revision 未变化时保存”。生产系统通常还需要租约、幂等令牌或业务侧状态机。

### 8.2 没有显式 envelope/version

Compose 的内部 Checkpoint 没有统一公开的版本字段、校验和、创建时间或框架版本。`MigrateCheckpointState` 只迁移 State，不覆盖整个运行时 schema。

ADK 中已经出现 gob 类型名兼容、旧版本字节替换和专门迁移逻辑，说明 `any + gob + 类型注册` 的长期兼容成本较高。

### 8.3 生命周期管理较弱

普通 Graph/Runner 成功完成后不会统一删除旧 Checkpoint。若继续使用相同 ID，可能再次加载旧快照。Store owner 必须制定：

- TTL；
- 成功完成后的删除；
- 审批超时后的回收；
- 审计归档；
- `WithForceNewRun` 的使用规则。

可选 `CheckPointDeleter` 只解决接口能力，不等价于完整生命周期策略。

### 8.4 Checkpoint 不是业务事务

快照写入与工具/API/数据库副作用之间没有原子事务。典型故障窗口包括：

```text
外部副作用成功 -> 进程退出 -> 新快照尚未保存 -> 恢复后重复执行
```

因此该设计更准确地说是“协作式控制流恢复”，不能直接当作通用容错工作流引擎。

### 8.5 Opaque blob 限制运维能力

Store 无法直接查询：

- 当前暂停在哪个工具；
- 谁在等待审批；
- 快照版本和大小；
- 是否已完成或过期。

生产实现通常需要在 blob 之外维护可查询元数据，或者定义带 metadata 的持久化 Record。

### 8.6 安全责任全部在外层

Checkpoint 可能包含完整消息、工具参数、模型输出和业务状态。接口没有内建：

- 租户命名空间；
- 访问控制；
- 加密；
- 敏感字段过滤；
- 大小限制。

不能直接把调用方提供的 ID 当作授权依据。

## 9. 对 acore 后续设计的启示

当前不预设 `acore` 后续运行编排和状态抽象。若未来引入 Checkpoint，应先区分面向调用方的结果、进程内运行状态与可恢复控制流快照，不能因为它们都包含消息或步骤信息就复用同一结构。

### 9.1 恢复快照需要独立 Memento

真正恢复通常需要考虑：

- 当前执行阶段和待执行任务；
- 请求中消息之外的配置；
- 已授权工具定义或其配置指纹；
- 待处理调用、活动调用和执行位置；
- 恢复点位于外部副作用之前还是之后；
- 可持久化、可版本化的错误表示。

这些信息通常不适合暴露在面向调用方的普通结果中，因此不能直接把结果 JSON 编码后作为 Checkpoint。

### 9.2 不建议让领域状态对象直接承担持久化

若让细粒度状态操作在每次迁移中直接访问远程 Store，会导致：

- 领域状态和 I/O、重试、超时耦合；
- 每个状态操作都隐含持久化提交语义；
- 执行器无法统一选择安全点；
- 工具副作用与状态保存顺序仍然没有解决。

更合理的方向是由未来的运行执行器在明确安全点导出私有 Memento，经 Codec 写入 Store；恢复时通过专门的恢复流程重建运行上下文。

### 9.3 建议借鉴的部分

1. Store 只负责持久化，不理解 Agent 状态；
2. Codec 与 Store 分离；
3. 持久化结构使用独立 Memento，不复用面向调用方的结果；
4. Run ID 与一次 Run 内的 Interrupt ID 分离；
5. 明确区分旧 Interrupt State 和用户本次 Resume Data；
6. Resume 是重建状态并重入执行器，不是恢复调用栈；
7. 在引入格式前先定义 schema version 和迁移策略；
8. 只有执行器能定义安全保存点和重跑语义。

### 9.4 不建议直接照搬的部分

1. 若目标包含多实例 Resume，只有 `Get/Set` 太弱，应提前评估 revision/CAS/lease；
2. 不建议以大量 `any` 和全局类型注册作为长期存储协议；
3. 不应等到格式不兼容后再补版本迁移，建议一开始加入 envelope version；
4. 不应依赖 Store owner 猜测清理时机，应在上层明确成功、失败、过期策略；
5. 尚无嵌套 Graph/Agent 的明确需求时，不必提前复制 Eino 的 Address 树全部复杂度；
6. 不应把“人工审批恢复”和“进程崩溃后工具 exactly-once 恢复”视为同一个需求。

## 10. 建议的后续决策顺序

若当前项目准备引入持久化，建议先确认目标，再单独形成实现方案：

1. **能力范围**：只支持人工审批/主动暂停，还是还要支持进程崩溃恢复；
2. **安全点**：模型生成后、工具执行前、单个工具完成后，分别是否保存；
3. **副作用语义**：工具是否要求幂等键，是否允许 at-least-once；
4. **并发语义**：同一 Run 是否允许多个实例 Resume，是否需要 CAS 或 lease；
5. **生命周期**：完成后删除、保留审计还是设置 TTL；
6. **持久化模型**：定义带版本的私有 Memento，而不是复用 Snapshot；
7. **模块边界**：Store/Codec 放独立持久化模块，未来的运行执行器负责 capture/restore；
8. **兼容性**：版本、校验、迁移和旧数据拒绝策略；
9. **安全性**：租户 Key、加密、敏感数据、大小限制和权限检查。

在这些语义未确认前，不建议预先增加状态存储接口或修改尚未确定的运行状态抽象。

## 11. 主要源码参考

- CheckPointStore / CheckPointDeleter：<https://github.com/cloudwego/eino/blob/v0.9.15/internal/core/interrupt.go>
- Compose checkpoint、Serializer、Memento：<https://github.com/cloudwego/eino/blob/v0.9.15/compose/checkpoint.go>
- Graph 保存与恢复控制流：<https://github.com/cloudwego/eino/blob/v0.9.15/compose/graph_run.go>
- Interrupt API：<https://github.com/cloudwego/eino/blob/v0.9.15/compose/interrupt.go>
- Resume API：<https://github.com/cloudwego/eino/blob/v0.9.15/compose/resume.go>
- Address 与恢复路由：<https://github.com/cloudwego/eino/blob/v0.9.15/internal/core/address.go>
- 类型注册：<https://github.com/cloudwego/eino/blob/v0.9.15/schema/serialization.go>
- ADK checkpoint 编解码：<https://github.com/cloudwego/eino/blob/v0.9.15/adk/interrupt.go>
- ADK Runner Resume：<https://github.com/cloudwego/eino/blob/v0.9.15/adk/runner.go>
