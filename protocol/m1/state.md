# Agent↔Task Bridge M1 — 状态与事务契约

日期：2026-10-30。状态：已冻结实施基线 `bridge-m1-1`；运行测试尚未执行。

本文件细化 [Core v1](../../docs/architecture/core.md) 和 [能力蓝图](../../docs/architecture/blueprint.md) 的 `delivery-acceptance-v1` profile。本文约束状态语义；字段编码、授权 action 与错误封装分别由同阶段 wire/auth 契约定义。历史 v9 不作为实现依据。

## 1. 聚合、版本与对象状态

一个 Task 是一个写聚合：Task、其 Assignment、Run、报告、Submission、Decision、child 自己持有的 Relation，以及 task-scoped Grant 均在该 Task 的事务边界内变化。任一成功事实变更使 `Task.revision` **恰好加一**，不按同事务内对象数累加。新 Task 的 revision 为 1。读取、拒绝、已提交幂等回执重放和重复到期扫描不加 revision。

`expected_revision` 对已有 Task 的所有客户端写命令必填，比较当前 Task 聚合 revision。它不是对象局部 revision、内容版本或事件 cursor。`task.create/createChild` 创建新 child，不接收其 expected_revision。认证/授权配置有自己的版本，事务内同样检查当前事实；不能用 Task CAS 替代授权检查。

| 对象 | 状态/关键字段 | 约束 |
|---|---|---|
| Task | `draft/offered/claimed/active/submitted/accepted/rejected/closed/cancelled`；revision、content_version、generation、current_assignment_id、pending_submission_id、recovery_gate | M1 不引入 Task=`expired`，租约过期只属于 Assignment；不提供任务 deadline 自动取消 |
| Assignment | `claimed/active/completed/released/revoked/expired/cancelled`；generation、agent_id、principal_id、binding_id、claim_grant_id、lease_expires_at、ended_at、end_reason | claimed/active 才可能有效；其余状态有 ended_at 且永不恢复；Task 当前指针必须指向它，且当前时间严格早于 lease_expires_at |
| Run | `running/submitted/ended`；assignment_id、started_at、ended_at、end_reason | 每 Assignment 至多一个；ended 只表示桥内报告资格结束，不证明外部进程结束；submitted 不被验收结果改写 |
| Submission | 不可变正式交付；run_id、content_version、artifact/evidence refs、submitted_at | 每 Run 至多一个；只能从 running Run 正式提交 |
| Decision | 不可变；submission_id、`accepted/rejected`、principal_id、reason/evidence、decided_at | 每 Submission 至多一个，不按内容版本限制唯一性 |
| Relation | 不可变 child → parent 引用，归 child 聚合 | 不携带授权、不修改父 Task revision、不汇总状态 |

Task 创建时 `generation=0`，每成功 claim 加一；其他操作不加 generation。结束 Assignment 清除 `current_assignment_id`，通过终态/ended_at 和当前指针 fence 原凭证；不靠结束时再加 generation。历史对象和事件不删除。`pending_submission_id` 仅在 submitted 非空；决定、取消或重开后为 null，已提交历史仍可读取。

`recovery_gate` 为 `claimable/reopen_required`。Task 创建时设 claimable；该值仅在 offered 时参与领取判断，不能把其他状态解释为可领取。contract 在 draft 可整体替换，`content_version=null`；publish 冻结为 `content_version=1`，之后永不改变。M1 不支持在线修改已发布工作约定。

## 2. 时间、过期物化与命令顺序

所有时间为 bridge authority 的 UTC RFC3339 毫秒时间（`Z`）。有效性区间是半开区间：`now < lease_expires_at` 有效，`now >= lease_expires_at` 无效。客户端时钟不参与裁决。授权期限同样使用半开区间，claim/renew 的 lease 不得超过原 claim Grant、固定 binding 的较早截止时间或冻结的最大租约时长。

服务端在持有写事务后读取权威状态，并在最终守卫处采样时间；该点是此命令的线性化裁决点。必须在同事务提交状态、事件与成功回执，不能把事务外时间样本或预检结果带进事务当作最终守卫。数据库繁忙等待发生在采样前。M1 假设 authority 使用受管理的时钟；daemon 至少持久化已使用时间下界，进程内不得倒退，重启不得让已物化失效对象恢复。明显时钟异常应停止接纳 lease 新写并报告不可用，而非接受时间倒退；这不保证不受控主机时钟的真实时间精度。

新客户端命令采用以下固定流程，不能任选另一种顺序：

1. 校验 transport、身份、参数结构；事务内查幂等键。同键不同规范化请求拒绝；等价成功回执命中则按当前结果读取权限返回历史结果，结束事务，**不再执行过期维护或原命令守卫**。
2. 未命中时，仅对已授权访问的目标 Task 运行独立的短维护事务：若当前 claimed/active Assignment 已到期，或其固定 binding 实例已永久撤销/到期，则按第 4 节原子物化一次 expiry/授权撤销并提交；两者均满足时优先 lease expiry。原命令的参数错误或后续失败不能回滚已到期事实；未授权请求不能借此探测/触发目标对象。
3. 开始命令写事务，重新检查身份、幂等、当前授权。读取当前 Task，比较 `expected_revision`。步骤 2 若改变 revision，返回 `STALE_REVISION`；有读取权限时带最新 revision 及需要重读提示，不替客户端改写请求。
4. 检查当前指针、generation、token、lease、目标状态、唯一性和 policy。若在步骤 2 与最终检查间跨过租约边界，拒绝该次执行写为 `LEASE_FENCED`，提交独立 expiry 维护后由客户端重读；不在失败事务中先改 Task 再回滚 expiry。
5. 应用变更、revision +1、记录有序事件及成功回执，提交后返回。任何拒绝都结束事务，不保持锁返回；没有业务部分提交，也不保存成功幂等回执。

步骤 1 与 3 都必须重新原子查询幂等记录，防止两个同键请求在维护间隙分别执行。明确拒绝仅证明该次调用未提交，不能证明同键所有副本都已结束。只在可确认没有未知或在途副本的同步调用中，客户端才能在确定拒绝并重读后修正参数（可复用未占用键，或为新意图换键）。一旦出现超时、断线或并行副本，继续原键原参数重试；后续一次拒绝或暂时查不到回执也不能证明整个意图已失败。一旦存在已提交记录，同键改变 expected_revision 或其他语义参数必须拒绝。

expiry 仅能把一个 current live Assignment 物化一次；重读后的 claim 不会再次因同一 expiry 改 revision，因此不会形成“每次 CAS 都被自己维护改坏”的无限循环。其他并发写仍可能合法导致新的 STALE_REVISION。

## 3. 客户端方法矩阵

所有行都继承第 2 节事务内当前授权、CAS 和幂等规则。表中的 Owner/Issuer、holder、验收者是权限说明，不是仅靠名字即获授权的角色；具体 capability 见授权契约。holder 必须通过 Principal↔Agent 绑定、原 claim Grant、当前 Assignment 和 lease 凭证检查。方法名均为 wire 名；本阶段不增加别名。

| 方法 | 额外 actor/状态守卫 | 原子写集（另含 revision、事件、回执） | 成功结果及主要拒绝 |
|---|---|---|---|
| `task.create` | 有权创建、指定 Owner 和初始授权；引用/contract 结构合法 | 新 draft Task、初始 task grants；无 Assignment/Run | 新 Task rev=1；FORBIDDEN/POLICY_DENIED |
| `task.createChild` | create 的全部要求；父存在且有当前 task.read/task.link 权限；分别验证对 child 的授权授予权 | 新 draft child、child 持有的 parent Relation、初始 grants；任一失败全部回滚；不写父 | child rev=1；父无需 lease/parent CAS；NOT_FOUND/FORBIDDEN |
| `task.updateDraft` | 有独立编辑权；仅 draft；完整替换 contract，不能改 ID/Owner/Relation | draft contract 替换；不变更 grants | draft；非 draft INVALID_STATE |
| `task.publish` | 有发布权；draft；输入可复现、候选主体/验收主体/恢复策略/allow_rework/租约策略完整 | Task→offered，gate=claimable，content_version=1，冻结 contract | offered；不合规 POLICY_DENIED；不替服务端访问外部资源作正确性承诺 |
| `task.claim` | 绑定 as_agent_id；当前 read+claim Grant；在冻结候选主体内；offered、gate=claimable、无有效 Assignment | generation+1，新 Assignment=claimed；lease 受政策/Grant/binding 上限限制；设当前指针；Task→claimed | Assignment 与 secret token；gate 不允许 GATE_CLOSED；资格不允许 FORBIDDEN；其他状态 INVALID_STATE |
| `assignment.renew` | holder；当前 Assignment=claimed/active；Task 相应 claimed/active；尚未到期 | lease_expires_at 更新，其他状态不变；新截止为 max(旧截止, now+请求时长) 后受 Grant/binding 截止及当前请求时长上限约束；必须严格延长 | 新截止；不能延长 POLICY_DENIED；迟到 LEASE_FENCED |
| `run.start` | holder；Task/Assignment=claimed；Assignment 无 Run | 新 Run=running；Assignment→active；Task→active | Run；重复新命令 INVALID_STATE，不创建第二个 Run |
| `run.report` | holder；Task/Assignment=active；目标 Run=running 且属于当前 Assignment | append-only report（进展/证据引用）；不由自报 progress 改 Task 生命周期 | report ID；过期/旧代次 LEASE_FENCED；内容不合规 POLICY_DENIED |
| `run.submit` | holder；Task/Assignment=active；当前 Run=running；无 Submission；content_version 匹配冻结版本 | 新 Submission；Run→submitted、ended_at=now/end_reason=submitted；Assignment→completed、ended_at=now/end_reason=submitted；清当前指针；Task→submitted、pending_submission_id=新 S | Submission；不需要随后续租；任何校验失败无半份提交 |
| `task.accept` | 指定验收主体，且有当前 Owner 资格或验收 Grant；Task=submitted；参数 S 等于 pending_submission_id；S 无 Decision | 新 accepted Decision；Task→accepted；pending_submission_id=null；不改 Run/S | Decision；非当前 S 或已有决定 INVALID_STATE |
| `task.reject` | 与 accept 相同 | 新 rejected Decision；Task→rejected；pending_submission_id=null；不改 Run/S | Decision；不自动 reopen，不创造 rework outcome |
| `assignment.release` | holder；Task/Assignment=claimed 或 active；lease 仍有效 | 按第 4 节 ended=released、Task→offered、策略门控；原因必填 | 结束事实；过期 LEASE_FENCED，不能伪写为 release |
| `assignment.revoke` | 独立撤销权；指明当前 Assignment ID；Task/Assignment=claimed 或 active | 按第 4 节 ended=revoked、Task→offered、策略门控；原因必填 | 不需 holder token；不撤销同主体其他独立 grants；旧对象 INVALID_STATE |
| `task.reopen` | 独立 reopen 权；(a) offered 且 gate=reopen_required；或 (b) rejected 且冻结 allow_rework=true | Task→offered、gate=claimable、pending_submission_id=null；无新 Assignment/Run，不改历史 | offered；已 claimable/其他状态 INVALID_STATE；禁止重做 POLICY_DENIED |
| `task.cancel` | 独立 cancel 权；draft/offered/claimed/active/submitted；原因必填 | Task→cancelled、清 pending/current 指针；有 live Assignment 则置 cancelled；有 running Run 则 ended/task_cancelled；已有 S/Run submitted 原样保留 | cancelled；不制造 Decision；accepted/rejected/closed/cancelled 拒绝 INVALID_STATE |
| `grant.issue` | Owner/Issuer 当前签发权；所有 Task 状态；recipient/actions/期限满足授权 envelope | 创建新的直接 Grant，auth_epoch+1；不改变 Task 生命周期 | Grant；FORBIDDEN/POLICY_DENIED |
| `grant.revoke` | Owner/Issuer 当前撤销权；所有 Task 状态；Grant 属于目标 Task 且尚未撤销 | Grant 标记撤销、auth_epoch+1；若支持当前承接，按第 4 节同事务结束；其他 grants 保留 | Grant；已撤销的新命令 INVALID_STATE；同键可恢复 |
| `task.close` | 独立 close 权；accepted 或 rejected | Task→closed；历史 Decision/S/Run/Assignment 原样保留 | closed；closed 不重开，重复新键 INVALID_STATE |

`task.createChild` 的 Relation 归 child，是新事实而不是修改父工作约定。M1 不允许任意已存在任务重设父关系、跨 authority 关系、递归权限继承或自动 DAG 求值。父处于终态也不自动禁止有权主体创建后续 child；业务如需限制，由上层不发命令或限制授权实现。

`assignment.renew` 的请求时长必须为正并在冻结 min/max lease 范围内。若截止已被 claim Grant 或固定 binding 的 expires_at 封顶，不能声称续租成功但不延长；返回 POLICY_DENIED。renew 与 report 共享 Task CAS，因此 supervisor 与执行者需要重读并重试确定冲突；M1 优先选择单聚合一致性，暂不引入 lease 专用版本。

`task.cancel` 与 accept/submit 竞争由同一 Task CAS 串行裁决：若 accept 先成功，cancel 不能取消 accepted；若 cancel 在 submitted 先成功，之后不能追加 Decision；若 cancel 在 active 先成功，旧 submit 被 fence。已成功的 submit/accept 回执仍可按当前读取权限恢复，不改变 cancelled 或其他当前状态。

## 4. 承接终止与授权撤销

| 触发器 | Assignment 结束状态/原因 | Run（若 running） | Task 与恢复门控 |
|---|---|---|---|
| lease 到期维护 | expired / lease_expired | ended / lease_expired | offered，按 recovery_policy 置 claimable 或 reopen_required |
| holder release | released / released | ended / released | 同上 |
| 独立撤销命令 | revoked / revoked | ended / revoked | 同上 |
| 原 claim Grant 显式 revoke | revoked / revoked | ended / revoked | 同上，与 Grant 撤销处于同事务 |
| task cancel | cancelled / task_cancelled | ended / task_cancelled | cancelled；不自动重新开放 |

每条结束路径写 Assignment.ended_at，清 Task.current_assignment_id；未 start 时没有 Run，不能伪造 Run。Principal/Agent 或其绑定被停用时，事务内授权检查立即拒绝新的执行写；配置变更原子永久撤销相关 binding_id 实例并更新授权版本（重新启用必须用新 binding_id），维护按 Task 独立短事务把仍 live 的 Assignment 置 revoked、Run 置 ended/end_reason=authorization_revoked，并按恢复策略回到 offered。这与 lease 维护使用同一公布的维护时限；不能因未物化而继续接受执行。撤销某一登录 credential 仅使该 credential 失效，不自动撤销同 Principal 使用其他有效凭据的 Assignment。

`Run.ended` 表示后续报告不再接纳，bridge 不为外部执行声明 stopped/failed。Task 发布时选择 safe_reopen 才在前三类承接结束后直接开放门控；manual_reopen 必须显式 task.reopen。授权 revoke 后仍需新 claimant 自身有效 grants；gate 开放不会复活被撤销权限。

`grant.issue/revoke` 的完整 actor/期限/范围规则见授权契约。task-scoped Grant 变化与该 Task revision、授权版本和事件同事务提交；若撤销恰是当前 Assignment.claim_grant_id，整组承接结束写集仍只使 Task revision 加一。不得以撤销普通 read-only Grant 或父 Assignment 作为自动结束 child 的理由。claim Grant 自然到期通过 `lease_expires_at <= min(claim_grant.expires_at,binding.expires_at)` 在租约路径处理。

维护事务以 current_assignment_id、generation 和 live 状态重新检查；旧计时器处理 completed、已被替换或已 ended 对象是无变化 no-op。到期事实的 batch `committed_at` 与 Assignment.ended_at 记录物化裁决时间，Assignment.`effective_at` 记录 lease_expires_at，可观察停机造成的延迟；Assignment.effective_at 在所有非 expiry 状态固定为 null。参考 daemon 启动时先执行补扫，在 ready 之后按宣告的 maintenance interval 持续补扫；没有新客户端命令也必须产生到期事实。重启和扫描重试不能重复推进 revision 或重复写 expiry 事件。

## 5. 事件与回执原子边界

每个成功聚合事务产生一个 commit batch，含 `commit_id/committed_at/actor/audit/events[]`。每个事件含稳定 `event_id/type/task_id/task_revision/record_type/record`，record 是该次提交中该对象的完整公开 after-image，不含 token 或 credential。一个对象每事务至多出现一次最终 after-image；复合变化产生多个对象事件，但整个 batch 必须在同一页交付，cursor 只能越过完整 batch。消费者以 batch 为原子单位更新投影与 checkpoint，不能应用半个 submit。全局提交顺序由 cursor 表达，不能用墙钟或 UUID 排序。所有同聚合对象事件携相同的新 Task.revision。

对象事件按下面固定次序放入 batch；括号中的字段表示 record_type，类型的 wire 拼写与 Schema 对齐：

| 命令 | batch 内事件次序（仅存在的对象） |
|---|---|
| create/createChild | `task.created`(Task) → `relation.created`(Relation，仅 child) → 按 grant_id 排序的 `grant.issued`(Grant) |
| updateDraft/publish | `task.draft_updated` / `task.published`(Task) |
| claim | `assignment.claimed`(Assignment) → `task.claimed`(Task) |
| renew | `assignment.renewed`(Assignment) → `task.updated`(Task，revision 更新) |
| start | `run.started`(Run) → `assignment.activated`(Assignment) → `task.activated`(Task) |
| report | `run.reported`(Report) → `task.updated`(Task，revision 更新) |
| submit | `submission.created`(Submission) → `run.submitted`(Run) → `assignment.completed`(Assignment) → `task.submitted`(Task) |
| accept/reject | `decision.created`(Decision) → `task.accepted` / `task.rejected`(Task) |
| expiry/release/revoke | `assignment.expired/released/revoked`(Assignment) → 若 Run 存在 `run.ended`(Run) → `task.reopened`(Task，含 gate) |
| grant.revoke | `grant.revoked`(Grant) → 若结束当前承接则接上上一行的 Assignment/Run/Task 事件，否则 `task.updated`(Task) |
| grant.issue | `grant.issued`(Grant) → `task.updated`(Task) |
| cancel | 若有 live Assignment，`assignment.cancelled`(Assignment) → 若有 running Run，`run.ended`(Run) → `task.cancelled`(Task) |
| reopen/close | `task.reopened` / `task.closed`(Task) |

`task.reopened` 仅描述回到 offered 或开放 gate，不能推断 gate=claimable；必须以 Task record 为准。创建时多个 Grant 顺序按稳定 ID，避免同一成功回执不同排序。Grant record 是公开安全投影，不能泄露 secret 或额外管理信息；授权过滤和 batch 容量限制见 wire 契约。

audit 固定 action（RPC 方法或维护动作 assignment.expire/assignment.invalidate）、request_id（维护为 null）和 reason（未提供为 null），与 batch 同事务持久化。公开历史控制事务通过 events.open 后的 events.read 读取；快照不代替全历史审计。

表中的事件顺序是公开契约，不要求数据库语句按此顺序执行。可观察单位始终为一个已提交 batch；事件、当前对象事实、幂等成功业务结果同事务落盘，响应只能在 commit 后发出。消费者不需猜测一次命令是否完整。

一次请求可产生独立 expiry 维护事件后再产生客户端成功事件，也可能只产生维护事件再返回 CAS 冲突；这不算失败命令部分成功。维护事件 actor 是 bridge authority，不能伪装成请求者成功执行了原命令。

## 6. 错误裁决与竞争反例

错误按以下优先级裁决，避免实现任意选择：transport/结构错误 → 当前认证/可见性 → 原子幂等冲突或有权历史回执 → 独立 expiry 维护 → 当前写授权 → STALE_REVISION → 执行方法 LEASE_FENCED → INVALID_STATE → GATE_CLOSED/POLICY_DENIED → 唯一性。不可见对象使用 NOT_FOUND；可见但无相应写权使用 FORBIDDEN。唯一性违反说明方法前提已不成立，使用 INVALID_STATE；存储异常不是业务冲突，返回 wire 的不可用/内部错误，不伪装成业务成功。

多条不满足条件同时存在时只返回最先适用错误。比如旧 Agent 携旧 revision 写入，允许先得到 STALE_REVISION；它重读并更新 revision 后仍会得到 LEASE_FENCED，不能越过代次守卫。凭证被整体撤销则先认证失败，不能为了测试期望 LEASE_FENCED 暴露对象。

必须由黑盒向量证明以下确定结果：

- 同 revision 并发 claim 至多一个成功，另一请求冲突；不能生成两份有效 Assignment。
- renew 的最终守卫在 expiry 前通过且先提交，扫描重新读取新截止；扫描先完成则 renew 不能复活旧 Assignment。
- submit 在有效租约内先成功，Assignment completed；后续旧扫描无变化，验收无需续租。
- revoke 原 claim Grant 与 submit 竞争：撤销先提交后，不接纳依赖原 Grant 的新提交；提交先提交后，不撤回历史 Submission。
- 同内容版本 S1 rejected → reopen → 新 Assignment/Run/S2 accepted，旧 S1 的 Decision 不阻塞 S2，也不能迟到推进当前 Task。
- P/C 分别保持另一方租约和独立授权有效；单边 expiry 不改变另一方的 revision/state。
- 失联响应用原键恢复成功业务结果；新键携失效 token 无法产生第二份 Submission。

本文件只提出可评审的确定语义；这些反例尚未在 daemon 上执行。协议冻结须同时核对授权表、wire/schema、验收向量与本矩阵；通过文档检查不能替代互操作验收。
