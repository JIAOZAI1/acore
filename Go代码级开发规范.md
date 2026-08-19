# Go 代码级开发规范

## 1. 目的与适用范围

本文档用于统一 Go 项目的代码风格、设计方式和质量要求，提高代码的可读性、可靠性、可测试性与可维护性。

适用于团队内所有 Go 业务代码、公共库、工具程序及测试代码。

规范级别说明：

- **必须（MUST）**：所有代码均应遵守，原则上不得例外。
- **推荐（SHOULD）**：通常应遵守；偏离时应有明确理由。
- **可选（MAY）**：根据项目特点和实际场景采用。

## 2. 格式化与静态检查

### 2.1 代码格式

- **必须**使用 `gofmt` 格式化代码。
- **推荐**使用 `goimports` 自动整理导入并格式化代码。
- 禁止通过手工对齐破坏 Go 官方格式。
- 禁止提交未使用的变量、导入或不可达代码。

建议在提交前执行：

```bash
gofmt -w .
go vet ./...
go test ./...
```

### 2.2 静态检查

- **必须**在 CI 中执行 `go vet`。
- **推荐**使用 `staticcheck` 或 `golangci-lint`。
- `//nolint` 只能用于确认无法消除的误报，并应注明原因。
- 禁止使用范围过大的 lint 忽略配置掩盖真实问题。

```go
result := legacyCall() //nolint:staticcheck // 兼容旧协议，待迁移任务 GO-123 完成后删除
```

## 3. 命名规范

### 3.1 包名

- 包名应简短、小写、有明确含义，通常使用单数形式。
- 包名不应包含下划线或无意义缩写。
- 避免使用 `util`、`common`、`base`、`misc` 等职责模糊的名称。
- 调用包成员时不应产生语义重复。

```go
// 推荐
package user

user.NewService()

// 不推荐
package userservice

userservice.NewUserService()
```

### 3.2 标识符

- 导出标识符使用 `PascalCase`，非导出标识符使用 `camelCase`。
- 缩写应保持统一，例如 `ID`、`URL`、`HTTP`、`JSON`。
- 名称应表达业务含义，避免无意义的单字母名称；循环索引等局部场景除外。
- 接收器名称应简短且在同一类型中保持一致，禁止使用 `this` 或 `self`。

```go
type HTTPClient struct{}

func (c *HTTPClient) GetUser(ctx context.Context, userID string) error {
	return nil
}
```

### 3.3 接口命名

- 单方法接口优先使用行为加 `-er` 的形式，例如 `Reader`、`Writer`。
- 领域接口使用能够准确表达职责的名称，例如 `UserRepository`。
- 避免使用 `IUserService` 等语言迁移式命名。

## 4. 注释规范

- 所有导出的类型、函数、方法、常量和变量都应有文档注释。
- 导出对象的注释应以对象名称开头。
- 注释应重点解释设计原因、约束和非显而易见的行为。
- 禁止用注释简单复述代码。
- `TODO`、`FIXME` 应附带负责人、任务编号或问题链接。
- 修改代码时必须同步维护相关注释。

```go
// UserService provides user-related business operations.
type UserService struct{}

// TODO(zhangsan): GO-123 完成新缓存方案后移除兼容逻辑。
```

## 5. 包与文件组织

- 一个包应承担相对明确、内聚的职责。
- 禁止循环依赖。
- 避免过深的包层级和只包含一个简单类型的无意义包。
- 不应仅按技术类型机械拆分为 `models`、`services`、`utils`。
- 文件名使用小写字母和下划线，例如 `user_service.go`。
- 测试文件使用 `_test.go` 后缀。
- 平台相关文件遵循 Go 构建约定，例如 `file_linux.go`。
- 避免使用 `init()` 执行隐式初始化，优先显式构造和依赖注入。

## 6. 函数设计

- 一个函数应只承担一项明确职责。
- 函数名应准确表达其行为和副作用。
- 应控制函数长度、参数数量及嵌套深度。
- 优先提前返回，使主流程保持清晰。
- 参数过多时可以引入参数结构体，但不要进行无意义包装。
- 谨慎使用命名返回值，避免长函数中的隐式赋值。
- 避免用布尔参数切换两种不同操作，优先拆分函数或使用明确的选项类型。

```go
func ValidateUser(user User) error {
	if user.ID == "" {
		return ErrMissingUserID
	}

	if user.Email == "" {
		return ErrMissingEmail
	}

	return nil
}
```

## 7. 类型与接口

- 对具有独立业务语义的数据，推荐定义领域类型。
- 接口应保持小而专一，通常由使用方定义。
- 不要为只有一个实现且没有替换需求的类型提前创建接口。
- 推荐接收接口、返回具体类型，但应结合实际扩展需求判断。
- 同一类型的值接收器和指针接收器应尽量保持一致。
- 必要时使用编译期断言验证接口实现。

```go
type UserID string

type UserReader interface {
	FindByID(ctx context.Context, id UserID) (*User, error)
}

var _ UserReader = (*UserRepository)(nil)
```

## 8. 变量、常量与作用域

- 变量作用域应尽可能小，并在首次使用位置附近声明。
- 局部变量通常使用 `:=`，但应避免变量遮蔽。
- 有业务含义的字面量应定义为常量。
- 常量应尽可能使用明确类型。
- 避免可变全局变量；共享状态应通过依赖注入传递并正确同步。
- 不应为了缩短代码而牺牲变量名的可读性。

```go
type Status string

const (
	StatusPending Status = "pending"
	StatusDone    Status = "done"
)
```

## 9. 错误处理

- 每个错误都必须被处理、返回，或明确说明忽略原因。
- 添加上下文时应使用 `%w` 包装错误并保留错误链。
- 使用 `errors.Is` 和 `errors.As` 判断错误，禁止比较错误文本。
- 错误文本以小写字母开头，通常不以标点结尾。
- 避免在底层记录错误后又将同一错误返回，防止重复日志。
- `panic` 不得用于普通业务异常，只应用于真正不可恢复的程序错误。
- 应区分业务错误、系统错误和可重试错误。

```go
user, err := repo.FindByID(ctx, userID)
if err != nil {
	return nil, fmt.Errorf("find user %s: %w", userID, err)
}
```

```go
if errors.Is(err, ErrUserNotFound) {
	// 处理用户不存在场景
}
```

## 10. Context 使用

- `context.Context` 应作为函数的第一个参数，并命名为 `ctx`。
- 不要将 `context.Context` 存入结构体。
- 禁止传递 `nil` context。
- 创建可取消或超时 context 后，应及时调用 `cancel`。
- context value 只用于请求级元数据，不应承载普通业务参数或可选参数。
- 调用外部服务、数据库或可能阻塞的操作时，应传递 context。

```go
func LoadUser(ctx context.Context, id UserID) (*User, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	return queryUser(ctx, id)
}
```

## 11. 并发规范

- 每个 goroutine 都必须有明确的结束条件和生命周期负责人。
- 启动 goroutine 时必须考虑取消、超时、错误传播和 panic 处理。
- channel 通常由发送方关闭；接收方不得随意关闭 channel。
- 禁止使用 `time.Sleep` 作为并发同步手段。
- 共享数据必须使用锁、原子操作或消息传递进行保护。
- 推荐使用 `errgroup` 管理一组相关并发任务。
- CI 或定期检查应执行竞态检测。
- 禁止创建无边界 goroutine、队列或 channel。

```bash
go test -race ./...
```

## 12. 资源管理

- 成功获得资源后，应立即安排释放。
- 重要的 `Close`、`Flush`、`Commit` 等错误必须检查。
- HTTP 响应体必须关闭。
- 数据库结果集必须关闭，并在遍历结束后检查 `rows.Err()`。
- 循环中应谨慎使用 `defer`，避免资源直到整个函数返回才释放。
- 事务必须明确提交或回滚，且边界不应覆盖无关操作。

```go
file, err := os.Open(name)
if err != nil {
	return err
}
defer file.Close()
```

## 13. 集合与数据处理

- 应明确区分 `nil` slice 与空 slice 在序列化中的语义差异。
- 已知容量时，推荐预分配 slice 或 map。
- 禁止依赖 map 的迭代顺序。
- 返回内部 slice、map 或指针前，应评估是否需要复制。
- 使用 `range` 时应注意循环变量地址、闭包捕获和副本修改问题。
- 公共 API 应明确返回集合是否允许调用方修改。

```go
users := make([]User, 0, expectedCount)
```

## 14. 控制流

- 正常流程应保持清晰，异常和边界情况优先提前返回。
- 避免过深的 `if`、`for` 和 `switch` 嵌套。
- `switch` 不需要显式 `break`。
- 谨慎使用 `fallthrough`，使用时必须明确表达意图。
- 避免复杂的一行表达式和难以理解的条件组合。
- 对空分支或有意忽略的结果，应写明原因。

## 15. 日志规范

- 使用结构化日志，并统一字段名称。
- 日志应包含定位问题所需的上下文，如请求 ID、用户 ID、操作名称和耗时。
- 禁止记录密码、访问令牌、密钥和完整敏感信息。
- 底层函数通常只返回错误，由系统边界层统一记录。
- 禁止用日志代替错误处理。
- 库代码不得随意调用 fatal 日志或 `os.Exit`；退出行为只应由程序入口层决定。

```go
logger.Error("load user failed",
	"user_id", userID,
	"request_id", requestID,
	"error", err,
)
```

## 16. 测试代码规范

- 核心业务逻辑必须有单元测试。
- 推荐使用表驱动测试和子测试。
- 测试名称应清晰描述场景和预期结果。
- 测试必须可重复，不得依赖执行顺序、真实时间或不稳定的外部环境。
- 测试辅助函数应调用 `t.Helper()`。
- 测试资源应使用 `t.Cleanup()` 释放。
- 应覆盖正常、异常、边界及必要的并发场景。
- 测试代码同样必须遵守格式化、命名和错误处理规范。

```go
func TestValidateUser(t *testing.T) {
	tests := []struct {
		name    string
		user    User
		wantErr error
	}{
		{
			name:    "missing user ID",
			user:    User{Email: "user@example.com"},
			wantErr: ErrMissingUserID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUser(tt.user)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateUser() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
```

## 17. 禁止事项

以下行为原则上禁止：

- 提交未经过 `gofmt` 的代码。
- 忽略错误且不说明原因。
- 通过比较错误字符串判断错误类型。
- 使用 `panic` 处理常规业务错误。
- 使用 `time.Sleep` 实现并发同步。
- 创建没有退出机制的 goroutine。
- 在日志中记录密码、密钥或访问令牌。
- 使用无职责边界的 `util`、`common` 包堆积代码。
- 在库代码中调用 `os.Exit` 或 fatal 日志。
- 依赖 map 的遍历顺序。
- 使用可变全局变量保存业务状态。

## 18. 自动化落地建议

建议将以下检查集成到 CI：

```bash
gofmt -l .
go vet ./...
staticcheck ./...
go test ./...
go test -race ./...
```

推荐的合并准入条件：

- 格式化检查通过。
- 静态检查无阻断级问题。
- 单元测试及竞态检测通过。
- 新增核心逻辑具有相应测试。
- Code Review 已确认错误处理、并发安全和敏感信息保护符合要求。

## 19. Code Review 检查清单

- [ ] 代码已通过 `gofmt`、`go vet` 和项目规定的静态检查。
- [ ] 命名准确，包和函数职责清晰。
- [ ] 导出对象具有有效的文档注释。
- [ ] 错误均被正确处理，错误链得到保留。
- [ ] 未使用错误文本比较或常规业务 `panic`。
- [ ] context 得到正确传递，超时和取消资源得到释放。
- [ ] goroutine 具有明确退出机制，无明显竞态或泄漏风险。
- [ ] 文件、连接、响应体、结果集和事务得到正确关闭。
- [ ] 日志字段合理，未包含敏感信息。
- [ ] 核心逻辑及异常、边界场景具有测试。
- [ ] 未引入无必要的接口、全局状态或第三方依赖。
- [ ] 代码变更与相关注释、文档保持一致。

---

本规范应结合项目架构、业务风险和团队实践持续更新。能够由工具检查的规则，应优先通过编辑器、Git Hook 和 CI 自动执行，避免仅依赖人工审查。
