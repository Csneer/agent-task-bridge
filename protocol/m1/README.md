# Agent↔Task Bridge M1 — 实施基线入口

日期：2026-10-30。版本：`bridge-m1-1`。状态：**已冻结为本地实施基线；未实现 daemon，尚未公开发布。**

本阶段把已采纳的 [Core v1](../../docs/architecture/core.md) 和 [能力蓝图](../../docs/architecture/blueprint.md) 转成方法、状态、授权、数据结构与黑盒向量。目标仍是：两个独立 Agent 客户端完成一次真实任务交接，独立读取方通过公共协议恢复统一事实。它是独立的新桥接项目，不是 p-drive 的投影器或自动编排系统。

## 阅读与规范分工

| 材料 | 负责回答的问题 |
|---|---|
| [状态与事务契约](state.md) | 每个方法何时允许，检查哪个 revision，怎样原子改变 Task/Assignment/Run/Submission/Decision，事件是什么顺序 |
| [身份与授权契约](auth.md) | 谁能创建、领取、交付、验收、授予和撤销权限，身份与父子关系怎样隔离 |
| [HTTP / JSON-RPC 契约](wire.md) | 27 个方法的参数/结果、错误、幂等、资源引用、快照/cursor、历史审计和部署限制 |
| [JSON Schema](schema.json) | 对象、27 个请求形状、结果与错误的机器可验证结构 |
| [方法映射](methods.json) | 精确方法名对应参数/结果类型及幂等、CAS、lease 要求 |
| [固定样例](golden-vectors.json) | 正/负输入、预期结构结果、canonical 字节和请求摘要 |
| [黑盒验收向量](../../docs/validation/vectors.md) | H01 真实交接、V01–V25 故障与竞争、G01–G06 跨语言结构/摘要覆盖 |

Core 定义业务不变量；状态/auth/wire 分别约束流程、权限和编码，三者取交集。Schema 约束结构，不代替授权、跨对象唯一性和时间守卫。若这些文件出现冲突，应修订协议包，不能靠选择一份“优先”文件静默绕过另一份。旧 v9 保留在来源工作区用于追溯，不属于本项目实施输入。

## 本阶段确定的最小选择

1. **统一 Task 聚合 revision。** 每个成功事实事务加一，renew/report/grant 也参与 CAS。实现简单、并发可解释；代价是 supervisor 与执行者需要处理明确冲突，M1 不另设 lease revision。
2. **先独立维护，再对新命令做 CAS。** 已提交幂等回执优先返回；新命令可能先物化到期并返回 stale。只物化一次，重读后不会被同一到期事实无限阻塞。
3. **claim 和 start 分开。** claim 只创建 Assignment，start 创建唯一 Run；submit 同事务完成 Assignment、Run 与 Submission。Decision 唯一性按 Submission，拒绝后重做产生新一组对象。
4. **直接授权，固定 binding 实例。** Owner/Issuer 显式授予 Task 权限；parent 不携权。停用后重新启用生成新 binding_id，旧 Assignment 不能复活。claim Grant 与 binding 都限制 lease 截止。
5. **child 独立聚合。** createChild 原子创建 child/Relation/grants，但不改父 revision；child 与 parent 同 Owner。跨 Owner、递归授权和父子状态聚合后置。
6. **历史回执与执行权分离。** 当前有读取权才能取回历史业务结果；token 不因重放恢复有效。只读状态可能尚未物化到期，恢复外部执行前必须通过当前 fenced 命令。
7. **快照接续与历史审计各有入口。** snapshot 在同一 W 捕获对象事实，接续 W 之后事件；events.open/read 提供 W 之前的完整保留事务历史。audit 保存 action、request_id、reason。
8. **有边界的 M1 运行配置。** 单 authority、私网、预注册、HTTP/JSON-RPC、SQLite/WAL、pull；快照有上限。全局 auth_epoch 简化权限一致性，代价是不相关授权变化也会引发重建。

M1 选择不清理成功幂等键/事件/历史，避免窗口结束后重新执行旧命令；容量达到限制时明确失败。这不是无限容量承诺，未来 retention/GC 需新增协议。JSON 数字只允许安全整数，未知业务数据放 namespaced extensions，较大产物放外部引用。

## 场景到契约的对应

```text
Owner create/publish P
  → A discover/claim/start P
  → A 以独立 Owner/Issuer 权限 createChild C + publish C
  → B discover/claim/start/report C
  → B submit C（结束租约、创建 Submission）
  → 指定主体验收 S_C
  → R snapshot + replay；需要过去控制记录时 events.open/read
```

C 超时只结束 C 的承接，P 保持自身流程；P 超时也不撤销 C 的直接权限。另用允许 A/B 的 Q 测试换 Agent 重领，不能扩大定向 C 的候选范围。

## 可复现的静态检查

在仓库根运行（Python 3.10+，依赖见根目录 requirements-contract.txt；Node 22+）：

```sh
python3 protocol/m1/check-contract.py
node protocol/m1/check-hashes.mjs
```

检查器验证 Schema 自身、固定正/负结构样例、UTC 毫秒日期与 URI 格式、重复 JSON 键/非法 Unicode/数值、27 个方法的请求覆盖，以及 JCS 安全整数子集的固定摘要。Python 与 ECMAScript 分别计算相同摘要；这仅是摘要互认，不等于两个完整客户端已经互操作。

迁移前检查通过 90 个结构样例、7 个摘要样例、9 个解析样例；本基线增加两个 IPvFuture 正例，结构样例为 92 个。版本变为 bridge-m1-1 后已重算固定请求摘要。历史设计审查见 [协议细化复核记录](../../docs/validation/design-review.md)，本次验证和基线摘要见 [冻结记录](../../docs/validation/freeze-review.md)。

当前尚未执行：daemon 状态/授权/事务测试、SQLite 崩溃恢复、H01/V01–V25、真实输入和产物访问，以及两个独立客户端的完整 conformance。不能把静态通过记为 M1 通过。

## 冻结与下一阶段

协议包已迁入独立 Git 项目；[manifest.json](manifest.json) 绑定本基线的确切文件，[版本规则](../../docs/versioning.md) 定义修订与兼容性处理。冻结记录保存审查范围及静态验证证据，不冒充运行通过。

后续按 [实现任务单](../../docs/implementation-plan.md) 先固定领域对象与事务端口，再实现 daemon、两个独立客户端和只读消费者，最后执行 H01/V01–V25。本阶段不包含这些运行实现。
