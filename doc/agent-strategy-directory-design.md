# Agent 运行策略文件分布调整方案

状态：已实施（2026-08-26）

## 1. 背景

当前 `acore/agent` 将通用 Agent 契约、Builder、运行入口、SingleTurn 和 ToolLoop 的实现及测试放在同一目录、同一 `agent` 包中。随着策略数量增加，策略专属文件会继续堆积，策略之间的边界也不够直观。该问题已按本文方案完成调整。

目标目录为新增 `acore/agent/agent-strategy/`，并在其下按具体策略拆分子目录。

## 2. 目标与非目标

### 目标

- 将策略实现和策略测试按策略归档到独立目录。
- 保留 `acore/agent` 作为 Agent 公共契约、Builder 组装和运行入口边界。
- 保留现有模块外 API（例如 `agent.NewSingleTurnStrategy`、`agent.NewToolLoopBuilder`），避免本次目录调整成为无关的破坏性 API 变更。
- 让未来新增策略只需要增加一个策略子目录，并复用稳定的公共契约。

### 非目标

- 不改变 RunStrategy、RunInput、Stream、Agent 事件、错误或运行行为。
- 不在本次调整中新增策略，也不接入运行事件 Publisher。
- 不把策略专属依赖提升为 Agent 全局依赖。

## 3. 推荐目录结构

```text
acore/agent/
├── agent.go                 # Agent 公共契约、RunInput、Stream、公共错误
├── builder.go               # 通用 Agent Builder
├── run.go                   # configuredAgent 运行入口
├── strategycontract/        # 策略实现共享的低层契约/辅助入口
│   └── ...
└── agent-strategy/
    ├── singleturn/
    │   ├── strategy.go
    │   ├── builder.go
    │   ├── session.go       # 仅当逻辑确实专属于该策略时保留
    │   └── *_test.go
    └── toolloop/
        ├── strategy.go
        ├── builder.go
        ├── convert.go
        ├── usage.go
        ├── session.go       # 仅当逻辑确实专属于该策略时保留
        └── *_test.go
```

`agent-strategy` 仅作为目录层级，不单独承载运行实现。子目录使用明确的 Go 包名：`singleturn` 和 `toolloop`。

## 4. 依赖与兼容性设计

Go 子包不能导入其父包后再由父包反向导入子包，否则会产生 import cycle。为此采用以下边界：

1. 新增 `agent/strategycontract`，承载策略实现必须依赖的低层公共类型和稳定错误；它不得导入具体策略包。
2. `agent` 对外继续提供现有类型和构造函数，通过类型别名或薄转发入口兼容旧 API。
3. `agent/agent-strategy/singleturn` 和 `agent/agent-strategy/toolloop` 只依赖 `strategycontract` 及其领域依赖（`model`、`session`、`tool`、`contextwindow`），不得反向导入 `agent`。
4. `agent` 的 Builder 只依赖 `RunStrategy` 接口，不识别具体策略类型；兼容构造函数由独立适配文件提供，不把策略选择逻辑重新塞回 Builder。

依赖关系：

```text
agent/strategycontract  ←  singleturn
          ↑             ←  toolloop
          ↑
       agent（兼容别名、通用 Builder、运行入口）
```

如果实施前确认可以接受 API 路径变化，则可省略兼容别名，让调用方直接使用 `agent/agent-strategy/singleturn` 和 `agent/agent-strategy/toolloop`；默认不采用该路线。

## 5. 文件迁移映射

| 当前文件 | 目标位置 | 说明 |
| --- | --- | --- |
| `single_turn.go` | `agent-strategy/singleturn/strategy.go` | 单轮运行流程 |
| `single_turn_builder.go` | `agent-strategy/singleturn/builder.go` | 单轮 Builder |
| `single_turn_test.go` | `agent-strategy/singleturn/strategy_test.go` | 单轮运行测试 |
| `tool_loop.go` | `agent-strategy/toolloop/strategy.go` | Tool Loop 主循环 |
| `tool_loop_builder.go` | `agent-strategy/toolloop/builder.go` | Tool Loop Builder、限制和错误模式 |
| `tool_loop_convert.go` | `agent-strategy/toolloop/convert.go` | Tool 调用及消息转换 |
| `tool_loop_usage.go` | `agent-strategy/toolloop/usage.go` | Usage 累加 |
| `tool_loop_test.go`、`tool_loop_builder_test.go` | `agent-strategy/toolloop/*_test.go` | Tool Loop 测试 |
| `tool_loop_example_test.go` | `agent-strategy/toolloop/example_test.go` | Tool Loop 示例 |
| `session.go`、`context_window.go` | 先保留在 `agent`，再按实际共享性决定 | 避免把通用 Session/窗口辅助误归入某一策略 |

测试辅助若只服务一个策略，随策略迁移；跨策略测试辅助保留在 `agent` 或抽到测试专用文件，不能引入生产包循环依赖。

## 6. API 兼容方案

旧调用保持有效：

```go
strategy := agent.NewSingleTurnStrategy()
toolLoop, err := agent.NewToolLoopBuilder().Build()
```

新增目录也提供直接导入入口：

```go
import "github.com/JIAOZAI1/acore/agent/agent-strategy/singleturn"

strategy := singleturn.NewStrategy()
```

兼容入口只做类型别名或构造函数转发，不复制策略逻辑。新入口的具体命名在实施前以现有公开 API 和 Go 命名习惯为准，避免同时保留两套可变实现。

## 7. 实施步骤

1. 先抽取 `strategycontract`，定义最小公共依赖，保持错误身份和接口签名不变。
2. 迁移 SingleTurn，建立独立包测试，并由 `agent` 提供兼容入口。
3. 迁移 ToolLoop 及其测试、示例，处理共享辅助函数的归属。
4. 更新仓库内示例、设计文档和包注释中的导入路径说明。
5. 使用 `gofmt`、`go test ./...`、`go vet ./...` 验证；涉及并发策略时补充 `go test -race ./...`。

## 8. 风险与取舍

- **循环依赖风险**：通过低层 `strategycontract` 和单向依赖解决；不允许子策略包导入父 `agent`。
- **公开类型迁移风险**：优先使用别名保持 `agent` 路径兼容；若别名无法表达某些错误或私有字段，需要先扩大契约，再迁移实现。
- **共享辅助误归类**：`session.go`、`context_window.go` 先不强行移动，待依赖分析后再拆，避免为了目录整齐引入重复逻辑。
- **改动范围较大**：本次只做物理归档和依赖解耦，不修改运行语义；每个策略迁移后单独测试再进行下一步。

## 9. 实施结果

- 公共契约和共享辅助已迁移到 `acore/agent/agent-strategy` 包。
- SingleTurn 实现已迁移到 `acore/agent/agent-strategy/singleturn`。
- ToolLoop 实现已迁移到 `acore/agent/agent-strategy/toolloop`。
- `acore/agent` 保留类型别名、构造函数和错误别名，现有调用方无需修改。
- 未改变 RunStrategy、RunInput、Stream、事件、错误和运行时行为。

## 10. 待确认事项

1. 是否采用 `acore/agent/agent-strategy/singleturn` 与 `acore/agent/agent-strategy/toolloop` 两个策略子目录。
2. 是否保留 `agent.NewSingleTurnStrategy`、`agent.NewToolLoopBuilder` 等现有入口作为兼容 API。
3. 是否接受新增 `agent/strategycontract` 作为低层契约包，以避免 Go import cycle。
4. `session.go` 和 `context_window.go` 是否先留在 `agent`，待后续按共享性单独拆分。
