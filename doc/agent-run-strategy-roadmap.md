# Agent 运行策略路线图

## 1. 目的

本文档记录 `acore/agent` 可逐步实现的运行策略、依赖关系和优先级，作为后续专项设计入口。路线图只定义方向，不直接授权一次性实现全部策略；每个策略进入编码前仍需检查当时的实际契约、编写专项方案并经用户确认。

项目继续遵循以下边界：

- `Agent` 是稳定调用契约；
- `RunStrategy` 是可替换的运行算法；
- Agent Builder 只组装共享 LLM、RunStrategy、Prompt Renderer 和模型参数；
- 策略专属组件由策略自己的构造函数或 Builder 组装；
- 新公开组件必须可导出，公开 API 不依赖 `internal` 或非导出类型；
- 新增策略不能要求通用 Builder 判断具体策略类型。

## 2. 策略与普通组件的边界

只有负责“何时调用模型、工具或其他 Agent，以及何时结束”的完整运行算法才定义为 `RunStrategy`。

以下能力通常不是独立运行策略：

| 能力 | 推荐形态 | 原因 |
|---|---|---|
| 超时、重试、限流 | LLM/Tool Proxy 或装饰器 | 横切单次调用，不决定完整运行流程 |
| 日志、Tracing、指标 | Proxy、Publisher 或 Observer | 只观察或包裹执行，不拥有终止条件 |
| Session、History、Memory | 上下文组件 | 负责加载、保存或裁剪数据 |
| 检索器、评分器、审批服务 | 可注入组件 | 由一个或多个策略组合使用 |
| Prompt 模板 | 已实现的 `prompt.Renderer` 组件 | 不应仅因 Prompt 不同创建策略类型；详见 `prompt-module-design.md` |
| ReAct | 首先作为 ToolLoop 的 Prompt/配置变体 | 基础控制流仍是模型与工具循环 |

若某项能力开始控制多阶段运行、拥有独立状态和终止条件，再评估提升为策略。

## 3. 策略清单

| 策略 | 状态 | 核心流程 | 主要依赖 | 建议阶段 |
|---|---|---|---|---|
| `SingleTurnStrategy` | 已实现 | Model → Done | `model.LLM` | M1 |
| `ToolLoopStrategy` | 已实现 | Model ↔ Tool，直到无 Tool Call | `tool.Service`、运行限制 | M2 |
| `StructuredOutputStrategy` | 待设计 | Generate → Validate → Repair | Schema Validator、修复限制 | M3 |
| `ReflectionStrategy` | 待设计 | Draft → Critique → Revise | 可选 Critic LLM、轮次限制 | M4 |
| `RouterStrategy` | 待设计 | Classify → Select Strategy → Run | 子策略集合、Router、Fallback | M5 |
| `PlanExecuteStrategy` | 待设计 | Plan → Execute Steps → Synthesize | Planner、Tool、计划状态 | M6 |
| `BestOfNStrategy` | 待设计 | Parallel Generate → Score → Select | Scorer、并发与候选限制 | M7 |
| `RAGStrategy` | 待评估 | Retrieve → Augment → Generate | Retriever、上下文预算 | M8 |
| `HandoffStrategy` | 待设计 | Agent → Handoff → Agent | Agent Registry、Handoff 协议 | M9 |
| `WorkflowStrategy` | 待设计 | Fixed Steps / DAG → Aggregate | Workflow/Graph、调度与节点状态 | M10 |
| `HumanApprovalStrategy` | 待设计 | Run → Pause → Approve → Resume | Checkpoint、Approval Service | M11 |

阶段编号表示依赖顺序，不表示发布时间。

## 4. 各策略的预期边界

### 4.1 SingleTurnStrategy

当前基线策略：只执行一次模型生成，保留模型流、Usage 和 Tool Call，但不执行工具。它同时是其他策略的协议行为参照。

### 4.2 ToolLoopStrategy

循环执行模型请求和工具调用，直到模型输出不再包含 Tool Call。首版采用同轮多 Tool Call 串行执行、显式调用预算、安全错误回馈和累计 Usage。专项方案见 `agent-tool-loop-strategy-design.md`。

### 4.3 StructuredOutputStrategy

要求最终输出符合指定 JSON Schema 或 Validator。校验失败时可在有限轮次内把脱敏错误反馈给模型修复。Validator 是独立组件，不能在核心中自行实现不完整的 JSON Schema 标准。

该能力也可能实现成包裹其他策略的策略装饰器；专项设计时需决定它是否只包裹单次模型生成，还是校验任意子策略的最终结果。

### 4.4 ReflectionStrategy

至少包含初稿、批评和修订三个阶段。首版可复用同一个 LLM，通过阶段 Prompt 区分角色；只有出现明确需求时再增加命名 LLM 或 Model Pool。

需定义批评内容是否进入 GeneratedMessages、Usage 如何累计、修订轮次和提前终止条件。

### 4.5 RouterStrategy

根据请求选择一个已构建子策略。至少存在两个可用业务策略后才实现，避免提前建立只有一个分支的空路由。

需定义路由错误、Fallback、子策略事件透传、递归路由限制和策略标识。Router 不得通过修改共享 Builder 在运行期切换组件。

### 4.6 PlanExecuteStrategy

先生成结构化计划，再逐步执行，最后综合结果。Planner 与 Executor 的职责要分离，计划必须有稳定 DTO、步骤状态、最大步骤数和失败策略。

首版不与 WorkflowStrategy 合并：PlanExecute 的步骤由模型动态生成，Workflow 的结构由应用静态定义。

### 4.7 BestOfNStrategy

并发或串行生成多个候选，通过 Scorer 选择最终输出。需要限制候选数、并发数和总 Token 预算，并定义部分候选失败是否允许继续。

Scorer 应是公开小接口，可以是规则、模型或应用实现；核心不能硬编码评分算法。

### 4.8 RAGStrategy

检索通常更适合作为上下文增强组件。如果流程只有“检索后单次生成”，优先组合 Retriever 与现有策略；只有检索、评估、重写查询和再检索形成完整循环时，才实现独立 RAGStrategy。

需定义文档 DTO、引用、去重、排序、上下文预算和敏感数据边界。

### 4.9 HandoffStrategy

允许当前 Agent 把任务交给另一个已构建 Agent。需要 Agent Registry、Handoff Request/Result、最大转交次数和循环检测。

Handoff 是 Agent 级委派，不应伪装成普通 Tool Call；事件需要明确源 Agent、目标 Agent 和关联 ID。

### 4.10 WorkflowStrategy

执行应用预定义的顺序步骤或 DAG。需要节点输入输出、调度器、错误传播、并发汇合和状态快照。只有节点和图契约稳定后再评估持久化与恢复。

### 4.11 HumanApprovalStrategy

在工具副作用、计划执行或高风险动作前暂停，等待外部批准后恢复。它依赖可靠的 Checkpoint/Resume，不应通过阻塞 goroutine 长时间等待实现。

需定义审批请求 DTO、幂等恢复、过期、拒绝和授权边界。

## 5. 推荐实现顺序

```text
SingleTurn（已完成）
        │
        ▼
ToolLoop（已完成）
        │
        ├──► StructuredOutput
        ├──► Reflection
        │        │
        │        ▼
        └──► Router
                 │
                 ├──► PlanExecute
                 ├──► BestOfN
                 └──► RAG
                          │
                          ▼
              Handoff / Workflow
                          │
                          ▼
                  HumanApproval
```

排序依据：

1. ToolLoop 首先验证多轮状态、工具协议、限制、错误和 Usage 累加；
2. StructuredOutput 和 Reflection 可复用多轮模型运行基础；
3. Router 必须在多个成熟策略存在后才有实际价值；
4. PlanExecute、RAG 和 BestOfN 需要更成熟的预算及事件模型；
5. Handoff、Workflow 和 HumanApproval 依赖 Agent Registry、Checkpoint 或调度能力，最后推进。

## 6. 所有策略的统一验收要求

每个新增策略都必须：

- 实现公开 `RunStrategy`，可由模块外替换；
- 通过公开构造函数或 Builder 得到可用实例；
- 不修改通用 Agent Builder 来识别自身类型；
- 明确 Context、早停、资源释放和 goroutine 退出条件；
- 产生一次 RunStart 和唯一成功 RunDone；
- 对模型轮次、工具调用、Usage 和 GeneratedMessages 给出一致定义；
- 明确正常、边界、协议错误和外部依赖错误；
- 对调用方输入、组件配置、事件和结果执行必要快照；
- 支持同一已构建策略实例并发 Run，或明确使用内部同步；
- 提供模块外实现/组装示例和表驱动测试；
- 通过 Agent 包测试、`acore` 全量测试和 `go vet`；涉及并发时执行 race 检查。

## 7. 文档维护规则

- 状态只使用“已实现”“专项设计中”“待设计”“待评估”；
- 开始一个策略专项设计时，在本表更新状态并链接设计文档；
- 方案确认并实现后，补充实际公开 API、验证结果和已知限制；
- 若实际实现偏离路线图，以经确认的专项设计为准，并同步本文件；
- 不因路线图存在而批量创建空包、空接口或返回未实现错误的占位代码。
