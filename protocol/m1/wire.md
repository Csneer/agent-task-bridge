# Agent↔Task Bridge M1 — HTTP / JSON-RPC 契约

日期：2026-10-30。状态：**已冻结实施基线 `bridge-m1-1`；没有 daemon 实现或互操作测试结果。**

配套：[状态与事务](state.md)、[身份与授权](auth.md)、[黑盒向量](../../docs/validation/vectors.md)、[JSON Schema](schema.json)、[结构与摘要样例](golden-vectors.json)。本文件固定 M1 的 binding 选择，不改变 Core v1 的业务边界；历史 v9 不作为依据。

## 1. 协议标识、数据规则与范围

- `protocol_version="bridge-m1-1"`，流程 `delivery-acceptance-v1`。这是 M1 首个实施基线标识，不宣称通用稳定 v1。`bridge.capabilities` 返回版本、authority、方法及限制；不支持的版本不能静默降级。
- 一个持久存储只属于一个 `authority_id`，所有方法显式携带它，capabilities 除外。完整存储正常重启保留 authority、事件、回执及密钥；新建存储使用新 authority。旧备份覆盖、双活或自动迁移不属于本 profile。
- 稳定 ID 是区分大小写、不透明的 ASCII 字符串；客户端不从 ID 提取时间或权限。revision、generation 使用 `0..9007199254740991` 的 JSON 整数，Task revision 从 1 起。到达上限返回容量错误，不能回绕。时间严格为 `YYYY-MM-DDTHH:mm:ss.sssZ`（合法公历日期、秒值 00..59，不表示闰秒），使用 bridge 时钟，截止边界不包含本身。
- JSON 使用 UTF-8；解析必须先按原始数值词法的精确数学值检查整数与范围，再转换为语言数值，不能先浮点舍入（1e0 合法，1.0000000000000001、9007199254740990.5 和 1e-400 均拒绝）。本 profile 的全部数字（含 extensions）仅允许安全整数值，拒绝分数、重复对象键、孤立 surrogate、NaN/Infinity 和超安全整数。核心对象拒绝未知字段；扩展只在 `extensions` 下使用带点的命名空间键，例如 `org.example.review`。扩展不能重定义核心字段，M1 不能强制执行未知业务策略。
- 对象规范化使用 [RFC 8785 JCS](https://www.rfc-editor.org/rfc/rfc8785)。对象键顺序不影响等价性；数组顺序有意义；省略字段与显式 null 不等价。解析后不注入默认字段、不进行 Unicode normalization；只有 Schema 明确允许 null 的字段才能传 null。Schema 使用 [JSON Schema 2020-12](https://json-schema.org/draft/2020-12)；格式校验和跨字段语义仍由本契约补充。

## 2. HTTP binding

`POST /rpc`，`Content-Type: application/json`，`Authorization: Bearer …`。跨主机使用 TLS；明文只允许 loopback 开发。响应 `Cache-Control: no-store`，日志不得记录 Authorization、lease token 或含 secret 的原始请求/回执。

请求遵循 [JSON-RPC 2.0](https://www.jsonrpc.org/specification) 的命名参数调用，`jsonrpc="2.0"`、非空字符串 `id`、精确方法名和对象 `params` 必填。id 仅关联传输响应，不是幂等键。

M1 binding 不执行 batch 或 notification：顶层数组以 HTTP 400 的 transport error 拒绝，整个数组不执行；无 id 的 notification 以 HTTP 204 空响应结束，不执行命令，不返回 JSON-RPC error。该限制是 profile 能力，不声称支持 JSON-RPC 的所有可选调用形态。无效/不允许的 id 使用 JSON-RPC Invalid Request，response id=null。

| 情况 | HTTP / 响应 |
|---|---|
| 非 POST、非 JSON、超体积、batch | 405 / 415 / 413 / 400；`{"error":{"code":"TRANSPORT_REJECTED","message":"…"}}`；不执行 |
| 缺失/失效凭据 | 401，同型 transport error，code=UNAUTHENTICATED；不回显敏感参数 |
| JSON 解析、RPC 结构、方法/参数错误 | HTTP 200，JSON-RPC -32700 / -32600 / -32601 / -32602；不可确定 id 时为 null |
| 业务拒绝 | HTTP 200，JSON-RPC error，见第 6 节 |
| 成功 | HTTP 200，JSON-RPC result |
| 未取得事务或服务未 ready | HTTP 503，transport error code=UNAVAILABLE；可含 Retry-After |

HTTP 503、断线、超时、-32603 均应被客户端视为可能结果未知；保留原键原参数重试。有定义的业务拒绝只证明该次调用未提交；同键未知/在途副本仍可能提交，独立维护也可能已经产生事实。

## 3. 请求与响应

所有写方法的共同参数：`authority_id`、`idempotency_key`；已有 Task 的方法另需 `task_id/expected_revision`。Agent 调用提供 `as_agent_id`，实际 Principal 来自认证。执行方法另需 `lease={assignment_id,generation,token}`，start 之后另有 `run_id`。Owner/验收等独立操作不需要 lease。

| 方法 | 除共同参数外的输入 | 成功 data |
|---|---|---|
| task.create | contract、initial_grants[] | CommandResult |
| task.createChild | parent_task_id、contract、initial_grants[] | CommandResult；新 child，父不写 revision |
| task.updateDraft | contract（整体替换，Owner 不可改） | CommandResult |
| task.publish | 无额外字段 | CommandResult |
| task.claim | as_agent_id、claim_grant_id、lease_seconds | CommandResult，唯一带 lease_token 的写结果 |
| assignment.renew | as_agent_id、lease、lease_seconds | CommandResult |
| assignment.release | as_agent_id、lease、reason | CommandResult |
| assignment.revoke | assignment_id、reason | CommandResult |
| run.start | as_agent_id、lease | CommandResult |
| run.report | as_agent_id、lease、run_id、message、evidence_refs[]、extensions | CommandResult |
| run.submit | as_agent_id、lease、run_id、content_version、summary、artifact_refs[]、evidence_refs[]、extensions | CommandResult |
| task.accept / task.reject | submission_id、reason、evidence_refs[] | CommandResult |
| task.reopen / task.cancel / task.close | reason | CommandResult |
| grant.issue | subject_principal_id、agent_id（可 null）、actions[]、not_before、expires_at | CommandResult |
| grant.revoke | grant_id、reason | CommandResult |

`CommandResult={task,records[],commit_id,lease_token?}`：task 是该次提交后的 Task，records 为该事务更改的非 Task 公开对象封套，不能误当成当前最新视图。历史重放返回相同 data。每个对象封套为 `{record_type,record}`，类型名为 `Task/Assignment/Run/Report/Submission/Decision/Relation/Grant`。原始 token 仅在 claim 私有结果出现，公开 Assignment 永不含 token。

JSON-RPC result 为 `{data,meta:{authority_id,replayed}}`；原键命中或 receipt.get 返回 replayed=true，其余 false。JSON-RPC id 使用本次请求的 id。业务 data 中历史时间、revision、对象 ID 不因重放而改变。

| 读取方法 | params（另含 authority_id、可选 as_agent_id） | data 与用途 |
|---|---|---|
| bridge.capabilities | 空对象 | 版本、authority_id、方法清单、Limits |
| task.get | task_id | 当前 Task；读取不物化过期 |
| record.get | task_id、record_type（非 Task）、record_id | 一个 Record 封套，必须确实属于 Task |
| task.discover | limit、可选 after_task_id；as_agent_id 必填 | `{tasks[],next_after_task_id}`，只返回当前可读且有 claim 资格的 offered；提示不预留权利 |
| snapshot.open | scope={owner_principal_id?} | snapshot_id、watermark、replay_cursor、page_cursor、expires_at、auth_valid_until |
| snapshot.page | snapshot_id、page_cursor、limit | `{records[],next_page_cursor,complete}`；全生命周期当前对象及 Assignment/Run/Report/Submission/Decision/Relation/Grant 历史 |
| events.open | scope={owner_principal_id?} | `{cursor,auth_valid_until}`，当前授权下的完整保留历史起点 |
| events.read | cursor、limit | `{batches[],next_cursor,at_head}`；按完整 commit batch 读取 |
| receipt.get | idempotency_key | 原 CommandResult，当前 Principal 的键空间；不存在返回 RECEIPT_NOT_FOUND，不证明没有在途请求 |

普通枚举由 snapshot.open/page 提供，**不额外定义 task.list RPC**。task.discover 只是即时 pull 提示，按 task_id 字节升序分页；并发变化可能使后续页不完整，Agent 必须定期从头重新发现。需要完整集合的消费者使用快照。

## 4. 工作约定、引用与对象语义

Contract 必须包含 profile、owner_principal_id、goal、constraints[]、input_refs[]、expected_output、acceptance_refs[]、context_refs[]、eligible_agent_ids[]、acceptance_principal_id、recovery_policy、allow_rework、lease_policy 和 extensions。没有省略即自动安全重试的默认策略。constraints 为公开声明；桥只强制执行本协议明确的策略字段。

`lease_policy={min_seconds,max_seconds}`，M1 范围 5..300，min<=max；claim/renew 请求位于该冻结区间。lease 截止为 server_now+请求秒数，并受 claim grant.expires_at 与固定 binding.expires_at 的较早值封顶；claim 实际剩余时间少于 min 时拒绝。renew 必须严格延长且不超 grant/binding 截止，不能用旧 deadline 累加请求时长无限预付续租。

资源 URI 使用 RFC 3986 ASCII absolute URI（非 IRI），百分号必须跟两个十六进制字符；userinfo/内嵌凭据禁止。URI 结构合法不保证资源可达。

引用分为 immutable 和 dynamic：

- immutable 必须含 URI、media_type、`sha256`（64 位小写 hex）和 version；定义工作约定的引用、交付 artifact/evidence 只允许此类。version 标识发布者版本，摘要绑定字节；只有可变 URL 和 version 字符串不足以证明内容稳定。
- dynamic 只允许 input/context，必须含 URI、media_type 和明确 read_at 说明；由合同声明 Agent 在什么时机读取。它不能作为变化中的验收定义。桥不抓取任意 URI、不代理外部凭据、不证明可访问性；H01 中 B 与验收者实际读取并验证摘要。

规范性文本直接存于 contract，或以 immutable 引用提供。规范对象仅指向资源，不能嵌入凭据。Task 发布后保存 `contract_digest=sha256(JCS(contract))`，content_version=1；draft 的两者为 null。各 Run/Submission 持久绑定 Task 和 Assignment，不由客户端任意改归属。

## 5. 幂等、回执与敏感 token

键域为 `(authority_id,authenticated_principal_id,idempotency_key)`，覆盖所有写方法。键为客户端随机 UUIDv4 字符串。计算摘要：`SHA256(UTF8(JCS({protocol_version,method,params_without_idempotency_key})))`。JSON-RPC id、HTTP bearer 凭据不参与；authority、as_agent、expected_revision、lease 和所有业务参数均参与。不同方法复用同一已提交键也冲突。JCS 对数组保序；客户端不得将 grants/actions 排序后假定仍是同一请求。

仅持久化成功业务回执，并与状态/事件同一事务提交。确定拒绝不占键；只有可以确认没有未知或在途副本的同步确定失败，才允许改参数复用空键或为新意图换键。一旦曾有未知/并行副本，保持原键原参数，不能因后续一次拒绝或 receipt.get 暂无记录就改参/换键。并发同键通过事务/唯一约束串行化，后继请求读取已提交记录；不能保留一个永不收敛的 pending 占位。

历史回执路径先校验身份与键摘要；等价时只检查当前结果读取权限，不重跑 lease/state。无 read 统一返回 NOT_FOUND；有 read 但敏感 Agent binding 不满足返回 FORBIDDEN。createChild 回执只含 child 事实，不含 parent 内容，恢复需要 child read。receipt.get 不提供他人 principal 参数。原 claim token 返回不代表它目前有效，task.get/record.get 只能核对身份与指针，可能读到尚未物化失效的状态。恢复外部工作前还需成功执行一个当前 fenced 命令：未 start 时 run.start，active 时 run.report，或能实际延长的 assignment.renew；它只证明该命令裁决时有效，不承诺之后持续有效或外部副作用互斥。

token 为 32 个随机字节的 base64url 无填充编码；与 Principal、Agent、不可复活的 binding_id、Assignment、generation 及有效期共同校验。数据库保存 token 验证摘要；含 token 的成功回执用部署外置密钥认证加密，存储 nonce/key_id/ciphertext。普通日志、事件、快照和公开查询均不含 token。丢失解密密钥返回 RECEIPT_UNAVAILABLE，禁止重新执行原命令；密钥恢复与完整存储备份一起验证，M1 不提供在线轮换协议。

M1 不清理事件、成功回执、历史事实或幂等键：其保留期为该 authority 完整存储生命周期。重启保留，不能过一段时间将成功键当新请求。容量不足在提交前返回 RESOURCE_LIMIT，不静默删除历史。未来 retention/GC 必须单独定义墓碑与恢复协议，不能由实现自行添加 TTL。

## 6. 错误与确定性

标准 JSON-RPC 错误保留原代码。业务 `error={code,message,data:{kind,retry,details?}}`；retry 取 none/read_state/same_request/reinitialize。message 可本地化，客户端按数字与 kind 判断。details 只返回当前有权读取的信息，可含 current_revision，不含 token。认证发生在 body 解析前；认证通过后的业务裁决优先级以状态契约第 6 节为准。

| code | kind | retry / 含义 |
|---|---|---|
| -32001 | FORBIDDEN | none；可见目标但当前动作或 Agent 绑定无权 |
| -32002 | NOT_FOUND | none；目标不存在或无读取权，不区分 |
| -32003 | STALE_REVISION | read_state；重读后建立明确的新命令 |
| -32004 | LEASE_FENCED | read_state；旧代次/指针/结束/到期不可新写 |
| -32005 | INVALID_STATE | none；状态/目标/唯一性不允许 |
| -32006 | GATE_CLOSED | read_state；manual recovery 尚未开放 |
| -32007 | POLICY_DENIED | none；冻结策略、授权签发 envelope 或引用语义不满足 |
| -32008 | IDEMPOTENCY_CONFLICT | none；已提交键与请求摘要不一致 |
| -32009 | REINITIALIZE_REQUIRED | reinitialize；授权版本/时间边界、快照到期、存储区间失效 |
| -32010 | INVALID_CURSOR | none；opaque token 无效、主体/scope 不匹配 |
| -32011 | RECEIPT_NOT_FOUND | same_request；本时点无已提交回执，不证明无在途命令 |
| -32012 | RECEIPT_UNAVAILABLE | none；记录存在但敏感结果无法解密，不得重执行 |
| -32013 | AUTHORITY_MISMATCH | none；目标 authority 不匹配 |
| -32014 | RESOURCE_LIMIT | none；请求或快照超可接受资源上限 |
| -32015 | UNAVAILABLE | same_request；暂不可处理，保留原请求 |

运行时查到当前 claim Grant 被撤销属于 FORBIDDEN（当前授权检查先于 lease），旧 Assignment 已被独立 revoke 但原 grant 仍有效，使用最新 revision 新写则 LEASE_FENCED。错误不能随实现任意选取；测试先固定哪些前提仍有效。

## 7. 快照、水位与事件

snapshot.open 在同一数据库读视图检查授权并捕获全部可读公开对象及提交水位 W。实现可在短事务内物化一个有上限的快照缓存；不要求网络分页跨越长 SQLite 事务。所有页使用同一数据、W、主体/Agent/scope 和授权上下文；记录排序为 task_id、record_type、对象 ID 的 ASCII 字节序。不能各页重新查当前数据库拼成快照。open 返回的 replay_cursor 位于 W 之后，只有完整装载并原子替换本地投影后才开始接续。

opaque watermark/cursor 使用服务端持久秘密验证的不可伪造 token 或随机句柄，不暴露其他任务 ID、事件计数或底层全局序号。客户端不解码、不构造。绑定 authority、Principal、可选 as_agent、scope、auth_epoch、auth_valid_until、位置；page_cursor 另绑定 snapshot_id 和页位置。每次响应在一致读取边界校验这些条件。

M1 选全局 auth_epoch，任意 grant/绑定/凭据变更均使旧快照/replay context 重建。`auth_valid_until` 保守取全 authority 已配置权限、binding、凭据的下一个时间生效/截止边界；无有限边界时为 null。自然到期不能靠 epoch 未变继续泄露旧集合。全局策略会使不相关授权变更也中断消费者，是明确的 M1 成本；不作为未来大规模租户方案。重新开始时按当前范围重建，不能只跳到最新 cursor。

每个事实事务为 `CommitBatch={commit_id,committed_at,actor,audit,events[]}`；actor 含 principal_id（维护时为 null）、agent_id（可 null）、kind=principal|authority、authorization_ids[]。audit 固定 action（RPC 方法名或 assignment.expire/assignment.invalidate）、request_id（原始 JSON-RPC id，维护为 null）、reason（命令 reason，无则 null）。这些不是 token/原始请求，必须与事实持久化。每个 Event 含 event_id、type、task_id、task_revision、record_type、record；record 是完整公开 after-image。对象类别由 Schema 约束，type 与操作的映射由状态契约固定。公开事件不包含调用原始请求或凭据。

一个 batch 只涉及一个 Task，授权后全批提供；不可见 Task 的整批跳过，不返回占位。配置变更以管理审计保存且推进 auth_epoch，不暴露全局凭据配置。消费者按 event_id 去重、按 batch 原子更新对象及 checkpoint；删除旧投影仅在重建时发生，M1 不发布对象删除事件。

events.open 为当前主体/scope 创建完整保留历史的起点 cursor；它不读取历史版本授权，而按当前 task.read 授权提供历史公开事实。这样首次接入者也能读取 W 之前的控制动作、actor 和 reason。snapshot 只负责当前对象与保留的对象历史，不保存所有过去的 Task after-image；历史事务审计通过 events.open/read 取得。

events.read 的 limit 是 batch 数，绝不拆开一个 batch；有限扫描后允许空 batches 与前进的 next_cursor。at_head 仅表示本次读取视图已到末尾，不表示流结束；此 cursor 可以读到将来提交。每次读取全局有序已提交 batch；同 cursor 重试可能重复旧批，不能跳过未交付批。调用者不得靠 committed_at 或 UUID 排序替代 cursor。

快照缓存到期、daemon 重启导致缓存丢失、授权版本/时间上下文失效都返回 REINITIALIZE_REQUIRED。合法 replay cursor 与持久密钥正常重启后仍可用。无事件 GC；检测到异常存储缺口时拒绝继续，不能静默重置位置。活动视图需按新授权替换，不承诺远程擦除历史已披露的数据。

## 8. M1 资源与运行配置

以下是参考 profile 的固定初值，capabilities 必须返回；调整需要新 profile 或明确协商，客户端不能猜默认：

| 限制 | 值 |
|---|---|
| request 最大 UTF-8 字节 | 262144；包含扩展与引用，不内嵌大产物 |
| 单 CommandResult / 单 commit batch 最大字节 | 各 1048576；写入前检查；必须留出维护结束事件容量 |
| initial_grants | 最多 16 |
| snapshot / discover / replay limit | 1..100；显式传入 |
| snapshot 生存期 | 300 秒；授权截止可更早失效 |
| snapshot 最大记录 / 字节 | 10000 / 33554432 |
| 同时 snapshot | 每 Principal 2、authority 16；超限拒绝新 open |
| lease 请求秒数 | contract 选择 5..300 内区间 |
| maintenance interval | 1 秒；正常无积压验收场景应在 5 秒内物化 |

snapshot 到期、授权失效或已完整交付后不再占活跃配额；新 open 在容量检查前回收这些缓存。允许保留已完成缓存供最后一页重试，但必须可被回收，回收后再次 page 明确要求重建，不得无界增长。

大于快照上限的部署不属于 M1 已证明容量；不会用分页掩盖无法创建完整快照。过载时 ready/health 报告退化，不能继续宣称 5 秒硬实时。启动补扫完毕才 ready；周期维护和授权失效维护始终重新检查当前指针/状态。测试通过受控时钟与暂停点注入竞争，不要求生产开放任意时间修改 RPC。

clock 下界、SQLite 锁超时、外置密钥加载、资源上限以及停机恢复由参考实现提供运行配置与健康证据。时间异常须 fail closed 为 UNAVAILABLE；已完成/撤销对象不会因时间倒退重新有效。M1 假设单机权威时钟受管理，不承诺恶意宿主或旧备份回滚安全。

## 9. 本阶段验证边界

附件 Schema 覆盖结构与方法参数，golden vectors 覆盖字段、错误、扩展与请求摘要；检查脚本仅做静态 conformance。状态/授权跨对象守卫、CAS、真实资源访问、事务原子性和消费者恢复仍必须由黑盒向量在实现后验证。两种独立语言的 Schema/JCS conformance 与真实 H01 是后续门槛，不能用当前单解释器检查代替。

冻结前复核四项：方法/Schema/错误一一对应；完整状态和授权格覆盖；每个反例有确定错误与事实断言；规范材料进入版本控制。本协议已迁入独立项目的 `protocol/m1/`，冻结清单与修订规则见 [版本规则](../../docs/versioning.md)。
