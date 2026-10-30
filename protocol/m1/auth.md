# Agent↔Task Bridge M1 — 身份与授权契约

日期：2026-10-30（Asia/Shanghai）

状态：**已冻结实施基线 `bridge-m1-1`；实施尚未开始。** 本文细化 [Core Definition v1](../../docs/architecture/core.md) 的直接授权模型，与 [状态契约](state.md) 及 wire contract 配套。只覆盖单 authority、预注册主体和私网 M1，不定义公共账号、递归转授或外部资源访问权。

## 1. 身份、认证与代表关系

- `Principal` 是认证与审计主体，具有稳定 `principal_id`、enabled 状态。请求的真实 Principal 仅由有效凭据解析，不能由 JSON 参数替换。
- `Agent` 是逻辑执行参与者，具有稳定 `agent_id`。预注册绑定 `(principal_id, agent_id)` 具有唯一 `binding_id`、有效期和不可撤回的 revoked_at；`agent_id` 只是请求选择的身份上下文，不是认证凭据。同一 Agent 可配置多个 Principal 绑定，但当前 Assignment 的执行资格限定到领取时的 Principal 与 Agent 组合；续租监督器使用该 Principal 的受控凭据，不通过另一个 Principal 自动代操作。
- `Owner` 是 Task 冻结的 `owner_principal_id`，不是一种可以在请求中自报的超级用户角色。Principal 可以是服务主体；它不必由人持续在线。
- A 代表 Owner 创建 child 的含义是：A 使用自己的凭据，服务端确认 Owner 预先授予的创建/签发资格。审计同时记录实际 `actor_principal_id`、可选 `actor_agent_id` 与 `owner_principal_id`。不得把 actor 改写成 Owner。
- Endpoint 是连接描述；pull 客户端无须注册入站 URL。HTTP bearer 认证用于 M1，私网跨主机使用 TLS；仅明确的 loopback 开发 profile 允许明文。服务端不在查询、事件、日志中披露 bearer token。

Principal、Agent、绑定、凭据以及 Owner 级授权由受信启动配置预注册；M1 不提供公开注册或任意身份管理 RPC。停用/撤销须导入权威存储并产生授权审计与版本变化，不能只替换进程内缓存。配置更新不重写既有 Task 的 owner 或冻结的 Agent 资格集合。

同一 Principal-Agent 组合同时最多一个有效 binding；替换时在同一配置事务撤旧建新。Binding 实例的主体及期限不可在线修改，撤销不可撤回；延期/重新启用使用新 binding_id。Principal 或 Agent 停用的配置事务必须同时永久撤销其所有关联 binding 实例，再由维护逐 Task 物化承接结束。重新启用 Principal/Agent 不恢复这些实例；重新导入配置也不能复活历史 ID。Assignment 固定 claim 时的 binding_id，不能自动切换到新绑定。因此“停用→维护前重新启用”也不能使旧 Assignment 复活。TaskGrant 同样不允许反撤销或修改主体/期限；替代使用新 grant_id。

## 2. 最小直接授权对象

两类记录都由 Owner 的权限边界约束，不构成递归链：

| 对象 | 必要字段 | 作用 |
|---|---|---|
| OwnerCapability（预注册） | id、owner_principal_id、subject_principal_id、actions、允许的 parent/task 范围、可签发 actions、可签发 subjects、max_grant_expires_at、not_before、expires_at、revoked_at | 允许 A 在指定 Owner 名下创建 Task、对指定 parent 建立关系、签发指定范围的直接 task grant |
| TaskGrant（公开事实） | grant_id、owner_principal_id、task_id、subject_principal_id、agent_id（可为 null）、actions、not_before、expires_at、issued_by_principal_id、revoked_at | 对一个确切 Task 的直接授权；不能靠字符串前缀扩大资源范围 |

有效区间统一为 `not_before <= server_now < expires_at`，撤销后立即无效。TaskGrant 的 expires_at 必填；OwnerCapability 的有效期和上限在预注册配置中明确。客户端自报时间无授权效力。M1 不接受通配 TaskGrant，不接受 `parent_grant_id` 或递归授予权限。

Owner 对自己拥有的 Task 有管理和读取资格；这不自动成为指定验收者或候选 Agent。A 的 OwnerCapability 是独立于父 Assignment 的资格。A 签发的 TaskGrant 是 Owner 对 B 的直接授权，保存 A 的签发审计事实；后来撤销 A 的签发资格只禁止 A 新签发，不隐式撤销已经签发的 B grants。需要停止 B 时显式撤销 B 的 grant 或 Assignment。

`grant.issue` 参数为 task_id、subject_principal_id、agent_id（可为 null）、actions、not_before、expires_at，加 wire contract 的 authority_id/idempotency_key/expected_revision。grant_id 与 issued_by_principal_id 由服务端生成。`grant.revoke` 指定 task_id、grant_id 及相同写入元数据。as_agent_id 不赋予独立管理权；一旦提供它，必须存在当前有效绑定。使用 agent_id 限定的 grant 还必须精确匹配它；null 的 grant 仅依赖 Principal。

TaskGrant 的基础动作集合：`task.read`、`task.claim`、`task.updateDraft`、`task.publish`、`task.link`、`task.reopen`、`task.cancel`、`task.close`、`assignment.revoke`、`submission.decide`。签发和撤销 TaskGrant 由 Owner 或预注册 OwnerCapability 控制，不能在普通 TaskGrant 中授出 `grant.issue`、`grant.revoke` 或身份管理能力。

领取 grant 必须同时包含 `task.read` 和 `task.claim`，subject_principal_id 与非空 agent_id 同时指定；验收 grant 包含 `task.read` 和 `submission.decide`，指定 subject_principal_id，agent_id=null。其余 grant 可以按最小权限组合。发布时冻结的 eligible Agent 列表和 acceptance principal 是额外限制：grant 只能在冻结范围内增加或减少当前授权，不能扩大该列表或替换验收主体。

## 3. 方法授权矩阵

所有新写入在同一权威写事务内最终检查当前凭据/绑定状态、授权有效期、Task 状态和 revision。下表动作条件与 [状态契约](state.md) 的状态条件取交集；持有权限不允许跳过生命周期。

| 方法/动作 | 调用者与目标 | 当前授权守卫 |
|---|---|---|
| task.create | Owner，或 OwnerCapability 授权的 A；新 Task | 对指定 owner 的 task.create；请求的 owner、候选 Agent、验收主体与签发初始 grants 均在允许范围；初始 grants 另检查 grant.issue |
| task.createChild | 同上；parent P、新 child C | 创建 C、读取并 task.link P、对 C 签发 grants 分别检查；这些检查与创建/关系/grants 原子完成；Relation 归 C aggregate，C revision=1，P 不写 revision 也不要求入口 CAS；P 的承接身份本身不授权 |
| task.updateDraft | Owner 或持 task.updateDraft 的主体；draft Task | 当前可读、可更新；只能改 draft 内容，不能改 owner 或绕过初始授权边界；与发布冻结互斥 |
| task.publish | Owner 或持 task.publish 的主体；draft Task | 当前可读、可发布；冻结的 eligible Agents 和 acceptance principal 均预注册；创建者身份不自动保留发布权 |
| task.claim | 与请求 Agent 有效绑定的 Principal；offered Task | 显式选择一个有效领取 grant，其 subject_principal_id 等于实际调用者且 agent_id 等于该 Agent；Agent 属于冻结 eligible 集合；read/claim 同 grant；gate、无 live Assignment 等按状态契约 |
| run.start / run.report / run.submit | 当前 Assignment 的原领取 Principal 与 Agent | 当前 claim_grant_id 仍有效、原 binding_id 实例有效、Assignment token/generation 与 Task 当前代次一致；租约未到期；不得用独立管理权限替代执行凭证 |
| assignment.renew / assignment.release | 同上；当前 Assignment | 与执行相同的当前授权检查；renew 后 lease 不超过领取 grant.expires_at 与固定 binding.expires_at 的较早值；release 不允许操纵别人的 Assignment |
| assignment.revoke | Owner 或具 assignment.revoke 的主体；当前 Assignment | 当前独立管理授权和 task.read；不要求调用者持有目标 lease/token |
| task.accept / task.reject | 发布时指定 acceptance principal；当前 Submission | authenticated principal 必须是冻结的 acceptance principal；当前 Owner 资格或 submission.decide grant，以及 task.read；不用原执行者 lease；Policy 在 M1 表示外部验收服务 Principal，桥不执行策略代码 |
| task.reopen / task.cancel / task.close | Owner 或持对应管理动作的主体；Task | task.read 与对应独立管理动作；reopen 还受 allow_rework 或 recovery gate 条件限制 |
| grant.issue | Owner 或预注册有 grant.issue 的 Principal；Task | owner 匹配、Task 可读、recipient/actions/期限在签发 envelope 内；不能因拥有某动作便转授该动作；不得越过冻结 eligible/acceptance 限制 |
| grant.revoke | Owner 或预注册有 grant.revoke 的 Principal；TaskGrant | 目标 grant 属于允许管理的 Task 与 Owner；撤销指定 grant，不隐式撤销同主体的其他独立 grants |
| task.get / record.get / snapshot.open / snapshot.page / events.open / events.read | 有 task.read 的 Principal（Agent 限定 grant 还须有效绑定且匹配 as_agent_id）；授权集合 | 不要求 task.claim；所有生命周期均可读；每次请求按当前授权检查与过滤；lease token 不授予普通读取权 |
| receipt.get / 原键重试读取结果 | 原幂等命名空间的 Principal | 当前认证有效且仍有结果涉及的各 Task 的 read；不要求原 mutation 权限、lease 或状态仍有效；敏感结果另见第 6 节 |

OwnerCapability 中的 task.link 只覆盖明确的 parent 范围；普通 task.link grant 只适用于其精确 task_id。M1 关系由 createChild 创建，不开放发布后任意改写 parent 或跨 Owner 挂接。child 与 parent 使用同一 Owner；未来跨 Owner 委派需单独协议，不能借 Relation 暗中推导授权。

## 4. 撤销、时间与 fencing

Assignment 持久化 `claim_grant_id`、`binding_id`、`principal_id`、`agent_id`、generation 和租约截止。新执行写入只认该绑定的领取 grant，不能在 grant 被撤销后悄悄替换成另一个 grant 来延续旧 Assignment；其他有效 grant 只能用于后续合法 claim。领取/续租的 lease 截止不得晚于 claim grant.expires_at 与固定 binding.expires_at 的较早值，因此 grant 自然到期不会留下比 grant 更长的执行资格。

- 撤销当前 claim grant：同事务把 grant 标记撤销，并结束它支持的当前 Assignment，记录 revoke/fence 和对应 Run 原因，再按 recovery policy 回到 offered/gate。一个 Task 的 revision 在该事务只增加一次。不要另建第二事务来结束执行权。
- 撤销其他 read、管理或验收 grant：只改变该授权与可见集合，不重写 Run/Submission/Decision；若存在另一个独立有效授权，主体仍可按它行事。验收 grant 撤销不删除已作出的 Decision。
- 撤销 A 的独立创建资格不影响 A 的已有效执行 Assignment；撤销 A 的 Assignment 不影响其仍有效的独立创建/验收资格。
- 撤销某 credential：该凭据立即不能认证；相同 Principal 的其他有效凭据仍可操作，不自动撤销 Assignment。停用 Principal、Agent 或该 Principal-Agent binding：对应新执行写立即拒绝；受控维护按与租约到期相同的界限物化受影响的 live Assignment 为 revoked、Run=ended、Task=offered/gate。授权配置更新先提交失效事实与 auth_epoch，跨多 Task 的物化用逐 Task 短事务；物化滞后不形成可写窗口，查询可标示失效而未物化。重复维护不会重复结束同一 Assignment。
- 有限授权到期在最终 guard 中按 server clock 判断，不等待维护线程。授权时间边界同时影响快照访问，见下一节。

撤销与 submit 竞争由写事务串行化：submit 先提交则留下 submitted 与 completed Assignment，后续撤销不撤回交付；撤销先提交则旧代次 submit 被拒绝。不存在“先在事务外鉴权通过，撤销之后仍可提交”的例外。

## 5. 授权版本、读取与审计

M1 使用单 authority 全局 `auth_epoch`：任何授权配置、binding、credential、TaskGrant 的新增/撤销/停用更新均递增。TaskGrant 变更还增加所属 Task 的 aggregate revision，并在同事务写公开授权事件、审计及幂等结果。即使授权变化与某消费者无关，也允许保守地使其快照/cursor 失效；这是 M1 的简单确定性选择，不是未来多租户性能承诺。

时间流逝不产生数据库版本写入，故 snapshot/cursor 除绑定 auth_epoch 外，还绑定 `auth_valid_until`，不得晚于当次授权集合中相关有限权限/绑定/凭据的下一时间边界，包括预定生效的 grants。到达边界必须返回需重新初始化的授权上下文失效，不能在相同 cursor 内静默改变可见集合。Task 命令仍按请求事务当前时间判断。

快照及事件结果必须在同一读取一致性边界检查 auth_epoch 与授权，不能“检查权限后再独立查询”。授权撤销若在线性化点之后提交，已经授权读出的响应可能仍在网络传输；桥不保证远程删除已经披露的信息。客户端收到授权上下文失效后以新快照替换活动投影，不把旧不可读记录继续作为当前授权集合。

对只有 child read 的主体，Relation 可以披露 parent 的不透明 ID，但不返回 parent 内容、状态、grants 或日志；创建时 Owner 应理解该结构引用是 child 的公开事实。快照分页、查询和事件使用一致字段过滤，不通过 replay 暴露普通查询不可读的数据。普通列表不附加 claim eligibility；领取发现是显式过滤。

公开审计存入 CommitBatch.actor/audit：含 action、request_id（维护为 null）及命令 reason（未提供为 null）；Task 控制动作历史可从 events.open 的历史起点读取，快照不包含全部历史 batch。公开审计至少包含 actor principal/agent、Owner、所用授权 id、action、目标 id、server time、Task revision 和 correlation/request id。Task read 允许读取该 Task 的授权与执行审计元数据；全局 credential 与 OwnerCapability 配置不公开。token、凭据秘密、原始认证头不进入审计。拒绝请求可写受控运行日志，但不写伪造业务成功事件。

## 6. 回执与敏感凭证

幂等命名空间绑定实际 Principal，不能用代理 Owner 或 Agent 共享命名空间。原请求结果可能在租约完成后仍可恢复；当前 result-read 权限是独立守卫。当前 mutation 权限撤销而 read 仍在时，可以读取历史成功，不得再次执行 mutation。

Claim 回执包含 lease token 时，只有原 Principal 在当前认证有效、对全部结果目标有 read、仍有相应 Agent binding 的条件下可恢复敏感回执。completed/expired token 仍只表示历史值，不会复活。另一个 Principal 即使属于同一 Agent 或是 Owner，也不能通过 receipt.get 取走别人的 token。同 Principal 的监督器要操作同一 Assignment，token 的安全交接由客户端部署完成，不能从公开 Task 查询获得。

持久化层须保护可恢复回执中的 token（例如用部署密钥加密），执行验证使用不可逆摘要，普通结果/快照/事件永不含 token。密钥不进入 SQLite 数据文件；丢失密钥导致回执不可恢复必须返回明确错误，不能据此重新执行原 claim。token 的具体长度、存储和保留期限由 wire contract 定义。

## 7. 授权错误与验收约束

未认证或失效凭据使用 `UNAUTHENTICATED`；当前主体无目标读取权时用不泄漏目标存在性的 `NOT_FOUND`；可读但没有所需动作使用 `FORBIDDEN`；Agent binding 不匹配使用 `FORBIDDEN`，旧执行代次/租约使用状态契约对应的 fencing/expiry 错误。授权上下文变化用 wire contract 的重建错误，不能返回空页伪装成功。

必须覆盖：A 无父 lease 仍可凭独立资格创建 C；有父 lease 却无 grant.issue 时整个 createChild 失败；向冻结名单外 Agent 发 claim grant 失败；B 到期后 A 独立验收权不消失；撤销 claim grant 与 submit 的两种串行顺序；撤销 A 签发资格不级联 B；R 无 claim 权仍读终态；grant 到期而 auth_epoch 未变时旧 cursor 仍失效；Owner 无法读取其他 Principal 的敏感 claim 回执。

本文明确 M1 的授权选择，不代表这些行为已经实现或经过黑盒验证。字段和方法与 Schema、错误码及状态向量统一；修订遵循 [版本规则](../../docs/versioning.md)。
