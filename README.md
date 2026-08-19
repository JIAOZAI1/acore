# acore

`github.com/JIAOZAI1/acore` — 一套使用 Go 构建、可被其他 Go 项目引用的完整 AI Agent 运行框架。

Acore 覆盖 Agent 的模型接入、工具调用、运行编排、事件、状态恢复、可观测性与评测等完整生命周期。框架既提供开箱即用的标准运行路径和高层 API，也允许替换 Provider、Tool、Loop 及其他策略实现。

## 安装

```bash
go get github.com/JIAOZAI1/acore@latest
```

## 使用

```go
import (
    "github.com/JIAOZAI1/acore"
)

func main() {
    // TODO: 补充示例
}
```

## 目录结构

```
acore/
├── event/                  # 进程内事件
├── looper/                 # Agent 运行驱动与 Loop 扩展点
├── model/                  # 厂商无关的模型协议
├── runtime/                # 进程级共享能力装配
├── tool/                   # 工具注册、发现与执行代理链
├── internal/               # 厂商适配等内部实现
└── go.mod                  # 模块定义
```

## 许可证

MIT
