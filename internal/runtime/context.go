package runtime

import "github.com/JIAOZAI1/acore/internal/model"

// Context 持有一个进程运行期共享的、跨模块的依赖集合。
// Providers 是大模型提供商的注册表，供 looper 等上层按 Provider/Model 查找并生成。
type Context struct {
	// Providers 是大模型提供商注册表。上层应通过其接口消费，禁止直接 import 具体厂商类型。
	Providers *model.ProviderRegistry
}

// New 构造运行时上下文，预置空的提供商注册表。
func New() *Context {
	return &Context{Providers: model.NewProviderRegistry()}
}
