# Agent↔Task Bridge M1 — 协议细化复核记录

历史记录（迁移前）：本文件保留当时结论；当前基线状态以 [冻结记录](freeze-review.md) 为准。

日期：2026-10-30。结论：**本阶段草案交付完成；可进入版本化与协议冻结复核。**

当前入口：[M1 协议草案包](../../protocol/m1/README.md)。本轮是用户“继续下一阶段”授权下的协议文档、Schema、样例与测试设计工作。没有 daemon 运行结果；H01/V01–V25 均尚未执行。

## 审查过程与修正

状态事务、直接授权和黑盒向量并行形成初稿，主代理整合 wire/Schema。随后独立 Architect 检查架构反例，修订后独立 Critic 检查确定性与编码；不是完整 Ralplan runtime 或实施授权记录。

| 确认问题 | 最终修正 | 验证位置 |
|---|---|---|
| 停用后维护尚未结束旧承接，重新启用可能复活旧 lease | binding 实例不可复活；Assignment 固定 binding_id；停用配置事务永久撤销旧实例；新启用用新 ID | auth §1/4、state §2/4、Assignment Schema、V23 |
| 物化状态 active 不证明查询时 lease/binding 有效 | 读取只核对对象/代次；恢复工作前须成功当前 fenced 命令；不承诺后续持续有效 | wire §5、V25 |
| 控制动作 reason 未落盘，首次接入者无法读 W 之前审计 | CommitBatch.audit 保存 action/request_id/reason；events.open/read 提供当前授权下的保留历史 | state §5、wire §7、Audit Schema、V25 |
| 状态表要求验收 Grant，授权表允许指定 Owner 资格 | 统一为指定验收主体且当前 Owner 或验收 Grant | state 方法矩阵、auth 方法矩阵 |
| JSON 浮点舍入使原始分数被当成安全整数 | 原始词法按精确数学值验证；1e0 可接受，细小分数/下溢拒绝 | wire §1、check-contract.py、9 个 parser cases |
| FormatChecker 缺可选实现，非法日期/URI 未被真实验证 | 显式日期与 RFC 3986 ASCII URI 验证；URI regex 同步 Schema | check-contract.py、Timestamp/Ref Schema、负例 |
| 快照失效后占用配额，重建被阻塞 | open 前可回收失效/完成快照；不再占活跃配额 | wire §8 |
| occurred_at/commit_cursor 等名称不对应实体字段 | 统一 committed_at/ended_at/effective_at、commit_id/next_cursor；非 expiry effective_at=null | state §4/5、Schema、向量 |

Architect 初次结论 WATCH，指出上述架构问题并接受最小修正方向。Critic 初次要求修正验收 guard、数字解析与 URI；修正后定点复核结论 **OKAY**，未发现当前审查范围内仍需修改的阻断项。两路意见均为设计评审证据，不是宿主安全授权或产品通过报告。

最强替代方案是按主体/作用域细分授权版本，以降低无关权限变化引起的重建。M1 保留全局 auth_epoch，接受保守重建成本，同时设置快照限额与回收规则；不把此选择描述为未来公共多租户规模方案。

## 已执行验证

```text
rtk proxy python3 protocol/m1/check-contract.py
PASS: 90 structural cases + 7 digest cases + 9 parser cases; 27 RPC request shapes.

rtk proxy node protocol/m1/check-hashes.mjs
PASS: 7 canonical bytes and hashes match in ECMAScript.
```

主代理和最终复核者分别复跑。另检查现行规范的本地链接、Markdown 表格/围栏/空白、27 方法覆盖及 V01–V25 编号覆盖。公开错误、静态样例和结构断言已对齐；JSON Schema 本身不证明权限、时钟、CAS 或事务原子性。

尚未执行：真实 bridge 黑盒测试、SQLite 崩溃恢复、两个完整独立客户端互操作、只读消费者恢复、输入/产物实际访问。当前摘要在 Python/ECMAScript 一致，只是摘要算法互认，不能代替完整第二语言客户端 conformance。

## 后续边界

下一步把协议包纳入独立项目的受版本控制目录，固定版本并完成冻结复核，再拆分参考 daemon/客户端/消费者实施和真实验收。当前文件仍位于 Git 忽略的 `.omx/`；本记录不声称已提交、发布或正式冻结。旧 v9 仍仅用于追溯。
