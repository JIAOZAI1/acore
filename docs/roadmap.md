# Acore 开发路线与待办

## 1. 当前状态

Acore 的目标是一套完整的 Agent 运行框架，目前已具备以下基础运行能力：

```text
model / event / tool
          ↓
        runtime
          ↓
        looper
```

已完成模块：

- `model`：厂商无关模型协议、流式事件、Provider Registry；
- `event`：同步进程内事件、通用 `Publisher`；
- `tool`：工具注册、发现、执行和不可变 Proxy 链；
- `runtime`：进程级共享能力组合；
- `looper`：可替换 Loop 和单轮模型调用；
- `internal/provider/openai`：OpenAI Chat Completions 参考实现。

当前仍处于完整框架的基础建设阶段，尚未打通“模型调用工具、推进对话、返回运行结果、暂停审批和恢复执行”的端到端主链路。后续建设不能止步于底层执行能力，还必须提供可直接使用的标准实现、高层 Agent API，以及状态管理、治理和生产化能力。

## 2. 总体依赖顺序

```text
模型协议完善
    ↓
ToolCallingLoop
    ↓
Run ID / Run Result
    ↓
Session / Checkpoint
    ↓
Approval / Resume
    ↓
Observability / Eval / Agent Facade
```

新增模块应继续遵守以下约束：

- `model`、`event`、`tool` 等基础协议不依赖 Runtime；
- Runtime 不是通用 Service Locator；
- Provider、Tool 和 Proxy 按需接收最小接口，不持有完整 Runtime；
- 每次运行的 Context、Run ID、对话和临时状态不进入进程级 Runtime；
- 控制流程使用明确的 Loop、Proxy 或策略接口，不使用普通 Event Handler 代替；
- 新增公开 API 前先明确错误、取消、并发、所有权和恢复语义。

## 3. P0：打通最小 Agent 主链路

### 3.1 完善 Model 协议校验

- [ ] 校验 Message Role 与 Content 的合法组合；
- [ ] 校验 `RoleTool` 必须携带 `ToolCallID`；
- [ ] 校验 ToolCall 的 ID、Name 和 Arguments；
- [ ] 校验 Image 的 URL/Data 二选一和 MIME Type；
- [ ] 校验 ContentKind 与有效字段的对应关系；
- [ ] 定义并校验模型流事件顺序；
- [ ] 增加 ContentStart/Delta/End/Done 状态机契约测试；
- [ ] 校验 Provider 返回的 Model ID 和 Provider ID；
- [ ] 为 Provider 增加可复用的协议契约测试。

### 3.2 实现 ToolCallingLoop

- [ ] 将 `tool.Spec` 转换为 `model.ToolSpec`；
- [ ] 从模型最终消息中提取 ToolCall；
- [ ] 调用 `tool.Service.Execute`；
- [ ] 将 Tool Result 转换成 `RoleTool` 消息；
- [ ] 将新消息追加到复制后的对话上下文；
- [ ] 支持模型与工具的多轮推进；
- [ ] 设置最大模型轮数和最大工具调用次数；
- [ ] 明确多个 ToolCall 的串行或并行策略；
- [ ] 明确工具错误反馈给模型还是直接终止运行；
- [ ] 处理 `ReasonToolUse` 与实际 ToolCall 不一致的协议错误；
- [ ] 发布每轮模型事件和必要的工具事件；
- [ ] 覆盖取消、流错误、工具错误和事件发布错误；
- [ ] 增加并发运行和输入切片隔离测试。

### 3.3 定义运行身份和结果

该项涉及 `Looper.Run` 等公开 API，实施前需要单独评审。

- [ ] 定义 `RunID` 的生成和外部传入规则；
- [ ] 定义运行状态：Completed、Failed、Canceled、Suspended；
- [ ] 定义最终 `RunResult`；
- [ ] 决定 `Looper.Run` 返回 Result 的兼容演进方式；
- [ ] Result 包含最终 Message、Usage、StopReason 和 Tool 执行摘要；
- [ ] 区分运行失败、取消和可恢复暂停；
- [ ] 确定 Result 与流式 Event 的一致性约束。

### 3.4 补充标准运行事件

- [ ] 为 `ModelEvent` 增加 Run ID 和 Turn；
- [ ] 设计 RunStarted、RunCompleted、RunFailed、RunSuspended；
- [ ] 根据需要增加 ModelStarted、ModelCompleted；
- [ ] 确定并发事件的序号和顺序语义；
- [ ] 明确每种事件发布失败是否中断运行；
- [ ] 禁止事件默认携带密钥、完整敏感参数等信息。

### 3.5 提供可公开使用的 Provider

- [ ] 评审 OpenAI Provider 公共 API；
- [ ] 将稳定实现从 `internal/provider/openai` 迁移到 `provider/openai`；
- [ ] 完善配置、凭据和自定义 HTTP Client 的说明；
- [ ] 增加 OpenAI Provider 契约测试；
- [ ] 协议稳定后再增加 Anthropic、Gemini 等 Provider。

## 4. P1：状态、暂停与恢复

### 4.1 Session / Conversation

- [ ] 明确 Session 与一次 Run 的关系；
- [ ] 定义 Session ID、消息历史、版本和元数据；
- [ ] 定义 `session.Store` 最小接口；
- [ ] 实现内存 Store 作为参考；
- [ ] 定义并发更新和乐观锁语义；
- [ ] 明确消息保存失败是否影响运行结果；
- [ ] Session 状态不得直接存入进程级 Runtime。

### 4.2 Checkpoint

- [ ] 定义可序列化 Checkpoint；
- [ ] 保存 Loop 轮次、消息、已完成工具调用和待处理动作；
- [ ] 定义 Save、Load、Delete 接口；
- [ ] 增加版本号和并发恢复保护；
- [ ] 明确 Tool 成功但 Checkpoint 保存失败时的语义；
- [ ] 定义敏感工具参数和结果的持久化规则；
- [ ] 实现内存 Store 作为契约参考。

### 4.3 Approval

Approval 应建立在 Run Result、Suspended 状态和 Checkpoint 之上，不使用普通 Event Handler 代替控制协议。

- [ ] 定义 Approval Request、Decision 和 Policy；
- [ ] 定义 Approved、Rejected、Pending 状态；
- [ ] 设计 Approval Proxy；
- [ ] 需要审批时保存 Checkpoint 并暂停运行；
- [ ] 定义外部提交审批决定的接口；
- [ ] 定义 Resume 流程；
- [ ] 防止同一审批被重复处理；
- [ ] 明确拒绝结果如何反馈给模型；
- [ ] 增加暂停、批准、拒绝、超时和重复恢复测试。

## 5. P2：生产化能力

### 5.1 Tool Event Proxy

- [ ] 使用独立集成包实现 `tool.Proxy`；
- [ ] 通过显式注入的 `event.Publisher` 发布事件；
- [ ] 定义 ToolStarted、ToolCompleted、ToolFailed；
- [ ] 明确事件发布错误与工具错误的优先级；
- [ ] 默认不记录完整敏感参数和结果；
- [ ] 保持 ToolSystem 核心不依赖 EventBus 或 Runtime。

### 5.2 可观测性

- [ ] 增加 `log/slog` 集成；
- [ ] 设计 OpenTelemetry Trace 集成；
- [ ] 关联 Run ID、Trace ID、模型轮次和工具调用 ID；
- [ ] 采集模型耗时、TTFT、Token Usage；
- [ ] 采集工具耗时和错误率；
- [ ] 使用 Provider Decorator、Tool Proxy 和 Loop 集成，避免污染基础协议。

### 5.3 Config

不提供 `Get(string) any` 形式的通用配置中心。

- [ ] 确定首版配置源：环境变量、文件或两者；
- [ ] 各组件继续使用 typed Config；
- [ ] 定义配置解码和校验规则；
- [ ] 定义多配置源覆盖顺序；
- [ ] 凭据通过环境变量或 Secret Provider 注入；
- [ ] Runtime 和 Request 不保存或序列化密钥；
- [ ] 热更新在生命周期和并发语义明确前暂缓。

### 5.4 Eval

Eval 应依赖稳定的 Run Result，不直接拼装底层流事件。

- [ ] 定义 Eval Case、Evaluator、Score 和 Result；
- [ ] 支持最终回答评测；
- [ ] 支持 Tool 调用轨迹评测；
- [ ] 支持 Token、耗时和错误统计；
- [ ] 支持自定义 Evaluator；
- [ ] 评估 LLM-as-a-Judge 的 Provider、成本和重试边界；
- [ ] 实现可重复、无真实网络依赖的 Runner 测试。

### 5.5 Agent Facade

- [ ] 在底层 API 稳定后增加高层 `agent` 包；
- [ ] 提供类型安全的 Builder；
- [ ] 简化 Provider、Tool、Runtime 和 Loop 的装配；
- [ ] 保留底层模块独立使用能力；
- [ ] 禁止演变为 `Register(any)` 或字符串 Service Locator；
- [ ] 明确 Agent 的并发安全和资源所有权。

## 6. P3：按需求扩展

### 6.1 Tool 能力增强

- [ ] 评估 JSON Schema Validator 及支持的 Draft；
- [ ] 定义结构化参数校验错误；
- [ ] 扩展多模态 Tool Result；
- [ ] 评估 JSON、图片、文件和结构化数据结果；
- [ ] 按需增加权限、超时、有界重试、缓存、幂等和脱敏 Proxy；
- [ ] 按具体 Tool 类型设计沙箱，不在 ToolSystem 核心中假设统一沙箱。

### 6.2 Runtime 生命周期

- [ ] 定义组件所有权；
- [ ] 定义启动顺序和逆序关闭；
- [ ] 定义部分启动失败的回滚；
- [ ] 定义并发 Close 和重复 Close；
- [ ] 生命周期协议稳定后再考虑 Runtime `Start/Close`；
- [ ] 热插拔和动态注册保持延后。

### 6.3 Event 扩展

仅在出现明确需求后评估：

- [ ] 异步或并行消费；
- [ ] 事件持久化；
- [ ] 跨进程传输；
- [ ] 重试和死信；
- [ ] Handler 优先级或通配符；
- [ ] 中间件链。

以上能力不应直接加入当前同步 Bus。

## 7. 发布前阻塞项

- [ ] 修正 README 中不存在的根包导入示例；
- [ ] 补充最小无工具 Agent 示例；
- [ ] 补充 ToolCallingLoop 完整示例；
- [ ] 提供仓库外可以导入的公共 Provider；
- [ ] 增加与 README 声明一致的 `LICENSE` 文件；
- [ ] 移除或重新定位 `main/` 演示代码；
- [ ] 为公共 API 补齐文档注释；
- [ ] 执行 `gofmt`、`go vet ./...`、`go test ./...`；
- [ ] 并发模块执行 `go test -race ./...`；
- [ ] 配置 CI，至少覆盖测试和静态检查；
- [ ] 确认 Go 版本和发布环境兼容性；
- [ ] 制定公共 API 版本和兼容策略。

## 8. 建议下一迭代

下一迭代优先设计并实现 `ToolCallingLoop`。验收目标：

1. 模型可以收到 ToolSystem 中的工具定义；
2. 模型返回 ToolCall 后可以执行对应工具；
3. 工具结果可以作为 `RoleTool` 消息进入下一轮；
4. 模型返回最终 Assistant Message 后正常结束；
5. 最大轮数、取消、模型错误、工具错误和事件错误均有明确行为；
6. 输入上下文不会被 Loop 原地修改；
7. 单元测试覆盖单工具、多轮、异常和并发场景。
