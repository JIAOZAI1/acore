# acore 发布指南

`acore` 是公开 Go 模块。当前只发布模块源码，不发布 CLI 或 Server 二进制。GitHub 自动生成的 source archive 不是另一套版本来源；Git 标签是 Go 模块版本的唯一依据。

## 版本规则

- 使用 [Semantic Versioning](https://semver.org/) 稳定标签：`vMAJOR.MINOR.PATCH`。
- 首个公开版本为 `v0.1.0`。
- `v0` 阶段 API 仍可能演进；破坏性变化必须在 Changelog 和 GitHub Release Notes 中明确说明。
- 向后兼容的功能使用 minor 版本，向后兼容的修复使用 patch 版本。
- 已发布标签不可移动、覆盖或删除。发布错误通过新版本修复。
- 当前最低 Go 版本由 `go.mod` 声明为 Go 1.26。

## 发布前检查

1. 确认工作树干净，发布提交已经合入 `main`。
2. 更新 `CHANGELOG.md`，将本次内容从 `Unreleased` 移入目标版本并记录日期。
3. 检查 README、公开包文档、Provider 能力矩阵和已知限制。
4. 在 `acore` 模块执行：

   ```bash
   go build ./...
   go test ./...
   go test -race ./...
   go vet ./...
   ```

5. 确认 `go.mod` 不包含本地 `replace`，仓库不包含凭证、构建产物或临时文件。
6. 在 GitHub 上确认目标提交的常规 CI 已通过。

## 创建版本

版本由发布者明确决定并手工创建标签。Release Workflow 不创建标签，只验证标签并创建 GitHub Release。

以 `v0.1.0` 为例：

```bash
git switch main
git pull --ff-only
git status --short
git tag -a v0.1.0 -m "release v0.1.0"
git push origin v0.1.0
```

推送标签后，`.github/workflows/release.yml` 将：

1. 校验稳定 SemVer 标签和模块元数据；
2. 执行 build、test、race test 和 vet；
3. 检查所有公开包都有包级文档；
4. 从独立临时模块按标签下载并导入所有公开包；
5. 使用 GitHub 自动生成的 Release Notes 创建 GitHub Release；
6. 验证公共 Go module proxy 和 checksum database 可以解析该版本。

只有所有发布前门禁通过后才会创建 GitHub Release。

## 发布后验证

```bash
go list -m github.com/JIAOZAI1/acore@v0.1.0

workdir="$(mktemp -d)"
cd "$workdir"
go mod init example.com/acore-consumer
go get github.com/JIAOZAI1/acore@v0.1.0
```

同时检查：

- GitHub Release 指向正确且唯一的提交；
- Release Notes 与 `CHANGELOG.md` 一致；
- README 安装命令可用；
- 标签没有被移动；
- Release Workflow 的 proxy 验证成功。

## 回滚与修复

已发布版本不可原地修改。若版本存在问题：

1. 在 README 或 GitHub Release 中标明已知问题；
2. 修复后发布新的 patch 版本；
3. 只有版本包含不可接受的安全风险时，才按 GitHub 和 Go module proxy 的约束评估撤回说明；不要通过移动标签伪造原版本内容。
