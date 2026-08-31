# acore

[![Go CI](https://github.com/JIAOZAI1/acore/actions/workflows/ci.yml/badge.svg)](https://github.com/JIAOZAI1/acore/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/JIAOZAI1/acore)](https://github.com/JIAOZAI1/acore/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/JIAOZAI1/acore.svg)](https://pkg.go.dev/github.com/JIAOZAI1/acore)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

`acore` 是一个用 Go 编写的可组合 Agent 框架。它通过 Builder 组装模型、运行策略、工具、Prompt、Session 和上下文窗口组件，让应用可以按场景选择、替换和扩展能力，而不把具体 Provider 或业务实现固化在 Agent 主流程中。

项目当前已具备 SingleTurn 和 ToolLoop 两条可运行链路，适合继续构建自定义 Agent、Provider、Tool 和运行策略。首个公开版本号为 `v0.1.0`；当前处于 `v0` 阶段，公开 API 仍可能按明确记录的版本变化继续演进。

## 设计目标

- 以 Agent Builder 作为共享组件的统一组装边界；
- 以 RunStrategy 封装可替换的运行算法；
- 通过小而稳定的公开契约连接 Model、Tool、Session、Prompt 和 Event；
- 显式注入依赖，不使用全局 Service Locator 或隐式单例；
- Builder 负责可变组装，成功构建后的运行对象保持不可变；
- Provider、存储、Telemetry 和业务 Tool 作为适配器接入核心。

## 当前能力

- Provider 无关的模型、消息、Tool Call、Usage 和流式事件协议；
- OpenAI Chat Completions 流式 Provider；
- Agent、Builder、拉取式 Stream 和完整结果消费接口；
- SingleTurn 和有界 ToolLoop 运行策略；
- 不可变 Tool System 和有序 Tool Proxy 链；
- 静态 Prompt 与严格字符串模板；
- 无状态请求和基于 Session 的有状态请求；
- Revision/CAS 会话契约和并发安全内存实现；
- 可替换 Token Estimator、Context Window Reducer 和尾部裁剪策略；
- 同步、进程内、类型安全的 Event Bus；
- 标准 RunEvent 数据契约。

### Provider 能力矩阵

| 能力 | 核心协议 | OpenAI Chat Completions |
| --- | --- | --- |
| 流式文本生成 | 支持 | 支持 |
| 文本输入 | 支持 | 支持 |
| 图片输入 | 支持 | 支持 URL 和 base64 data URL |
| Thinking 内容 | 支持 | 不支持映射 |
| Tool Call | 支持 | 支持 |
| Reasoning Level | 支持 | 映射为 `reasoning_effort` |
| Structured Output | 未定义 | 未实现 |
| Tool Result | 仅文本 | 仅文本 |
| Token Estimator | 提供扩展接口 | 未提供具体实现 |

矩阵描述当前代码能力，不代表上游服务中所有模型都支持对应特性。应用仍需按具体模型配置和 Provider 返回结果处理能力差异。

## 架构

```text
应用配置 / 凭证 / CLI 或 API
              │
              ▼
         Agent Builder
       ┌──────┼─────────┐
       │      │         │
       ▼      ▼         ▼
  model.LLM  Prompt  RunStrategy
       ▲                │
       │        ┌───────┴────────┐
       │        ▼                ▼
  Provider  SingleTurn       ToolLoop
                   │       ┌────┼──────────┐
                   ▼       ▼    ▼          ▼
                Session  Tool  Session  ContextWindow
```

通用 Agent Builder 只接收所有策略共享的 LLM、RunStrategy、Prompt 和默认模型参数。Tool、Session、Context Window 等策略专属依赖由具体 Strategy Builder 组装。

## 环境要求

- Go 1.26 或更高版本；
- 使用 OpenAI Provider 时，需要应用提供 API Key 和模型 ID；
- Tool、Session Store、Token Estimator 等组件需根据应用场景选择或实现。

## 安装

发布版本应始终显式指定：

```bash
go get github.com/JIAOZAI1/acore@v0.1.0
```

当前首个公开版本为 `v0.1.0`。生产构建应固定明确版本，不要依赖分支头。

## 快速开始：SingleTurn

下面的程序使用 OpenAI Chat Completions Provider 构建一个无状态 SingleTurn Agent。API Key 和模型 ID 由应用环境显式提供。

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/JIAOZAI1/acore/agent"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/provider/openai"
)

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	modelID := os.Getenv("OPENAI_MODEL")
	if apiKey == "" || modelID == "" {
		log.Fatal("OPENAI_API_KEY and OPENAI_MODEL are required")
	}

	provider, err := openai.New(openai.Config{
		APIKey: apiKey,
		Models: []model.Model{{ID: modelID}},
	})
	if err != nil {
		log.Fatal(err)
	}

	selected := provider.Models()[0]
	llm, err := model.Bind(provider, selected)
	if err != nil {
		log.Fatal(err)
	}

	builder := agent.NewBuilder()
	if err := builder.UseLLM(llm); err != nil {
		log.Fatal(err)
	}
	if err := builder.UseRunStrategy(agent.NewSingleTurnStrategy()); err != nil {
		log.Fatal(err)
	}
	if err := builder.SetSystemPrompt("You are a concise assistant."); err != nil {
		log.Fatal(err)
	}

	value, err := builder.Build()
	if err != nil {
		log.Fatal(err)
	}

	result, err := agent.Complete(context.Background(), value, agent.Request{
		Messages: []model.Message{{
			Role: model.RoleUser,
			Content: []model.ContentBlock{{
				Kind: model.ContentText,
				Text: "Explain dependency injection in one paragraph.",
			}},
		}},
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, block := range result.Output.Content {
		if block.Kind == model.ContentText {
			fmt.Print(block.Text)
		}
	}
	fmt.Println()
}
```

运行：

```bash
OPENAI_API_KEY='<your-key>' OPENAI_MODEL='<model-id>' go run .
```

应用负责读取和保护凭证；`acore` 不会隐式读取环境变量。

## 使用 ToolLoop

ToolLoop 会把 Tool 目录发送给模型，并按模型返回的 Tool Call 串行执行工具，直到模型不再请求工具或命中运行限制。

先实现一个 Tool：

```go
type greetingTool struct{}

func (greetingTool) Spec() tool.Spec {
	return tool.Spec{
		Name:        "greet",
		Description: "Greets a user by name.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {"type": "string"}
			},
			"required": ["name"]
		}`),
	}
}

func (greetingTool) Execute(
	ctx context.Context,
	arguments json.RawMessage,
) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}

	var input struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return tool.Result{}, fmt.Errorf("decode greeting arguments: %w", err)
	}
	if input.Name == "" {
		return tool.Result{}, errors.New("name is required")
	}
	return tool.Result{Content: "Hello, " + input.Name + "!"}, nil
}
```

再构建 Tool System 和 ToolLoopStrategy：

```go
toolBuilder := tool.NewBuilder()
if err := toolBuilder.AddTool(greetingTool{}); err != nil {
	return err
}
toolService, err := toolBuilder.Build()
if err != nil {
	return err
}

strategyBuilder := agent.NewToolLoopBuilder()
if err := strategyBuilder.UseTools(toolService); err != nil {
	return err
}
if err := strategyBuilder.SetLimits(agent.ToolLoopLimits{
	MaxModelTurns:      8,
	MaxToolCalls:       32,
	MaxToolResultBytes: 64 * 1024,
}); err != nil {
	return err
}
strategy, err := strategyBuilder.Build()
if err != nil {
	return err
}

agentBuilder := agent.NewBuilder()
if err := agentBuilder.UseLLM(llm); err != nil {
	return err
}
if err := agentBuilder.UseRunStrategy(strategy); err != nil {
	return err
}
value, err := agentBuilder.Build()
if err != nil {
	return err
}
```

默认限制可通过 `agent.DefaultToolLoopLimits()` 获取。默认 Tool 错误模式会把脱敏后的失败反馈给模型；也可以使用 `agent.ToolErrorModeFailFast` 让运行立即失败。

完整 Tool 示例见 [tool/example_test.go](tool/example_test.go)，ToolLoop 组装示例见 [agent/example_test.go](agent/example_test.go)。

## Prompt

固定 System Prompt 可以直接使用 Agent Builder：

```go
if err := builder.SetSystemPrompt("You are a support assistant."); err != nil {
	return err
}
```

需要每个 Run 提供变量时，使用严格模板：

```go
renderer, err := prompt.NewTemplate(prompt.TemplateConfig{
	Name: "assistant",
	Text: "You are a {{.role}} assistant. Reply in {{.language}}.",
	Defaults: prompt.Values{
		"language": "English",
	},
})
if err != nil {
	return err
}
if err := builder.UsePrompt(renderer); err != nil {
	return err
}

request := agent.Request{
	Messages: messages,
	PromptValues: prompt.Values{
		"role": "support",
	},
}
```

缺失的模板变量会返回错误。Prompt Renderer 每个 Run 只渲染一次，不会隐式读取 Session、Tool 或全局状态。

## Session

Agent Request 支持两种互斥输入：

- `Messages`：调用方传入本次 Run 的完整历史；
- `Session`：调用方传入 Session Key 和尚未保存的新消息。

使用内存 Session：

```go
history := session.NewMemoryService()

strategyBuilder := agent.NewSingleTurnBuilder()
if err := strategyBuilder.UseSession(history); err != nil {
	return err
}
strategy, err := strategyBuilder.Build()
if err != nil {
	return err
}

request := agent.Request{
	Session: &agent.SessionInput{
		Key: session.Key{
			Scope: "tenant-a",
			ID:    "conversation-1",
		},
		Messages: []model.Message{{
			Role: model.RoleUser,
			Content: []model.ContentBlock{{
				Kind: model.ContentText,
				Text: "Remember that my preferred language is Go.",
			}},
		}},
	},
}
```

`session.MemoryService` 使用 Revision/CAS 处理并发提交，但数据会在进程退出后丢失。生产环境应实现 `session.Service` 并明确事务、租户隔离、保留期和删除策略。

Session 只在 Run 成功完成时追加消息。若 Tool 已产生外部副作用，而 Session 随后发生提交冲突，不要盲目重试整个 ToolLoop。

## 上下文窗口

`contextwindow.TailReducer` 会按估算后的输入预算删除最旧的完整 User 轮次，并保护当前 Run 的消息。它需要一个面向实际 Provider/模型的 Token Estimator：

```go
reducer, err := contextwindow.NewTailReducer(contextwindow.TailConfig{
	Estimator:            estimator,
	SafetyMarginTokens:   256,
	FallbackOutputTokens: 1024,
})
if err != nil {
	return err
}

strategyBuilder := agent.NewSingleTurnBuilder()
if err := strategyBuilder.UseContextWindow(reducer); err != nil {
	return err
}
```

`acore` 不提供声称适用于所有模型的通用 Estimator。实现 Estimator 时必须计算完整 `model.Context`，包括 System Prompt、Messages 和 Tool Specs。

Context Window 只改变单次 Provider 请求视图，不会裁剪 Session 中保存的完整历史。

## 流式消费

`agent.Complete` 适合只需要最终结果的调用方。需要实时输出模型增量或观察工具边界时，直接消费 Agent Stream：

```go
stream, err := value.Run(ctx, request)
if err != nil {
	return err
}

for event, streamErr := range stream {
	if streamErr != nil {
		return streamErr
	}

	switch event.Type {
	case agent.EventModel:
		if event.ModelEvent != nil &&
			event.ModelEvent.Type == model.EventContentDelta {
			fmt.Print(event.ModelEvent.Delta)
		}
	case agent.EventToolStart:
		fmt.Printf("\nstarting tool %s\n", event.Tool.Call.Name)
	case agent.EventToolDone:
		fmt.Printf("finished tool %s\n", event.Tool.Call.Name)
	case agent.EventRunDone:
		fmt.Printf("\nturns=%d toolCalls=%d\n",
			event.Result.ModelTurns,
			event.Result.ToolCalls,
		)
	}
}
```

Stream 是 pull-based 的。调用方停止迭代会向底层生成器传播早停信号，释放 Provider 资源，并阻止尚未开始的后续模型调用和 Tool 副作用。

## 模块目录

| 包 | 职责 |
| --- | --- |
| `agent` | Agent 契约、共享 Builder、Stream、内置策略兼容入口 |
| `agent/agent-strategy` | 外部策略实现可复用的低层契约和辅助入口 |
| `agent/agent-strategy/singleturn` | SingleTurnStrategy |
| `agent/agent-strategy/toolloop` | ToolLoopStrategy、限制和 Tool 错误模式 |
| `agent/runevent` | 标准运行事件数据契约 |
| `model` | Provider 无关的模型协议、LLM 和 Provider Registry |
| `provider/openai` | OpenAI Chat Completions Provider |
| `tool` | Tool、不可变 Tool System 和 Proxy 链 |
| `prompt` | System Prompt Renderer、Static 和 Template |
| `session` | 会话历史契约和 MemoryService |
| `contextwindow` | Token Estimator、Reducer 和 TailReducer |
| `event` | 同步、进程内、类型安全的 Event Bus |

## 关键运行语义

- Builder 用于单 goroutine 启动期组装；首次成功 Build 后冻结；
- 构建后的 Agent、Strategy 和 Tool System 是不可变对象；
- 同一 Agent 支持并发 Run，但注入的 LLM、Tool、Session、Renderer 和 Reducer 也必须支持并发；
- Agent Request 必须且只能设置 `Messages` 或 `Session` 之一；
- ToolLoop 同一轮的多个 Tool Call 当前按模型返回顺序串行执行；
- Runtime 错误通过 Stream 的 error 值返回，不编码成成功事件；
- 使用 `errors.Is`/`errors.As` 判断稳定错误和 Provider `APIError`；
- Event Bus 是旁路通知机制，不能代替 Agent Stream 或审批控制流。

## 安全边界

- API Key、Token 和其他凭证由应用显式注入，不要硬编码或记录；
- 模型生成的 Tool 名称和参数均是不可信输入；
- Tool System 只验证参数是 JSON 对象，不执行完整 JSON Schema 校验；
- Tool 应自行校验字段、权限、范围和业务不变量；
- Tool Proxy 可以实现鉴权、审批、超时和审计，但不等于 Shell、文件系统或网络沙箱；
- 默认不要记录完整 Prompt、消息、Thinking、Tool 参数或 Tool 结果；
- 对有外部副作用的 Tool 使用幂等键、调用账本或其他业务保护。

## 当前限制与演进方向

以下能力尚未完成或尚未进入核心运行链路：

- `agent/runevent` 已定义事件，但尚未接入 Publisher；
- OpenAI Provider 当前只支持 Chat Completions；
- Session 只有内存实现；
- Context Window 没有内置 Provider/模型 Estimator；
- Tool Result 只支持文本；
- 没有 Checkpoint、Interrupt、Resume、MCP 或长期记忆；
- Structured Output、Reflection、Router、PlanExecute、Handoff 和 Workflow 等策略尚未实现。

这些能力会在出现明确用例后按模块设计，不会通过一个全能 Runtime 或 Middleware 一次性引入。

## 版本与发布

- 模块使用 `vMAJOR.MINOR.PATCH` 形式的 SemVer 标签；
- 当前处于 `v0` 阶段，破坏性变化会在 Changelog 和 GitHub Release Notes 中明确说明；
- 最低 Go 版本以 `go.mod` 为准，当前为 Go 1.26；
- 当前只发布 Go 模块源码，不发布独立 CLI 或 Server 二进制；
- 发布者手工创建版本标签，GitHub Actions 验证后创建 GitHub Release；
- 已发布标签不可移动或覆盖，修复通过新版本发布。

版本变化见 [CHANGELOG.md](CHANGELOG.md)，维护者发布步骤见 [RELEASING.md](RELEASING.md)，漏洞报告与支持范围见 [SECURITY.md](SECURITY.md)。

## 开发与验证

```bash
go build ./...
go test ./...
go vet ./...
go test -race ./...
```

竞态检测需要启用 CGO。仓库 CI 会执行 build、test、race test 和 vet。

只格式化本次修改的 Go 文件：

```bash
gofmt -w <changed-go-files>
```

更多可执行用法可参考各包中的 `example_test.go` 和单元测试。

## 贡献原则

- 先检查当前代码和模块边界，不为假设中的需求创建占位接口；
- 优先复用已有契约，只在存在真实扩展或解耦需求时增加抽象；
- 新公开组件必须能由模块外实现、组装和替换；
- 不把应用配置、凭证或具体厂商 DTO 放入核心协议；
- 新功能和缺陷修复应覆盖正常、边界和关键错误路径；
- 提交前运行与改动范围匹配的测试、静态检查和竞态检测。

## 开源协议

Copyright 2026 acore contributors.

本项目依据 [Apache License 2.0](LICENSE) 开源。你可以在遵守协议条款的前提下使用、修改和分发本项目，包括用于商业用途。
