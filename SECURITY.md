# Security Policy

## 支持范围

| 版本 | 安全修复支持 |
| --- | --- |
| 最新发布的 `v0.x` minor | 支持 |
| 更早的 `v0.x` minor | 不支持 |
| `main` / 未发布提交 | 不提供发布版本级承诺 |

项目从 `v0.1.0` 开始，优先为最新发布的 minor 版本提供安全修复。`v0` 阶段如修复涉及破坏性 API 调整，会在 Changelog 和 GitHub Release Notes 中明确说明。已停止支持的版本不会移动原标签，修复通过新版本发布。

## 报告漏洞

请优先使用 GitHub 仓库的 **Private vulnerability reporting** 提交安全问题：

<https://github.com/JIAOZAI1/acore/security/advisories/new>

报告应尽量包含：

- 受影响版本或 commit；
- 可复现步骤和最小示例；
- 影响范围与可能的利用方式；
- 已知缓解措施；
- 是否存在公开披露期限。

如果私有报告入口不可用，请先通过仓库所有者的 GitHub 联系方式请求私密沟通渠道，不要在公开 Issue 中提交凭证、完整利用代码或其他敏感细节。

普通缺陷和不包含敏感信息的安全加固建议可以使用公开 Issue。

## 安全边界

使用者仍需负责凭证管理、Tool 输入校验与授权、外部副作用幂等、Session 数据保护和部署沙箱。框架不会自动把 Tool Proxy、Event Bus 或 Context 取消转换为完整安全隔离机制。具体边界见 [README](README.md#安全边界)。
