# M1 独立代码审查记录

日期：2026-10-30。审查源码进入实现提交 `f537f936fafa95fd74be63801fe4b5ac057ce1b4`。审查角色为独立 code-reviewer，读了 26 个主要实现文件、修复专项测试和冻结 state/auth/wire 契约；未修改生产源码。最终结论：**APPROVE，审查范围内没有未解决的必须修复项**。

这份记录区分代码审查与全量测试。审查者自行运行 Go vet、Go authority 并发及租约监督 race 专项、草稿权限与维护容量 HTTP 专项、TS watchdog/共享 journal 专项；完整回归和 H01 在各自结果目录中保存。

| 初次发现 | 修复与回归 |
|---|---|
| capability ID 的 `owner:` 前缀被当作真实 Owner 身份 | 改为 authenticated Principal 与 contract Owner 的实际比较；合法前缀 ID、受限编辑、配置扩大和重启回归 |
| TS 续租或前置读取挂起，无法在已确认租约截止前通知停止 | 独立 watchdog，默认 1 秒提前量，单调时间及迟到成功保护；挂起与迟到响应回归 |
| 两个 TS 实例共享 journal，一次拒绝覆盖另一在途副本的 unknown；不同请求可覆盖同键 | 原请求独占发布，发送标记追加，只有有效成功建立确认；内存状态也保守保留 unknown；双实例和同键冲突回归 |
| Go 响应 40 MiB 限制小于合法事件页，TS 100 MiB 没有封套余量 | 两端改为 101 MiB；Go 实际 HTTP 测试包含约 44 MiB、100 个合法且各自小于 1 MiB 的批次 |
| Go 共享实例 authority 发现存在未同步读写风险 | 内部 authority 访问使用互斥锁，共享实例 Discover/Call 的 race 回归 |
| JSON 转义扩张可能使未来维护事件没有容量 | 提交前给有界 Assignment/Run/结束事件预留 16 KiB；大于可接受范围的创建原子拒绝，接近上限的 Task 实际完成 publish/claim/start/expiry |

审查确认保留容量符合冻结 wire §8，未修改冻结协议。审查没有把测试通过解释成外部执行互斥、跨机器双活、公网多租户或 exactly-once。旧可变 journal 明确拒绝迁移，原文件保留供显式恢复；不静默丢弃未知请求。

最终架构验收和完整运行证据另行记录；本结论不替代真实 Agent 产物、验收决定或源码身份核对。
