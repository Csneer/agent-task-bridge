# Agent↔Task Bridge — M1 acceptance profile v1

日期：2026-10-30（Asia/Shanghai）。状态：**已按蓝图复审修订；协议按 bridge-m1-1 基线管理，运行实现尚未开始。** 本文件描述首版验收目标和反例场景，不是测试执行报告，也不是对公网市场的承诺。

具体方法和测试时序已细化为 [M1 协议基线](../../protocol/m1/README.md) 与 [H01/V01–V25 向量](vectors.md)。以下 14 条为原始验收基线，仍未执行。

当前依据：[Core Definition v1](../architecture/core.md) 与 [v1 能力链路与架构蓝图](../architecture/blueprint.md)。

## 目标

证明两个独立 Agent 客户端可以完成一次可复现的任务交接，并让独立读取方通过公开接口恢复统一事实。

## 主场景：A 承接父任务，向 B 委派审阅子任务

P 是一份分析或技术说明的父任务；C 是审阅某个固定版本候选产物的子任务。两者都是可重复产生候选产物的工作，发布时分别配置 `delivery-acceptance-v1`、`safe_reopen`、验收主体及 `allow_rework`。C 的规范性输入固定版本，不能依赖 A 未公开的会话或持续变化的私有文件。

下表按语义表示调用及其目标；最终 RPC 方法名和请求字段由 wire contract 固定。

| 步骤 | 操作与目标 | 需观察的事实 |
|---|---|---|
| 1 | Owner 创建并发布 P | P 的工作约定、可领取主体、验收主体和策略已确定 |
| 2 | A `claim(P)`，再 `startRun(P)` | 父 Assignment 和 Run 明确绑定 P；A 按规则续租 |
| 3 | A 凭显式创建、关联和授予权限执行 `createChild(C, parent=P, eligible=B)` | C、Relation、B 的范围与期限可审计；权限失败不得留下部分 child/Relation/grant |
| 4 | 有权主体通过 A 客户端 `publish(C)` | C 从 draft 进入可发现、可领取状态；创建 C 本身不等于发布 |
| 5 | B `discover(C) → claim(C) → startRun(C) → report(C)` | B 使用另一个独立客户端，仅凭公开约定和有权输入执行 |
| 6 | B `submit(Run_C)`，得到 `Submission S_C` | C、当前 Assignment、Run 和 Submission 在同一事务完成提交联动 |
| 7 | 指定验收 Principal 对 `S_C` 作出 accepted 或 rejected | Decision 绑定明确 Submission；C 的验收不会自动改变 P |
| 8 | 只读消费者 R 首次建立视图，断线后再恢复 | R 只用公开查询和事件得到 P/C、关系、承接、执行、交付和决定；无需 claim 权限 |

Owner/Issuer、A 所代表的 Principal 和验收 Principal 是权限角色，可以按明确配置由同一或不同主体承担。A 承接 P 是本验收场景的一部分，**不是所有 child 创建的权限前提**。M1 采用显式 Owner/Issuer 直接授权；Agent 客户端可以代表获授权的主体创建 child，不要求递归转授体系。

正常主场景保持 P 的租约有效，以免把 P 自身到期混同为 C 状态导致的变化。C 的输入由 B 实际读取，交付产物由验收主体实际读取；只保存 URL 不算满足条件。P 后续是否提交、取消或继续等待，由上层发出有权命令决定。

## 必须验证

- A、B 使用独立客户端集成；同一客户端启动两次不算充分证明。
- B 不读取 A 的隐藏上下文、bridge SQLite 文件或内部对象；公开 Task 内容和授权引用足以启动工作。
- Task 工作约定发布后稳定；外部规范性引用固定版本或可恢复原内容。
- 一个 Task 同时至多一个有效 Assignment；release、revoke 或 expiry 后，不能凭旧代次执行新的 progress/submit。拒绝旧执行写入不等于撤销同一 Principal 的独立 Owner/Issuer 或验收权限。
- expiry 的事实由 bridge 权威时钟和受控 lease maintenance/等价物化机制产生；与旧代次 fencing 原子完成，重启和重复扫描不产生重复事实。
- Run 的执行报告、Submission 的交付和 Decision 的验收分别可见；Decision 不改写 Run 事实。
- `run.submit` 在同一事务把 Task 置为 submitted、当前 Assignment 置为 completed 并结束租约、Run 置为 submitted，同时创建该 Run 唯一的正式 Submission；事件与幂等结果一并提交。之后的租约扫描不得重新开放已提交或已验收任务。
- M1 的 Decision outcome 只有 `accepted` / `rejected`。最终决定唯一性按 `submission_id`，不按 Task 内容版本；只有发布时指定且当前仍有验收权限的 Principal/Policy 能对当前待验收 Submission 作决定。相同幂等请求返回原决定，冲突决定拒绝。
- `rework` 不是第三种 Decision outcome。发布时冻结 `allow_rework`；拒绝后只有策略允许且有权主体显式 `task.reopen`，才能形成新的 Assignment/Run/Submission。同一内容版本可以先有 S1 的 rejected，再有 S2 的 accepted；S1 及其决定保留，不能用旧 Submission 推进当前 Task。
- 新命令的最终授权、revision、generation、租约、状态和唯一性检查，以及幂等竞争判定，都使用同一写事务内的权威状态。事务外预检通过不能替代最终检查。
- 已提交的幂等回执在校验当前读取权限后返回原业务结果，不重新执行原命令或以当前租约状态推翻历史成功；无已提交回执时才执行新命令的状态守卫。旧 token 不因回执重放恢复效力；同键不同参数不能复用回执。
- 创建 child、建立 parent Relation、授予 B 可领取/执行权限分别通过显式 Owner/Issuer action；parent_id 单独不能授权。
- Submission 引用的输入/产物对 B 和验收者实际可访问。
- Read Consumer 可以枚举所有当前有读取权限的任务，包括 offered、submitted、accepted、closed；没有 claim 权限、当前不能领取或已进入终态，均不能成为普通读取的隐含排除条件。
- 事件至少一次；首次或重建按下节的一致快照与事件水位衔接，已有消费者从持久化 checkpoint 恢复，按 `event_id` 去重。投影更新与 checkpoint 必须按可恢复的一致方式持久化，不能先推进位置再丢失事实处理。
- `safe_reopen` 任务租约到期后可由具备领取资格的 Agent 重新 claim；测试只证明该显式策略，不扩大为所有任务安全重试。定向 B 的 C 不因过期就向 A 开放；换 Agent 承接使用另一个明确允许 A/B 的任务实例。
- `manual_reopen` 任务在 `reopen_required` 门控下不可领取，直到授权 `task.reopen`；`safe_reopen` 到期后进入 `claimable`，旧 Run/Submission/Decision/事件保留。
- 旧 Agent 可继续运行的故障注入只验证桥内 fencing，不声称外部执行停止。
- 父任务不因 child expiry/rejection 自动变化；上层能读到事实并自行决定。

## 消费者首次接入与重建

采用“一致快照 + 对应事件水位 W”的衔接方式：R 取得一份授权查询快照及其水位 W；全部分页属于同一快照；完成该快照装载后，从 W 之后继续 replay，再持续向后读取新提交事件。快照覆盖公开的权威事实，不要求仅凭事件重建 bridge 内部数据库。

每页和 replay 都检查当前读取权限。授权变化、快照失效或游标失效时，必须返回明确的重建要求；消费者丢弃不完整快照，重新取得与当前授权一致的快照和水位，并替换当前视图。不能静默改用“当前最新 cursor”，也不能继续暴露已失去读取权的当前投影视图。已向外部消费者披露的数据不由此获得远程擦除保证。

具体的 snapshot/cursor 编码、有效期、分页资源限制、事件保留窗口及稳定错误类型在 wire contract 中固定。验收必须在查询初始化与 replay 建立之间插入并发提交，并核对最终公开事实没有遗漏；仅测试已有 checkpoint 的重连还不够。

## 反例验收表

下表是待实现的黑盒验收向量，当前未执行。每个场景使用独立 fixture，检查公开状态、历史记录、事件和回执，而不是读取 SQLite 内部表。

| 前提 | 动作或故障注入 | 必须满足的结果 |
|---|---|---|
| T 内容版本固定，`allow_rework=true`；S1 已被拒绝 | 授权 reopen，建立新 Assignment/Run 并提交 S2，再接受 S2 | D1/rejected 与 D2/accepted 可并存；内容版本不变；旧 S1 的新决定不能推进 T |
| T 已 rejected 且 `allow_rework=false`，或 T 已 accepted/closed | 请求 reopen 或再次 claim | 拒绝非法流转；旧历史不覆盖，不能靠 recovery gate 绕过生命周期 |
| submit 已提交，响应丢失，原执行凭证随后不可写 | 同一身份在仍有回执读取权时用原键原参数重试；再尝试用旧 token 新写 | 返回原 Submission，不产生重复 Submission/成功业务事件；新的旧代次写入被拒绝 |
| 已存在成功回执，但调用者的相关读取权限被撤销 | 重放原请求 | 不泄露历史结果；拒绝读取不删除原回执、不改写已发生的业务事实 |
| 旧代次提交在事务外预检通过 | 在其取得写事务前，另一个事务 revoke 该 Assignment | 提交事务重新检查并拒绝；没有新的 Submission 或提交成功事件 |
| B 的 submit 与到期维护竞争 | 控制两个事务的先后次序 | 只有符合权威时间及状态守卫的结果可提交；submit 胜出后 Assignment completed，维护不得再重开 T；expiry 胜出后旧提交被拒绝 |
| child 创建、关系和授权采用原子便利命令 | 让 grant 校验失败 | 不留下部分 child、Relation 或 grant，也不产生成功创建事件 |
| R 只有 read/replay 权限 | 列出 offered、submitted、accepted、closed 的有权任务 | 不因缺少 claim 权限或任务不可领取而漏项 |
| R 正在分页装载水位 W 的一致快照 | 另一客户端在快照期间提交，随后 R replay | 所有快照页一致；W 之后的事件被消费，任务当前视图不漏提交 |
| R 已读完某水位，或正在装载快照 | 分别新增事件、改变 R 授权、使快照/游标失效 | 新事件可从原合法检查点续读；授权或快照/游标失效明确要求重建，不能静默跳至最新位置 |
| P 的租约保持有效；C 执行失联或被拒绝 | 仅让 C 到期，或对 C 作 rejected 决定 | C 产生自己的事实；P 不被自动改写。C 到期后是否可领取按 C 自己的策略与资格判断 |
| C 的直接授权和租约仍有效；A 另有独立 Owner/验收权限 | 仅让 P 的 Assignment 到期 | P 的旧执行写入被拒绝；C 不被删除或隐式撤销；A 的独立权限仍按自身有效性检查 |
| Q 明确允许 A/B 领取，分别建立 safe/manual 恢复实例 | B 租约到期，A 尝试领取；manual 实例随后由有权主体 reopen | safe 实例允许合法重领；manual 实例在 reopen 前拒绝、之后允许；单 Task 始终至多一个有效 Assignment |
| bridge 已停止，某租约在停机期间到期 | 完整存储重启后仅运行维护与公开读取；再重复扫描 | 启动补扫在约定维护界限内产生一次到期事实；不会恢复旧执行权或重复产生到期事实 |

父任务到期与子任务到期必须分别测试，不能以父任务自身的租约变化宣称“发生了父子状态级联”。定向领取与多人可领取的 fixture 也必须分开，不能为了换 Agent 演示偷偷扩大 C 的资格范围。

## 不作为 M1 通过条件

- Web UI、公共市场、支付、声誉、算力计量、push、自动父子汇总。
- SQLite 文件跨机器双活、旧备份无损恢复、在线 authority migration。
- 任意外部副作用 exactly-once 或“租约过期即旧进程停止”。
- 仅靠同源客户端双开、读取内部数据库、或只验证 JSON Schema 就宣称完成跨 Agent 互操作。

## 进入协议复核前的产物与执行顺序

1. delivery-acceptance-v1 的状态/一致性矩阵，列出 Task、Assignment、Run、Submission、Decision 的事实写集。
2. 显式 Owner/Issuer/Agent/Acceptance Principal 授权表，parent Relation 不携带权限；区分独立授权与 Assignment 来源，定义 safe_reopen/manual_reopen 及拒绝后重做的入口和门控。
3. Task、Submission、Decision、引用资源和事件的最小 JSON Schema 与 golden vectors。
4. 两个独立客户端和一个只读消费者的黑盒互操作测试计划，细化本表的输入、时序、可观察输出及错误类型。

顺序为：**契约与测试计划 → 协议复核并冻结 → 参考 daemon、客户端和消费者实现 → 真实黑盒互操作与故障测试**。测试计划先于实现；真实集成测试在原型存在后执行，不能用文档检查代替。

上述产物已迁入独立 Git 项目并作为实施基线；后续按实现任务单建设 daemon/客户端。协议冻结及静态检查不表示 M1 运行验收已通过。
