# Changelog

本文记录 `acore` 的公开版本变化。格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本遵循 [Semantic Versioning](https://semver.org/)。

## [Unreleased]

### Changed

- README 和模块分析文档同步 `v0.1.0` 实际发布状态。

### Fixed

- Release Workflow 显式获取 annotated tag 对象，并正确清理只读 Go module cache，避免标签类型和发布后验证误判。

## [0.1.0] - 2026-08-31

### Added

- Provider 无关的模型、消息、Tool Call、Usage 和流式事件协议；
- OpenAI Chat Completions 流式 Provider；
- Agent 公共契约、Builder 和拉取式 Stream；
- SingleTurn 与有界 ToolLoop 运行策略；
- 不可变 Tool System 和有序 Tool Proxy 链；
- Static/Template Prompt Renderer；
- Revision/CAS Session 契约和并发安全内存实现；
- Token Estimator、Context Window Reducer 和 TailReducer 扩展点；
- 同步、进程内、类型安全的 Event Bus；
- 标准 RunEvent 数据契约；
- GitHub SemVer 标签发布工作流，以及构建、测试、竞态检测、静态检查、模块外导入和公共 Go module proxy 验证；
- 公开模块发布指南、安全策略、Provider 能力矩阵和包级文档。

### Known limitations

- RunEvent 尚未接入 Agent Publisher；
- OpenAI Provider 只支持 Chat Completions；
- Session 只有内存实现；
- 没有 Provider/模型专属 Token Estimator；
- Tool Result 只支持文本；
- Structured Output、Checkpoint/Resume、MCP 和长期记忆尚未实现。

[Unreleased]: https://github.com/JIAOZAI1/acore/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/JIAOZAI1/acore/releases/tag/v0.1.0
