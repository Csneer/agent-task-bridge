# Changelog

产品版本与协议版本分别管理；协议基线仍为 `bridge-m1-1`。

## v0.1.0 — 2026-10-30

首个 Developer Preview，面向单 authority、单 daemon、预注册 Agent 和单机/受控私网。

- Go daemon：HTTP/JSON-RPC、SQLite/WAL、27 个方法，持久化任务/租约/执行/交付/验收、幂等回执、快照与事件恢复。
- 独立 Go/TypeScript 客户端与只读消费者；未知命令原键恢复、lease fencing、不可变资源摘要校验。
- `bridge init` 私有初始化，随机 bearer/密钥、摘要管理配置、30 天默认身份期限、已有目标不覆盖。
- 自包含最小 HTTP 示例，真实计算、提交、独立验收和历史读取。
- CLI 版本信息、文本/JSON 请求观测、维护失败和退出计数。
- MIT 许可证；Linux amd64/arm64、协议包、TypeScript SDK tarball、SHA256SUMS 与 CI 验证后发布流程。

Linux amd64 已执行验证；arm64 仅交叉编译。SDK 通过 GitHub Release 安装，npm registry 发布另行安排。首版不提供公网多租户、市场/支付、代码执行沙箱、外部 exactly-once、双活、旧备份回滚或历史 GC。详见 [发布说明](docs/releases/v0.1.0.md)。
