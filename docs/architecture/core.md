# Agent↔Task Bridge — Core Definition v1

日期：2026-10-30（Asia/Shanghai）

状态：**核心重新划定已采纳；当前 M1 实施基线见 [M1 协议入口](../../protocol/m1/README.md)。** 这是当前需求与边界的主文档。配套材料为 [v1 能力蓝图](blueprint.md) 和 [M1 验收计划](../validation/acceptance.md)。历史访谈、v9 PRD 和 v9 测试表保留用于追溯，不再直接作为实现目标。

## 目标

桥为不同 Agent 客户端提供共同的工作约定、唯一有效承接授权、执行报告、交付记录、验收决定和可恢复的公开事实。首个价值证明是：两个独立 Agent 客户端能在没有共享私有会话的条件下完成一次真实任务交接，独立上层读取方能通过公开契约恢复同一组事实。

桥不是项目管理系统、工作流编排器、执行沙箱或市场。外部项目、Issue、资源系统和支付系统保留自己的业务权威；桥只对桥内发布的工作约定及其授权、执行、交付和决定记录负责。

## 核心对象与权威边界

### Task：有边界的工作约定

Task 是可被独立委派、能够说明输入、边界、预期产物和完成条件的工作单元。它不等于整个项目、外部 Issue、Agent 会话、模型调用或工具调用。

Task 的桥内权威内容包括：目标/意图、机器可判定的接取条件、约束声明、输入引用、预期输出与验收说明、上下文/项目引用、负责人、完成策略和显式关系。一个外部业务目标可以关联多个 Task；外部状态不会静默覆盖 Task 状态，改变桥内约定必须经过授权命令。

Task 发布后，M1 冻结工作约定内容。操作 revision 用于并发控制，不表示目标或验收标准改变。定义目标、约束或验收标准的外部引用必须固定版本或保留当时内容；仅保存一个会变化的 URL 不足以冻结约定。动态输入按约定的读取时间和权限处理。未来若允许修改约定，必须新增内容版本并定义重新确认，不在 M1 隐式支持。

### Assignment：当前承接授权

Assignment 表示某个 Agent 在一个时间范围和权限范围内推进 Task 的资格。一个 Task 同一时刻最多有一个有效 Assignment。这个不变量只保证桥内的授权排他和当前代次的写入资格，不保证旧进程、CI、外部工具或不可逆副作用已经停止。

租约失效、release 或 revoke 会 fence 旧代次；该 Assignment 及依赖其有效性的权限不能再用于新的桥内写入。这不会自动撤销同一 Principal 独立持有的 Owner/Issuer 或验收权限；这些操作仍按其授权来源和目标状态检查。外部资源的互斥、幂等或回滚由资源系统和 Agent 协作负责。桥不会把“旧授权失效”描述成“旧执行已经停止”。

### Run：执行尝试与观察事实

Run 记录一次 Assignment 下的执行尝试、进展报告、来源和结束/失联事实。Run 的执行事实不因验收决定而被改写。Agent 自报“完成”是可审计的报告，不等于桥验证了内容正确。

### Submission：正式交付

Submission 是对某次约定和某个 Run 的正式交付记录，引用产物、证据、版本和可访问位置。已有的中间产物可以先作为证据保存，正式提交时再明确选择交付内容。桥只保存并授权访问引用，不解释业务内容。

### Decision：验收决定

Decision 记录被授权 Principal/Policy 对某个 Submission 作出的接受或拒绝决定。M1 的 outcome 只有 `accepted`、`rejected`；`rework` 不是第三种决定或状态，而是拒绝后按发布时冻结的 `allow_rework` 策略显式重新开放。Decision 不把 Run 事实改写成“成功/失败”；上层可以自行计算整体完成度。`accepted` 只表示指定主体作出了接受决定，不自动表示通用正确性、支付资格或外部项目已完成。

### Relation 与身份

父子、派生、关联和外部映射是显式 Relation。Relation 本身不产生读取、接管、撤销或验收权限。

Principal 是可验证的行为主体；Agent 是被授权代表的逻辑参与者；Session、Process、Model call 和 Endpoint 是通信或执行上下文。pull 客户端不需要开放入站 Endpoint，续租可以由被授权的客户端监督器完成，不要求模型持续推理。

## 核心不变量

1. 一个 Task 同时最多一个有效 Assignment；不同 Task 即使指向同一外部资源，桥也不宣称资源互斥。
2. 当前 Assignment 的代次和授权到期后，旧代次不能写入新的桥内进展、交付，或执行依赖该 Assignment 的其他命令；独立授权与已提交幂等回执的读取分别判断。
3. Task、Assignment、Run、Submission、Decision 的事实含义独立；验收决定不能覆盖执行事实。
4. 父子关系不自动汇总父子状态，也不自动授予权限；上层明确发出业务命令后才改变父任务。
5. 已发布工作约定及其规范性引用在 M1 内可复现；操作 revision 不冒充内容版本。
6. 所有公开事实都能通过授权的协议读取和事件恢复；消费者负责自己的 checkpoint 和去重，桥不承诺消费者已经处理事件。
7. 事件至少一次、命令幂等和写入冲突可观察；不承诺 exactly-once 或外部副作用 exactly-once。

新写入的最终授权、目标 revision、当前 generation、租约时间、状态和唯一性检查，必须使用同一写事务内读取的权威事实；事务外预检不能代替这些检查。权威状态、公开事件和已提交幂等结果在同一事务中提交。已完成命令的回执读取不重新执行当年的租约或状态守卫，也不产生第二次业务变更。

这些不变量的触发器和写集已在 [状态契约](../../protocol/m1/state.md) 细化：租约失效使用 bridge 的权威 server clock；到期后新执行写入即被拒绝，不等待扫描器先运行；过期物化事务同时记录失效并持久化 fencing。M1 参考 daemon 需要启动补扫和有界 lease maintenance loop，或提供等价的受控物化机制；“没有业务 scheduler”不代表过期事实可以永远不产生。重启、时钟边界、renew/submit/claim 竞争和重复物化必须有确定结果。

## 重新开放与租约失效

授权失效和允许再次承接是两个独立决定。当前仍处于 claimed/active 的 Assignment 到期时，桥记录失效并结束该承接；release/revoke 也结束承接，但保留各自原因。对应 Task 回到 `offered`，是否允许再次 claim 取决于发布时冻结的 recovery policy：

- `manual_reopen`：Task 进入“需要重新开放”的不可领取门控；有权 Owner/Issuer 发出 `task.reopen` 后才可再次 claim。
- `safe_reopen`：Task 在上述承接结束后可再次 claim。该策略只适用于发布者明确声明可重复产生候选产物，或外部系统提供充分幂等/对账保证的工作。

两种策略都不自动创建 Run、自动选择 Agent、自动汇总父子状态或声明重试成功。M1 的跨 Agent 演示使用可重复产生候选产物的任务，并显式选择 `safe_reopen`；通用核心不承诺无条件自动重新开放。

`recovery_gate` 是与 Task 生命周期正交的恢复门控：`claimable` 或 `reopen_required`。它不是单独的领取凭证；最终可领取还必须满足 Task=`offered`、没有有效 Assignment、当前读取/claim 授权及主体资格。`safe_reopen` 的承接结束事务 fence 旧 Assignment 并设 gate=`claimable`；`manual_reopen` 设为 `reopen_required`，直到授权的 `task.reopen` 才开放。已完成的历史 Assignment 不再触发 lease expiry；`submitted/accepted/rejected/closed` 不能被它的旧计时器重新开放。拒绝后的重做受 `allow_rework` 和显式 reopen 控制，不能借 `safe_reopen` 自动发生。

两种恢复路径都保留旧 Run/Submission/Decision/事件，不重置旧 Run。操作 revision、门控和事件的变化见状态矩阵。待领取的查询提示不是承接保证；M1 基线选定先独立物化再做新命令 CAS，确定失败后重读，不能混用事务内物化的另一套顺序。

## 委派、子任务与 pull

创建 Task、建立 parent Relation、授予另一 Agent 权限是三个可组合但不同的动作。`createChild` 可以作为原子便利命令，但不得把 parent_id 当成授权来源。

M1 采用显式的 Owner/Issuer 创建和委派授权；当前是否持有父 Assignment 只有在授权明确来源于该 Assignment 时才是前提。创建 Task、建立 parent Relation、授予 B 领取/执行权限分别检查对应的 action；parent_id 本身不授予任何 action。Child 的 Owner、可领取 Agent、验收主体、范围和期限必须显式记录，并在一个可审计的创建/授权事件中关联。父任务失联或到期不会自动删除 child；授权撤销、资源清理和 child 状态改变分别记录。

M1 验收采用一种最小配置：预注册 Owner/Issuer 直接授权，Agent A 可代表该主体创建 child，并在明确许可内授予 B 工作权限。该配置不引入任意递归转授链；范围、期限、撤销和审计仍然必须检查。A 的独立权限不因父 Assignment 到期自动消失，`parent_id` 或父租约本身也不能扩大授权。Assignment 派生授权若后续纳入协议，必须另行明确失效传播，不能由当前配置隐式推出。

定向交给 B 与 pull 不冲突：桥可以把可领取主体限定为 B，B 仍主动发现并 claim。已创建、已发现、已领取、已执行和已交付是不同事实；M1 不做 push、匹配排序或通知队列。

## 首版流程 profile：delivery-acceptance-v1

首版只验证一种明确的交付型流程：

```text
draft → offered → claimed → active → submitted → accepted → closed
                                           └→ rejected → closed
                                                  └→ task.reopen → offered
                                                      （仅 allow_rework=true 且有权发起）
```

这是一种 M1 profile，不是所有未来任务的本体。该 profile 要求发布时选择指定验收 Principal/Policy；未来可以增加自动完成或无需独立验收的 profile，但不得重定义本 profile 中已发布、承接、提交和决定的含义。

在本 profile 中：

- claim 只创建 Assignment；`run.start` 原子创建该 Assignment 的唯一 Run，并将 Task/Assignment 推进为 active。M1 每份 Assignment 至多一个 Run，重试使用新的 Assignment/Run。
- `run.submit` 在同一事务中把 Task 置为 submitted、Assignment 置为 completed 并结束租约、Run 置为 submitted，同时创建唯一 Submission 和对应事件/幂等结果；它不把 Run 改写成验收成功。成功提交后不再需要执行者续租，验收也不要求其 lease 仍有效。
- `task.accept/reject` 由发布时指定且当前仍有授权的 acceptance Principal/Policy 发起，只能对 Task 当前待验收的 Submission 作出决定。**每个 Submission 至多有一个最终 Decision**，唯一性不绑定 Task 内容 revision；同键等价请求可重放已提交结果，对同一 Submission 另建第二个决定或用历史 Submission 推进当前 Task 均被拒绝。Task 流程改变，Run/Submission 事实保留。
- rejected 只有在发布时冻结的 `allow_rework=true` 且有权 Owner/Issuer 显式 `task.reopen` 时才回到 offered，并置 gate=`claimable`；旧 Submission 不再是当前待验收目标。重新承接和提交产生新的 Assignment、Run、Submission；新交付可以在同一冻结内容版本下被 accepted，旧 rejected Decision 仍保留。accepted/rejected 可由有权主体显式 close；closed 不得重开。逐方法授权与状态表已列入 M1 协议基线。
- lease expiry 的 `manual_reopen`/`safe_reopen` 门控按照上一节执行；过期不自动判定交付失败。

## 幂等恢复与公开读取

对相同幂等键、等价请求，先在原子判定下查找已提交回执。命中后验证当前调用者是否有权读取该结果，再返回原业务结果；不重新执行原命令，也不因原 lease 已完成或过期而把历史成功改写成失败。当前凭证失效或结果读取权限被撤销时不能披露回执。返回历史 claim 结果不恢复其中 token 的效力，客户端恢复执行前必须确认当前 Assignment。未命中才进入新写入的当前权限、revision、lease 和状态守卫；键复用不同请求拒绝。键域、摘要、敏感回执保管和错误保留规则见 [wire contract](../../protocol/m1/wire.md)。

读取权限与领取资格分开：Read Consumer 无需 claim 权限，也能枚举所有当前可读的 Task 及其历史事实，包括已提交、已验收或已关闭任务。Agent 的领取发现是在可读集合上显式增加 claim eligibility 过滤，不能成为普通列表的隐含条件。

首次接入或重建的语义基线是“一致查询快照 + 对应提交事件水位 W”：全部分页属于同一快照，消费者装载后从 W 之后连续 replay，再按公开事件恢复后续事实。服务端逐次检查当前读取授权；权限变化、快照/cursor 失效或事件保留不足须显式告知并要求重新初始化，不能跳到当前水位而静默丢失区间。消费者负责 event_id 去重，以及投影更新与 checkpoint 的一致保存。水位后的事件即使在分页期间产生也必须可补读；耗尽一页或当前水位后仍可续读后来提交的事件。具体 cursor 语义、保留期限、快照资源上限与错误码见 wire contract。

## 可移植性与 M1 部署 profile

可移植性分层：

1. 客户端能理解同一工作约定并交付可访问结果——首版必须验证。
2. 不同语言使用同一协议——保留 Schema 和 golden vectors；第二语言 conformance 是独立门槛。
3. 上层只依赖公开接口和事件读取事实——首版必须验证，不得读 SQLite 内部表。
4. 更换 daemon、数据库、机器或进行跨桥联邦——需要额外的权威移交和恢复规则，M1 不保证。

M1 部署 profile 采用单一 bridge authority、预注册 Principal/Agent、私网、独立 daemon、SQLite/WAL、HTTP + JSON-RPC 和 pull。SQLite、Go、HTTP/JSON-RPC 不是核心语义；它们是第一个参考实现的配置。完整存储重启与旧备份恢复不视为同一保证，M1 不支持双活或自动权威迁移。

## 首版验收场景

使用一个可重复产生候选产物的开发/分析任务：Owner 发布父任务 P，A claim/start P；A 凭独立的创建/关联/授权权限创建 child C，写明 B 的领取范围并显式 publish C；独立客户端 B 只领取 C、执行并提交 S_C，指定主体验收 S_C，独立读取程序消费事实。父任务 P 的后续处理由上层明确决定。A 承接 P 是本场景要验证的行为，不是所有 child 创建的权限前提；另设无需父 lease 的 Owner 创建验证。

验收必须证明：

1. A、B 是两个独立客户端集成，不只是同一个客户端进程启动两次。
2. B 仅凭公开 Task 内容和有权访问的引用开始工作，不读取 A 的私有会话或 bridge 内部数据库。
3. 输入与产物对相应主体实际可访问；保存一个 URL 字符串不算交付成功。
4. 执行报告、Submission 和 Decision 可分别读取；拒绝交付不会伪造“执行失败”。
5. 独立读取程序无需 claim 权限；首次快照分页期间并发发生提交也不漏读，断线后按 cursor 恢复，消费者自行去重。
6. 旧 Agent 继续运行时，依赖旧代次的新桥内写入被拒绝；独立授权和已提交回执读取分别校验，测试不声称外部进程已停止。
7. 演示任务显式采用 `safe_reopen`，过期后可重新 claim；这不证明所有外部副作用任务都适合自动重开。
8. 父 Agent 失联或 child 失败时，父状态不被桥自动改写；上层能读取 child 事实并自行决定后续业务动作。
9. 同一内容版本的 S1 被拒绝后显式重做，S2 可以被接受；已提交回执在丢响应、租约结束后可按当前读取权限恢复，不增加 Submission 或成功事件。

## 暂不进入核心

公网多租户、匿名市场、支付结算、信誉/SLA、算力计量、任意代码或插件执行、MCP/browser 执行、push、通用工作流/DAG、自动父子汇总、外部副作用 exactly-once、在线迁移、双活和 Web UI 均后置。

已依据本规范形成 [M1 协议包](../../protocol/m1/README.md)：状态/一致性表、授权表、wire contract、Schema、固定样例与黑盒向量。协议已迁入独立 Git 项目并以冻结清单管理；实现 daemon/客户端后才运行真实黑盒互操作测试。协议细化与静态检查均不代表 daemon 已实现或 M1 已通过。
