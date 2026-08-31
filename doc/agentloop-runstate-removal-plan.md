# AgentLoop 与 RunState 移除方案

## 1. 背景与结论

现有 `acore/agentloop`、`acore/agentloop/singleloop` 和 `acore/runstate` 的职责边界及运行模型需要重新设计。为避免旧实现和旧文档继续形成约束，本次先完整移除该部分，不提供兼容层、占位接口或替代实现。

## 2. 目标与非目标

### 2.1 目标

- 删除 AgentLoop、SingleLoop、RunState 的全部生产代码和测试；
- 删除以现有 AgentLoop/RunState 架构为前提的设计文档；
- 清理保留文档中对现有实现及已删除文档的失效引用；
- 保持 `model`、`tool`、`event` 及其通用内部包可独立编译和测试；
- 为后续重新设计保留无旧 API 兼容负担的代码基线。

### 2.2 非目标

- 本次不提出新的运行循环、状态、会话、持久化或 Agent Builder 方案；
- 不保留 `agentloop.Looper`、`agentloop.Input/Result`、`runstate.State/Factory` 等类型别名或空壳；
- 不修改 `model`、`tool`、`event` 的现有公共 API；
- 不删除仅在概念层提到未来编排者的独立 Tool/Event 设计内容；
- 不处理与本次范围无关的工作区既有修改。

## 3. 删除范围

### 3.1 代码

完整删除：

```text
acore/agentloop/
acore/runstate/
```

这包括根 Looper 协议、SingleLoop Builder 与执行实现、RunState 状态机、事件、Factory，以及对应测试和示例。

当前仓库依赖检查显示，`agentloop` 与 `runstate` 之外的 Go 包没有导入这两个包；删除后剩余模块为 `model`、`tool`、`event` 和仍被它们使用的 `internal/*` 包。

### 3.2 设计文档

删除以下完全描述旧架构或直接依赖旧架构的文档：

```text
doc/agent-builder-design.md
doc/agent-loop-design.md
doc/agent-loop-event-naming-design.md
doc/agent-loop-looper-runstate-layering-design.md
doc/agent-loop-state-machine-interface-design.md
doc/agent-loop-state-refactor-design.md
doc/runstate-event-publishing-design.md
```

其中 `agent-builder-design.md` 虽然属于上层 Agent 组装设计，但其核心契约、数据流和默认构建路径都建立在 `agentloop.Looper`、`singleloop.Builder` 与 `runstate.Factory` 上，旧基础被移除后该方案不再成立，因此一并删除。

## 4. 保留文档清理

- `doc/event-module-design.md`：删除“RunState 已负责运行事件”的集成更新和现状描述，保留独立 Event Bus 设计；
- `doc/eino-checkpoint-persistence-analysis.md`：删除基于当前 RunState/SingleLoop/Snapshot 的本地映射结论，保留对 Eino Checkpoint 的独立分析和不绑定具体执行器的通用启示；
- `doc/tool-system-design.md`：保留。其中 Agent Loop 仅表示未来可能的编排调用方，不声明当前存在某个具体包或接口，不构成对旧实现的依赖。

清理后，保留文档不得链接已删除文档，也不得声称 `acore` 当前存在 AgentLoop、SingleLoop 或 RunState 实现。

## 5. 兼容性与取舍

本次是有意的源码级不兼容删除：任何外部调用方对下列路径和 API 的引用都会编译失败：

- `github.com/JIAOZAI1/acore/agentloop`；
- `github.com/JIAOZAI1/acore/agentloop/singleloop`；
- `github.com/JIAOZAI1/acore/runstate`。

不增加 deprecated 别名或临时适配层，因为这些兼容入口会继续固化待重新思考的抽象。后续新设计应从实际需求重新定义模块边界、运行输入输出、状态所有权、事件职责和恢复语义。

## 6. 风险

- 仓库外调用方可能仍依赖被删除 API，本仓库无法完整检测；
- 保留文档中的“Agent Loop”概念可能被误认为现有模块，因此只保留明确面向未来调用方的描述；
- `acore` 当前存在大量与本次无关的既有删除和修改，实施时只删除上述未跟踪目录并精准编辑指定文档，不回滚或覆盖其他变更。

## 7. 验证计划

在 `acore` 模块执行：

```bash
go test ./...
go vet ./...
go list ./...
```

在工作区执行引用检查：

```bash
rg -n 'acore/(agentloop|runstate)|agent-loop-(design|event-naming|looper-runstate-layering|state-machine-interface|state-refactor)-design|runstate-event-publishing-design' acore doc agent
```

预期结果：

- `go list` 不再列出 `agentloop`、`agentloop/singleloop` 或 `runstate`；
- 剩余 Go 包测试和静态检查通过；
- 不再存在对被删除包路径或被删除设计文档的引用。
