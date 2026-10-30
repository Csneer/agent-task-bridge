# Agent↔Task Bridge — M1 黑盒契约向量草案

日期：2026-10-30（Asia/Shanghai）。状态：**测试设计，尚未执行；不是 M1 通过报告。**

依据：[核心定义](../architecture/core.md)、[蓝图](../architecture/blueprint.md)、[原验收计划](acceptance.md)。逐方法语义以 [状态契约](../../protocol/m1/state.md)、[授权契约](../../protocol/m1/auth.md) 和本阶段 wire contract 为准。本文将原反例表的 14 行固定为 V01–V14，追加实现不可省略的竞争和客户端互操作覆盖。

## 1. 测试边界与判定方式

每个向量使用独立 fixture；所有断言通过公开 JSON-RPC、公开快照/事件与产物服务完成，不读 SQLite。竞态测试可由测试宿主控制 daemon 的时钟、暂停点和进程终止，但这不是新增生产 RPC；注入只控制调度，不直接制造数据库状态。运行证据应保存脱敏请求/响应、执行顺序、读取水位、事件 ID 和 fixture 标识。仅证明外部进程仍能运行、桥拒绝其旧授权写入，不宣称外部副作用已经停止。

`call(actor, method, params, K, r)` 表示经过真实认证的 JSON-RPC 调用；请求携带 `authority_id`，K 对应 `idempotency_key`，r 对应现存 Task aggregate 的 `expected_revision`。Agent 调用声明 `as_agent_id`，执行凭证为 `lease:{assignment_id,generation,token}`。create/createChild 没有 child 的 expected_revision，child 从 revision=1 开始；createChild 不写 parent revision。`read(T)` 表示当前授权的公开完整事实读取。`events(W)` 表示同一合法查询范围从水位 W 之后读取到当前已知水位。文中 ID 为符号标签，由实际返回值绑定，不能假设服务端固定 ID。

所有成功变更事务使受影响 Task aggregate revision 增加一次（创建从 revision=1 开始）；同事务多个对象/事件共享该次 aggregate revision，不按对象数量加多次。事件以原子 batch 返回，每个事件有 event_id，batch 有 commit_id，读取响应有 next_cursor，分页不得拆开 batch。拒绝请求和已提交回执重放不得引起新的业务 revision 或成功业务事件。独立 expiry 物化事务可以在请求失败前已合法提交，因此失败断言必须区分「该请求没有业务写入」和「整个系统没有变化」。

错误断言读取 JSON-RPC error 中的稳定类型：`STALE_REVISION`、`INVALID_STATE`、`LEASE_FENCED`、`FORBIDDEN`、`NOT_FOUND`、`GATE_CLOSED`、`POLICY_DENIED`、`IDEMPOTENCY_CONFLICT`、`REINITIALIZE_REQUIRED`。数值映射和认证/解析错误以 wire contract 为准。不得只断言「返回非 200」而忽略错误体。

## 2. 公共 fixture

| 符号 | 配置 |
|---|---|
| O | 预注册 Owner/Issuer Principal，具有本测试 scope 的创建、发布、管理、直接授权和读取权限 |
| A / B | 两个预注册 logical Agent，各自有可验证 Principal 绑定和独立客户端集成；两份凭证不可互换 |
| X | 发布时指定的验收 Principal，拥有当前有效的验收与读取权限 |
| R | 只读消费者 Principal，拥有规定 scope 的 read/replay，没有 claim、执行或管理权限 |
| P / C | P 仅 A 可领；C 为 P 的 child，仅 B 可领；两者选择 `safe_reopen`；保持独立直接 grants |
| Qs / Qm | 两个独立任务，发布时均允许 A/B；分别为 `safe_reopen` / `manual_reopen` |
| T | 向量专用交付任务，规范性输入固定版本，`allow_rework` 按向量显式设置 |
| t / d | bridge 权威时间和 Assignment 的 lease 截止值，UTC RFC3339 毫秒 Z；`t < d` 有效，`t >= d` 即到期 |
| W / E | 一致快照提交水位与服务端内部授权版本；W 公开返回，E 由 opaque cursor 绑定，不由客户端合成 |

为真实交接准备固定内容的输入资源，例如版本化技术说明及其摘要；B 可实际读取输入，X 可实际读取交付。目标、约束、期望输出和验收说明完整写入工作约定，不能靠 A 的私有会话补齐。R 的 fixture 范围包含向量涉及的 Task 历史，避免权限过滤使“事件不存在”的断言失真。

## 3. 正向互操作 H01

1. O `task.create(P)`、`task.publish(P)`；A 查询并 `task.claim(P)`，核对返回 Assignment AP、generation 与截止值；`run.start(P, AP)` 返回 RP。A 的监督器在后续流程维持 P 的有效 lease。
2. A 用明确独立的 Owner/Issuer 授权调用 `task.createChild(parent=P, child=C, eligible=B)`；核对 C 初始为 draft、Relation 指向 P、grant scope/期限和验收主体。随后有权主体 `task.publish(C)`。B 在发布前不得领取 C，发布后才通过 pull 发现它。
3. B 的独立客户端从公共 Task 与引用读取完整约定并实际取得输入；不访问 A 会话或 bridge 数据库。B `task.claim(C)` → `run.start(C)` → `run.report(C)`，分别保存 AC、RC 和报告标识。
4. B 生成可访问审阅产物，`run.submit(C, RC)` 得 SC。一次提交后读取 C=submitted、AC=completed、RC=submitted、唯一 SC；不再要求 B 续租。
5. X 实际读取交付后 `task.accept(C, SC)`，核对唯一 DC 与 SC 绑定；RC/SC 的执行和交付事实不被改写。P 除自身续租等显式操作外不发生状态联动。
6. R 用一致快照加 W 后事件恢复视图，公开读取 AP/RP、AC/RC/SC/DC 与 Relation；比较稳定 ID、状态和内容版本。断线恢复后重复读取一个已处理事件，投影只应用一次。
7. 另起无父 lease 的 fixture：O 凭合法独立权限创建 child 成功，证明父承接不是授权前提。只有 parent_id、无创建/关联/授予权限的 A 则失败。

通过必须同时提供两份客户端集成来源、B/X 的资源访问证据和 R 的公开协议重建证据。同源 SDK 双开只能算竞态负载测试，不能单独满足 H01。

## 4. 原 14 条反例的具体向量

### V01 — 同内容版本拒绝后重做

- 前置：T 已发布，内容版本 v1，`allow_rework=true`；B 经 claim/start/submit 产生 S1，X reject 得 D1。
- 输入：O 以 T 最新 revision `task.reopen`；B 新 claim/start/submit 得 A2/R2/S2；X accept(S2) 得 D2。每步使用新意图键，所有内容引用仍为 v1。
- 断言：D1=rejected、D2=accepted，分别绑定 S1/S2；A1≠A2、R1≠R2、S1≠S2；历史不覆盖，当前 T=accepted。对旧 S1 用新键 accept/reject 均拒绝；用原 reject 键重放仅返回 D1，不把 T 改回 rejected。
- 事件：每份 Submission 仅一个正式提交事实，每份 Decision 仅一个最终决定事实；reopen 有一次显式恢复事实，不包含自动 Run 创建。

### V02 — 重做门槛与终态不可绕过

- 分别建立 T1=rejected 且 `allow_rework=false`、T2=accepted、T3=closed；以有权 O、最新 revision、新键调用 reopen，再由合资格 B claim。
- 断言：不允许重做的 rejected 返回 `POLICY_DENIED`，accepted/closed 的 reopen 返回 `INVALID_STATE`；非法 claim 返回 `INVALID_STATE`。Task/历史不变；`safe_reopen` 或 gate=claimable 不能绕过生命周期。另在 allow_rework=true 的 rejected 上由仅验收权限 X reopen，返回 `FORBIDDEN`。
- 事件：这些请求不生成 reopen/claim 成功事实；原 Submission/Decision ID 保留。

### V03 — submit 丢响应及过期执行凭证的回执恢复

- 前置：B active(T)，记录完整 submit 请求、键 K、原 expected revision r、当前 token/generation。让 submit 事务提交后丢弃 HTTP 响应。
- 输入：保持 B 的当前认证与 T/回执读取权；原样重试 K（包括原 r）；再以新键用旧执行授权 run.report 或 run.submit。
- 断言：重放返回原 Submission ID 与原业务结果，Task revision 不再增加；新写入先取最新 revision，再因 Assignment 已 completed 返回 `LEASE_FENCED`；携旧 revision 时按优先级先返回 `STALE_REVISION`。验收 X 无需原 B lease 有效即可正常决定该 Submission。
- 事件：只有一次 Submission/提交业务变更。重试响应 envelope 可以标明 replay，但不能伪造第二次提交时间。

### V04 — 已有回执不能绕过当前读取权限

- 前置：完成 V03 的成功提交；随后撤销 B 的结果读取权或停用当前认证凭证。
- 输入：原主体使用仍可认证但无相关读取权的凭证重放 K；另以已停用凭证重放。
- 断言：分别以 `NOT_FOUND` / 认证失败拒绝披露原结果、产物引用或 token。有权 O 仍能读取原 Submission，说明撤读没有删除业务事实或历史回执。恢复读取权后原回执仍可恢复；M1 完整存储生命周期内不 GC 回执。
- 事件：拒绝回执读取不产生第二次提交；管理撤销事件与业务提交事件分开计数。

### V05 — 事务外检查不能赢过已提交撤销

- 前置：B active(T)，在其 submit 完成认证/预检后、取得最终写事务前暂停。
- 输入：O 完成 `assignment.revoke` 并提交；恢复 B 的旧请求。
- 断言：B 的事务重新读取授权、revision、generation 和状态；拒绝旧写入，不创建 Submission。若先因 CAS 返回 `STALE_REVISION`，B 用新键和最新 r 重试仍返回 `LEASE_FENCED`，排除只靠 stale 偶然保护的实现。
- 事件：有一次 revoke/承接结束事实，没有该旧请求的提交成功事实；依 recovery policy 设置 T 的 gate，不宣称 B 进程停止。

### V06 — submit 与 expiry 的两个线性化顺序

- 顺序 A：在 t<d 时 submit 完成最终事务检查并提交；随后把时钟推进到 d 并运行维护。断言 T=submitted、Assignment=completed、Run=submitted，不生成该 Assignment 的 expiry，不把 T 重开。
- 顺序 B：先在 t=d 物化 expiry 并提交；随后旧 submit 进入事务。断言拒绝旧写入，T 按恢复策略 offered，Run=ended、end_reason=lease_expired，Submission 不存在。
- 边界 C：t=d 时旧 submit 即使在 d 前做过预检，也不能靠尚未运行的扫描器通过；以最新 r 重试仍失败。测试宿主应分别控制权威时间采样与锁获取，不用不确定的 sleep 猜测竞争。
- 事件：对同一有效承接不能同时出现成功 Submission 与随后生效的旧 lease expiry。维护重试不得重复物化。

### V07 — createChild 三项授权与原子回滚

- 前置：A 对 P 可读且有创建、关联权限，但请求授予 B 的某 action/scope/期限超出 A 的显式授权范围。
- 输入：A 发送 child、parent Relation、初始 B grant 的原子 `task.createChild` 请求；再分别构造缺创建权和缺关联权的变体。
- 断言：任一校验失败均不留下 C、Relation 或 grant；通过有权 O 的快照/列表和事件确认，不以失败调用者看不到 C 作为唯一证据。P revision 不因失败命令变化。
- 对照：补齐权限后以新键重试成功，创建 draft C，随后仍需单独 publish；不能借 createChild 绕过冻结时的约定检查。

### V08 — 只读消费者可枚举全生命周期

- 前置：R 可读 scope 中分别含 offered、submitted、accepted、closed 四个 Task；另有 R 不可读的 Task U。R 没有 claim 权限。
- 输入：snapshot.open/page 普通枚举与 task.get/record.get 单对象读取；另使用显式 claim eligibility 发现过滤。
- 断言：普通快照包含四个可读任务及其可读历史，不含 U；缺 claim 权不会删除终态项。领取过滤不得污染普通枚举语义；其空集/授权错误按对应查询方法断言。
- 事件：R 可恢复其可读集合的交付/决定；读取本身不改变 Task revision。

### V09 — 分页快照期间提交不会漏读

- 前置：R 开始快照获得快照 ID、水位 W、绑定授权版本 E 的 opaque cursor；fixture 足够大以产生至少两页，T 在 W 时 active。
- 输入：读取第一页后 B submit(T) 提交为 W 之后的事务；继续读完该快照，再从其 W replay。
- 断言：所有快照页表示同一 W：T 在快照中仍为 active，不混入新 Submission；W 后 replay 含提交事实，装载加 replay 后的投影为 submitted 且含唯一 Submission。若 T 所在页是第二页，也必须保持 W 的旧状态。
- 事件：消费按 event_id 去重并与 checkpoint 同步持久化；故意在投影事务提交前崩溃再恢复，不能先推进 checkpoint 而丢提交。

### V10 — 读到末尾、授权变化与 cursor 失效

- 分支 A：R 读到当前末尾，保存返回 cursor；之后新 Task 提交，R 从原 cursor 续读。必须读到新事件，过滤后空页也必须保持合法前进位置，不能把末尾当永久终止。
- 分支 B：在快照分页中或正常 replay 后更改全局 auth_epoch；下一页/次 replay 必须返回 `REINITIALIZE_REQUIRED` 并要求按当前授权重建，不继续拼接旧授权集合。R 替换活动投影，不能保留已撤读的对象作为当前可用数据。
- 分支 C：将 snapshot 时间推进超过 300 秒，核对 `REINITIALIZE_REQUIRED`；构造损坏/范围不匹配的 cursor，核对 `INVALID_CURSOR`。M1 不正常 GC 事件，因此“所需事件保留区间消失”仅作为不支持的存储重置/数据缺口故障，必须明确拒绝恢复，禁止静默跳最新水位；不要为该测试新增生产 GC。
- 分支 D：将一个主体或过滤范围的 cursor 交给另一主体或另一个过滤范围；拒绝不匹配的续读，不泄露原集合。历史已披露内容不在远程擦除承诺内。

### V11 — child 故障不改变父任务

- 前置：A active(P)，由监督器保持 P lease；C 仅 B 可领且 active。记录 P 状态、内容版本和在暂停 P 自身续租窗口的 revision。
- 分支 A：只使 C 到期并物化；C=offered，按 C 的 safe policy 可重新领取，但只有 B 合资格。A 对 C 的 claim 失败，不能因 C 过期扩大主体范围。
- 分支 B：独立 fixture 中 B 提交 C，X reject(C)。P 仍 active，不自动 blocked/rejected/cancelled。
- 事件：C 的 expiry/reject 仅改变 C；若同时存在 P 的续租事件，应按原因分开，不能误报“P 完全无事件”。P 后续变化需有权上层显式命令。

### V12 — 父 lease 失效不撤销独立 child 权限

- 前置：C 由直接 Owner/Issuer grants 授权 B、lease 仍有效；A 另外持有独立创建/管理及指定验收权限。
- 输入：只使 AP 到期；旧 AP 执行 run.report(P)；B 对 C 正常 report/submit；A 凭独立授权执行合法 child 创建或对其被指定的当前 Submission 作决定。
- 断言：旧 AP 写入被拒绝；C 不被删除、取消或隐式撤读；B 与 A 的独立权限按自身范围/状态继续判断。不能因为 A 是旧 holder 就全 Principal 禁写，也不能因为它曾持父 lease 自动授予额外权限。
- 事件：P 的承接失效与 C 后续独立命令可分别追溯，无自动父子级联事务。

### V13 — safe/manual 重领与 expiry 入口 CAS

- 前置：Qs/Qm 均允许 A/B，B 已领取执行，公开读取分别记 r；让当前 lease 到期但暂停维护。
- 输入：A 使用旧 r claim。新命令前的独立 expiry 物化提交一次，Task revision 增一；该请求因 stale/状态守卫拒绝。A 重读、使用新键与最新 r 再 claim。
- 断言：Qs 允许 A 合法领取新 Assignment；Qm 因 gate=reopen_required 返回 `GATE_CLOSED`，直到 O 以最新 r reopen 后才允许。每份 Task 同时至多一个有效 Assignment，旧 B 代次不能写。
- 事件：expiry 独立提交，不随失败 claim 回滚；再次读取/重试不会反复物化同一 expiry 而永久 stale。历史 Run=ended 且原因保留，重新 claim 不直接创建 Run。

### V14 — 停机到期与幂等补扫

- 前置：B active(T)；正常持久化存储完整停止 daemon，使 lease 在停机期间到期；保持相同 authority 身份及完整数据重启。
- 输入：重启后只允许维护和公开读；在部署公布的维护界限内观察，再重复扫描/重启一次。
- 断言：出现一次承接到期事实，旧执行授权立即无效，Task 的恢复 gate 正确；不需要某客户端先写入才能物化。第二次扫描没有第二次 expiry 或多余 revision。
- 限制：不以旧备份恢复、跨 authority 迁移或双活替代本 fixture；维护界限和重启后时钟策略须作为部署测试配置记录。

## 5. 补充契约反例

| ID | 输入与时序 | 公开断言 |
|---|---|---|
| V15 | offered Q 同一 revision，A/B 同时不同键 claim；败方重读后仍请求 claim | 恰一个新 Assignment；败方返回 stale 或冲突，不能在未结束当前承接时领取；同一 winning 请求并发同键仅一个提交 |
| V16 | renew 在 t<d 成功；在 t=d、expiry 提交后和 completed 后各调用；另在 renew 前暂停后让 revoke 提交 | 续租只在有效当前承接内生效，不复活过期/结束代次；revoke 后旧请求最终事务必须失败；renew 事件/revision 仅计成功一次 |
| V17 | 成功键 K 原样重放、改 expected revision/目标/载荷复用 K；同键在途时断线重试；未知响应时立即查回执 | 等价重放只读历史；不同语义参数复用已成功键返回 `IDEMPOTENCY_CONFLICT`；未查到回执不允许判定未执行并换键/参数重做；明确拒绝只证明本次调用未提交；只有已知无未知/在途副本的同步确定失败才能改参复用空键或换新键 |
| V18 | draft 更新约定后发布；发布后尝试改目标、验收者、eligible agents、恢复/重做策略或规范性引用 | draft 合法更新进入 revision；发布后拒绝改约定；可变 URL 不满足规范性版本冻结，合法引用可恢复发布时内容；运行状态 revision 不改变内容版本 |
| V19 | 各自在 claimed/active 调用 release、revoke、cancel；在 submitted/accepted/closed 用旧执行 token 重试新意图 | 合法动作按状态矩阵结束相应承接并保留原因；cancel 不能误走恢复 offered；已完成历史承接不能重新结束或重开当前 Task；同键回执仍按读取规则恢复 |
| V20 | 合法 Principal 伪造 agent_id、复制另一 Agent 的 lease token、撤销 binding 后请求 report | token 本身不足以越过 Principal-Agent 绑定；当前授权最终检查拒绝；已持有独立 Owner 权限的管理命令另按自身规则判断 |
| V21 | 无接受权限者 accept；非指定验收者 accept；X 同时 accept/reject 当前 S，随后对同一 S 新键再决定 | 未授权者拒绝；并发最终仅一个 Decision，Task 与其一致；无第二个最终决定；Run 保持 submitted 不改为业务成功/失败 |
| V22 | 在成功事务提交前终止 daemon，再用原键恢复；另在提交后响应发送前终止 | 前者无部分 canonical/事件/成功回执；后者恢复同一业务结果。通过公开读取与事件判断，不依赖内部 SQLite 断言 |

V17 另设精确时序：task.create(K) 超时后产生两个同键副本；副本一在权限不足时返回 FORBIDDEN，随后管理员授予权限，仍在途副本二成功。客户端不得因副本一被拒绝就建立 K2 或改变 K 参数；原键恢复只能得到一份 Task。此时序只验证桥内重复意图控制，不宣称外部副作用 exactly-once。

V19 的状态覆盖应展开为状态契约所有允许/禁止格，而不是只选一个正常状态。V22 的注入位置必须由测试宿主确认，否则只能记录“未知时点重启”，不能当作原子事务证据。

## 6. Schema 与跨语言 golden vectors

静态 conformance 与运行互操作分开记录。Schema 通过只证明结构；权限、lease、CAS、内容冻结和跨对象引用仍须运行向量验证。

| ID | 固定输入/变体 | 预期 |
|---|---|---|
| G01 | 最小合法 Task 约定、Submission、Decision、Event 和 JSON-RPC request/result/error 示例 | 两个独立语言实现按同一 Schema/绑定规则接受，并往返保留所有规范字段 |
| G02 | Decision outcome=`rework`；未知核心状态；遗漏所需稳定 ID、内容版本、输入或结果字段 | 结构不合法；不能靠 extension 改写规范核心状态/outcome |
| G03 | 无命名空间未知顶层字段；合法 namespaced extension；已知字段显式 null 与省略分别构造 | 按 Schema 明确拒绝/保留；客户端不得静默抹除规范允许的未知 extension |
| G04 | UTC RFC3339 毫秒 Z 截止前、恰等于截止、截止后的时间；非法时区/精度；超安全数值的 revision/generation | 按 wire 的固定时间和整数表示解析，不由语言浮点或本地时区改变判定 |
| G05 | 幂等载荷对象键顺序互换、数组顺序互换、可选字段省略/显式默认、不同 id/request 关联号 | 键顺序等价；其余是否等价按wire 第 5 节 canonicalization 规则，输出固定请求摘要测试值，跨语言一致 |
| G06 | 规范性引用只提供可变 URL；引用固定 digest/version；资源实际不可访问 | Schema/发布契约分别报告结构与冻结约束；实际不可访问由 H01 资源访问验证暴露，不伪装为桥已验证内容 |

G01–G06 的精确 JSON、预期接受/拒绝及摘要 golden 值已写入本阶段 Schema/wire 附件。静态检查结果单独记录；跨语言执行结果在客户端实现后追加，不能提前写 PASS。

## 7. 本轮补充：授权实例与方法结果

### V23 — 停用后重新启用不能复活旧承接

- B 用 binding_B_1 和 grant_B 领取 T，保持 lease 尚未到期；暂停维护。管理员停用 B 的 Agent 或 Principal，配置事务永久撤销 binding_B_1。
- 在维护前重新启用该身份，并建立 binding_B_2。B 使用旧 Assignment/token、最新 Task revision 执行 report/renew/submit；必须拒绝，不能使用新的 binding_B_2 代替旧实例。
- 维护只产生一次 authorization_revoked 结束事实；T 按恢复策略开放后，B 需新 claim 才能获得绑定 binding_B_2 的 Assignment。对已撤销 binding/grant 的配置重导入不得复活原 ID。
- 另将 binding_B_1 自然到期后新建 B_2，旧承接同样不能复活；因为 lease 截止被 binding.expires_at 封顶，此分支按 expiry 优先物化为 expired/lease_expired，而非 authorization_revoked。保留 B 的独立 Principal read 避免不可见性错误掩盖执行守卫。

### V24 — 授权自然生效/到期与回执隔离

- R 建立快照，auth_epoch=E；预配置授权在 t1 到期或未来 grant 在 t1 生效。只推进 server clock，不修改 E。t>=t1 旧 snapshot/replay 返回 REINITIALIZE_REQUIRED，新快照按新授权集合加载。
- O 对 T 有 read，但用 O 自己命名空间 receipt.get(B 的 K) 不得到 B 的 claim token；无对应自身回执时 RECEIPT_NOT_FOUND。B 在有独立 read 与有效当前 binding 时可以读取历史 claim 回执，但不能用历史 token 新写。
- grant_B 撤销后，有另一独立 read 的 B 新执行先 FORBIDDEN；原 claim grant 仍有效、仅 Assignment 被 revoke 时，以最新 revision 请求则 LEASE_FENCED。grant.issue/revoke 在终态允许管理权限但不改变 Task 终态。

### V25 — 历史控制审计与失效读取提示

- T 在 R 接入前已 reopen/cancel。R 用 snapshot 初始化当前对象后，另从 events.open 的历史起点 events.read，能读到旧控制事务的 action/actor/request_id/reason；快照不假装含所有历史 Task 状态。
- 到期后、维护前 task.get 可能仍显示 active；不得据此直接恢复外部执行。客户端先调用当前 fenced 命令并收到拒绝，即使旧 claim 回执可读取，也不启动新的外部工作。
- 验证批次审计不含 bearer/lease token；同键回执重放不新增第二个审计 batch，RPC 响应 id 可以不同而原 batch.audit.request_id 保留首次请求 id。

实体附件见 [Schema](../../protocol/m1/schema.json)、[golden vectors](../../protocol/m1/golden-vectors.json) 与 [检查器](../../protocol/m1/check-contract.py)。当前附件有 27 个方法请求形状，正/负结构样例、固定 canonical 字节/hash 与解析样例。静态检查不能把 H01/V01–V25 标为已运行。

## 8. 交付与停止条件

设计阶段交付为：H01、V01–V25、G01–G06 与最终方法/错误/字段的对应关系。协议冻结审查应检查每个向量都有明确前置、输入、调度点、公开输出与不变量；任何 wire 规则或 golden 实体向量缺口须在冻结时收敛。

实现阶段每条记录 `not_run / pass / fail / blocked`、客户端/daemon 版本、fixture 参数、证据位置；未实现与无证据均为 not_run，不能因文档自洽记 pass。M1 通过要求真实 H01、状态/授权/恢复反例和独立读取证据；第二语言 conformance 单独报告覆盖程度。全部测试只在参考 daemon 和客户端存在后执行。
