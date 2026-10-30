# Agent↔Task Bridge v1 — 能力链路与架构蓝图

日期：2026-10-30（Asia/Shanghai）

状态：**已按第二轮审查完善能力链路与关键行为；M1 协议已形成实施基线，运行实现尚未开始。** 本文建立在 [Core Definition v1](core.md) 与 [M1 acceptance profile](../validation/acceptance.md) 上，用于说明目标与能力链路；具体实施顺序见根目录任务单。本次修订明确验收重做、幂等回执、事务内检查、授权来源、消费者初始化及父子演示；完整逐方法状态矩阵、授权表、Schema 和错误向量现已形成 [M1 协议包](../../protocol/m1/README.md)，具体版本见协议冻结记录。

## 1. v1 要解决的问题

不同 Agent 客户端通常各自拥有会话、任务格式、执行日志和结果表达。一个 Agent 想把工作交给另一个 Agent 时，容易出现四个断点：

1. 接收方拿不到完整的目标、边界、输入和验收条件。
2. 发布方无法表达“谁暂时有权推进这项工作，以及这个权力什么时候失效”。
3. 上层产品只能读取某个 Agent 的私有日志，无法得到统一的承接、执行、交付和验收事实。
4. 断线、重复请求和租约失效后，缺少查询桥内命令是否已提交、当前承接权是否有效及是否已重新承接的统一方式；外部操作没有回执时，桥也不能自行断言它没有执行。

v1 的桥只解决工作约定和事实流转，不接管外部项目、资源执行、业务编排或结算。

## 2. 目标与非目标

### 目标

- 让一个 Task 成为跨 Agent 可理解的工作约定。
- 让一个 Task 同一时刻只有一个有效承接 Assignment。
- 让 Agent 通过 pull 发现和领取工作，不依赖入站 Endpoint 或 push 通知。
- 让 Assignment 的租约、代次、失效和 fencing 可观察。
- 让一次 Run 的执行报告、Submission 的正式交付、Decision 的验收决定分别可读取。
- 让独立上层消费者只通过公开接口和事件恢复统一事实。
- 让断线、重复请求、过期租约和旧 Agent 迟到写入有确定结果。
- 通过两个独立 Agent 客户端完成一条真实任务交接链，证明桥的互操作价值。

### 非目标

v1 不做公开市场、匿名匹配、支付、信誉、SLA、算力计量、通用工作流/DAG、执行沙箱、任意插件/MCP/browser 执行、Web UI、push、双活、在线 authority migration 或外部副作用 exactly-once。

## 3. 参与者与边界

| 参与者 | v1 职责 | 不承担的职责 |
|---|---|---|
| Publisher / Owner | 创建工作约定、指定候选 Agent、验收主体和 recovery policy | 不代表外部项目系统自动完成 |
| Agent Client A/B | 发现、claim、续租、报告执行、提交产物引用；可在有授权时创建 child | 不获得 parent_id 隐含的权限；不因提交自报而获得验收结论 |
| Acceptance Principal | 对当前待验收 Submission 作出接受或拒绝决定 | 不由桥判断业务内容正确性，也不凭验收权限自动获得重开权 |
| Bridge Authority | 保存权威 Task、Assignment、Run、Submission、Decision 和事件；执行认证、授权、CAS、租约 fencing、幂等 | 不执行 Agent 代码，不停止外部进程，不替上层汇总父子任务 |
| Read Consumer | 通过查询和 replay 构建看板、审计或二次业务逻辑 | 不读 SQLite 内部表，不直接改 canonical state |
| Resource Provider | 提供输入、保存产物或执行外部副作用 | 不由桥假定其具有 exactly-once、可回滚或永久可访问性 |

身份分层为 `Principal → logical Agent → Session/Process → optional Endpoint`。pull 客户端不要求可被 bridge 反向连接。

## 4. 核心对象关系

```text
外部业务目标 / Issue
          │ explicit relation
          ▼
Task（桥内工作约定与生命周期）
  ├── Assignment（某 Agent 的有期限承接授权，最多一个有效）
  │     └── Run（一次执行尝试与观察事实）
  │            └── Submission（正式交付，引用产物/证据）
  │                    └── Decision（授权验收决定）
  ├── Relation（parent/child/related/external，关系不携带权限）
  └── Event stream（不可变事实，供 replay）

Principal / grants ──> 创建、读取、claim、执行、委托、验收等 action
Resource provider ──> input/artifact refs 的实际可访问性
```

事实含义必须保持正交：Decision 的 reject 不改写 Run 曾经执行过的事实；lease expiry 不证明外部进程停止；parent Relation 不产生接管或验收权。M1 每份 Assignment 至多有一个 Run，每个 Run 至多一个 Submission，每个 Submission 至多一个最终 Decision；Task 可以沿重做路径保留多份历史交付及各自决定。

## 5. 能力链路

### 5.1 能力总链

```text
身份建立
  → 创建工作约定
  → 冻结并发布
  → 发现与资格过滤
  → 原子领取 Assignment
  → 启动 Run / 报告进展
  → （可选）创建 child Relation + 显式授权
  → 正式提交 Submission
  → 指定主体作出 Decision
  → 接受后关闭；拒绝后关闭或按 allow_rework 显式重新开放
  → 查询 / 事件 replay / 上层消费
```

执行阶段若 release/revoke/lease expiry，则另按 `manual_reopen`/`safe_reopen` 处理承接恢复；它不代替验收拒绝后的重做策略。

每个箭头都是公开能力边界，不等于必须设计成一个独立 HTTP 方法。本文中的命令名表达语义；M1 基线已给出精确 wire 方法名及 actor、目标、授权 action、输入 revision、原子写集、事件和幂等结果，后续修改遵循版本规则。

### 5.2 主链路逐步输入与输出

| 阶段 | 调用者 | 输入 | 桥的动作 | 可观察输出 |
|---|---|---|---|---|
| 1. 身份建立 | 管理员/Principal | Principal、Agent 绑定、授权 scope、凭证 | 注册或轮换身份，建立审计上下文 | principal/agent 标识、授权版本、凭证结果 |
| 2. 创建 | Owner/Issuer | goal、constraints、input refs、expected output/acceptance、owner、recovery policy、relations | 校验工作约定、引用和初始授权；生成 Task 操作 revision | Task `draft`、Task id、内容摘要、事件 |
| 3. 发布 | Owner/Issuer | Task id、acceptance principal、可领取主体、recovery policy、allow_rework | 冻结工作约定及策略，进入 `offered` 与 `claimable` | 可发现 Task、固定内容版本、发布事件 |
| 4. 领取发现 | Agent B | workspace、显式 claim eligibility 过滤、cursor | 在可读集合上计算该 Agent 当前可领取的 Task；查询结果不预留承接权 | 工作约定摘要、门控、操作 revision、引用 |
| 5. 领取 | Agent B | Task id、expected Task 操作 revision、idempotency key | 事务内校验授权、主体资格、offered/gate 和无有效 Assignment，CAS 后创建 Assignment | Assignment id、lease expiry、generation/token、事件；此时尚无 Run |
| 6. 执行 | 当前 holder / 被授权 supervisor | Assignment 凭证；start 后使用 Run id、progress/evidence refs | start 原子创建 Run 并推进 Task/Assignment；后续报告检查当前代次和 lease | Run 事实、进展事件、可恢复结果 |
| 7. 委派 | Owner/Issuer/受授权 Agent | child 内容、parent Relation、目标 Agent、授权 scope/期限 | 分别校验创建、关系和授权 action；便利命令原子写入三者，任一失败全回滚 | draft child、Relation、grant、授权事件；随后显式 publish child |
| 8. 提交 | 当前 Assignment holder | Run id、内容版本、submission payload、artifact/evidence refs | 事务内校验并创建唯一 Submission，完成 Assignment、结束租约及当前 Run 报告 | Task/Run=`submitted`、Assignment=`completed`、Submission、事件/回执 |
| 9. 验收 | Acceptance Principal/Policy | Task 操作 revision、当前待验收 Submission id、decision、reason/evidence | 检查当前验收权限、目标绑定及每 Submission 决定唯一性；不依赖执行租约继续有效 | accepted/rejected Decision、Task 状态、事件；不改写 Run 事实 |
| 10. 恢复 | Bridge maintenance / 有权 Owner/Issuer | 当前 Assignment、server clock、release/revoke 或 reopen 命令 | 承接结束原子 fence 并按 recovery policy 设 gate；拒绝后的重做另检查 allow_rework | offered/gate、结束原因及事件；不自动启动执行，不覆盖旧交付 |
| 11. 消费 | Read Consumer | read filters、snapshot、cursor/checkpoint | 枚举全部有权读取的任务，不隐含 claim 过滤；一致快照接续事件 replay | canonical state、公开事件、续读 cursor；消费者自行去重 |

### 5.3 两个 Agent 的首个演示链

预注册 Owner 配置 A 的父任务承接权，以及独立的 child 创建/关联/授权/发布权限；配置 B 的 child 工作权限、指定验收主体和仅有 read/replay 权限的 R。A 可以代表 Owner 操作，凭证到 Principal/Agent 的绑定必须显式核对。

```text
Owner
  └─ create(P) → publish(P)：分析/形成技术变更说明，可由 A 领取

A（独立客户端）
  ├─ claim(P) → startRun(P) → 形成供审阅的候选输入
  ├─ createChild(C, parent=P)：审阅这个明确子目标
  │    明确 C 的 Owner、B 资格、验收主体、固定输入引用、safe_reopen、allow_rework
  └─ publish(C)：C 从 draft 进入 offered，B 此后才能发现并领取

B（另一种独立客户端）
  ├─ discover(C) → claim(C) → startRun(C)
  ├─ 读取 C 的授权输入 → reportProgress(C)
  └─ submit(C) → S_C：可访问的审阅产物；C 的 Assignment 同时完成

Acceptance Principal
  └─ decide(S_C)：accepted 或 rejected，明确作用于 C

Read Consumer R（没有 claim 权限）
  ├─ 只用公开接口读取 P/C 及各自的 Assignment/Run/Submission/Decision
  ├─ 通过一致快照及水位 W 初始化，再 replay W 之后的事件
  └─ 断线后续读并去重，不读 bridge 数据库

上层
  └─ 读取 C 的决定后自行决定如何推进 P；桥不自动提交或验收 P
```

必须核对每一步的 P/C、Assignment、Run、Submission 和 Relation ID，不能以 B 领取了 P 代替 C 的交接。A 领取 P 是本场景的覆盖要求，不是所有 child 创建的普遍权限前提；另验有权 Owner 在没有父 lease 时也能创建 child。定向 C 只允许 B 领取；测试“到期后换另一个 Agent”应另建允许 A/B 的任务，不能在恢复时绕过发布时冻结的主体范围。

此链路证明跨客户端交接和统一事实。公开 Task 及授权引用必须足以使 B 工作，但它不承诺模型理解质量、外部进程停止或所有业务任务都可安全重开。

### 5.4 关键状态联动

以下表固定蓝图层面的联动结果，不替代逐方法 revision、授权、错误与事件序号的完整协议矩阵。工作约定内容版本保持冻结，生命周期的操作 revision 单独变化。

| 动作 | Task / 门控 | Assignment / Run / 记录 |
|---|---|---|
| publish | draft → offered；gate=claimable | 冻结约定、恢复与重做策略；尚无 Assignment/Run |
| claim | offered 且 gate 允许 → claimed | 创建唯一 Assignment=claimed，不创建 Run |
| start | claimed → active | Assignment→active，创建唯一 Run=running |
| submit | active → submitted；指定当前待验收 Submission | Assignment→completed 并结束租约，Run→submitted，创建 Submission；同事务 |
| accept/reject | submitted → accepted/rejected | 仅当前待验收 Submission 可创建唯一 Decision；不改 Run/Submission |
| reopen（拒绝后） | rejected 且 allow_rework 且有权命令 → offered；gate=claimable | 清除当前待验收指向，保留旧决定；新 claim/start/submit 使用新对象 |
| expiry/release/revoke | 仅当前 claimed/active 承接结束时回到 offered；按策略设置 gate | 旧 Assignment 失效；已有 Run 结束桥内写入资格并记录原因，不声称外部进程停止 |
| reopen（恢复门控） | offered 且 gate=reopen_required → gate=claimable | 显式 Owner/Issuer 命令；不创建 Assignment/Run |
| close | accepted/rejected → closed | 有权命令；历史保留，不得重开 |

`claimable` 只表示恢复门控开放；最终 claim 还要检查生命周期为 offered、无有效 Assignment、当前权限和主体资格。completed Assignment 的旧定时器不能改变 submitted/accepted/rejected/closed。每份 Submission 的决定唯一性与 Task 内容版本无关，因此同一内容版本下 `S1 rejected → reopen → S2 accepted` 是合法链路。

## 6. 关键异常链路

### 旧 Agent 失联

`lease expires → 新执行写入立即失去资格 → bridge authority clock/reaper 原子记录 expiry 与 fencing → Task=offered, recovery_gate=claimable|reopen_required → event replay`。该路径只处理当前 claimed/active 的承接；迟来的旧扫描不得作用于已完成 Assignment 或新的承接代次。

`safe_reopen` 用于发布者声明可重复的候选产物，或资源侧提供充分幂等/对账保证的任务；首个演示采用前者。`manual_reopen` 需要 Owner/Issuer 显式打开资格。release/revoke 同样结束桥内承接，按同一 recovery policy 处理再次承接资格；原因分别记录。所有路径均不自动创建 Run 或宣称执行失败。

### 重复 claim / 重复 submit

恢复必须区分“读取已提交回执”和“执行新写入”：

1. 认证并在原子幂等判定下查找该请求的已提交回执；相同键但请求不等价时拒绝复用。
2. 命中等价回执时，校验当前调用者的结果读取权限，允许后返回原业务结果。无需原 lease 仍有效，不重做原命令的状态守卫，不产生新 Submission 或第二次成功事件。被撤销凭证或失去读取权限的主体不得借重放获取结果。
3. 未命中时，才按当前授权、目标 revision、generation/lease、状态及唯一性处理新写入。旧代次不能创建新进展或交付；同键并发请求也不能同时当作未执行请求提交。

例如 submit 已提交但响应丢失，Assignment 随后已 completed，原键重试仍能恢复 Submission；换成新键使用已失效 token 则不得创建交付。历史 claim 回执中的 token 不代表当前有效权限；客户端恢复外部执行前必须核对当前 Assignment，不能仅凭旧成功回执重启工作。一次查询没看到回执也不证明不存在在途请求，不能因此换键重做。回执保留、敏感 token 的可恢复保管、摘要及错误缓存分类见 wire contract。

### 外部操作结果未知

桥只能记录“某 Agent 报告过什么”和“某主体作出什么决定”。如果外部操作已经成功但回执丢失，`safe_reopen` 可能导致重复副作用；因此演示只使用可重复候选产物，通用任务必须显式选择 recovery policy 并由资源侧提供幂等/对账能力。

### 父子故障

`child expiry/rejection → child facts/events → upper layer decides parent wait/block/retry/cancel`。桥不自动改变 parent，也不因为 parent 失联删除 child；授权撤销和资源清理另行记录。测试 child 故障时保持父租约有效；测试父租约故障时保持 child 的独立授权和自身租约有效，避免把两者自身同时到期误判为状态级联。

### 验收拒绝

`S1 → Decision=rejected → allow_rework=true + 有权 task.reopen → 新 Assignment/Run → S2 → 独立 Decision`。内容版本不变，S1/D1 保留；S2 可以被 accepted，不受 D1 占用内容版本唯一键限制。M1 outcome 只有 accepted/rejected；rework 是显式重开路径，不是第三种 Decision。历史 S1 不能作为当前交付被再次决定并推进 Task；读取其原决定的幂等回执也不改变 Task。

### 承接失效与独立授权

fencing 作用于失效 Assignment 及依赖其有效性的权限，不是整个 Agent/Principal 的禁写开关。Owner/Issuer 的独立创建、撤销、重开权限，以及指定验收主体的独立验收权限，仍按其当前授权和目标状态检查。已提交 Submission 对应的 Assignment 已 completed，也不会因此阻止合法验收。

M1 选择预注册 Owner/Issuer 直接授权：Agent A 可代表获授权主体创建 C，分别检查创建、关联及向 B 授权的资格，并记录范围、期限、撤销和审计。无需为了这个场景引入任意递归 grant、depth 或父链 TTL 算法；若以后支持 Assignment 派生授权，再定义其失效传播规则。`parent_id` 不携权，A 有某项使用权也不自动意味着它可以将该权限授予 B。

## 7. 架构模式

### 7.1 推荐：六边形架构 + CQRS 读写分离 + append-only event feed

```text
                 ┌──────────────────────────────────┐
 Agent SDKs ────►│ HTTP/JSON-RPC binding adapter     │
 Read clients ──►│ authn / request decode / cursor   │
                 └────────────────┬─────────────────┘
                                  │ commands / queries
                 ┌────────────────▼─────────────────┐
                 │ Application layer                 │
                 │ command handlers / query handlers │
                 │ idempotency / authz orchestration │
                 └──────────┬───────────┬────────────┘
                            │           │ ports
                 ┌──────────▼───┐   ┌───▼─────────────┐
                 │ Domain core   │   │ Lease maintenance│
                 │ Task lifecycle│   │ clock/reaper     │
                 │ fencing/gates │   │ only facts       │
                 │ submission/   │   └─────────────────┘
                 │ decision rules│
                 └───────┬───────┘
                         │ transaction boundary
                 ┌───────▼──────────────────────────┐
                 │ Persistence adapter               │
                 │ SQLite/WAL canonical rows         │
                 │ event log + idempotency records   │
                 │ artifact/reference metadata       │
                 └──────────────┬───────────────────┘
                                │ committed events
                 ┌──────────────▼───────────────────┐
                 │ Query projection / replay reader │
                 │ authorization-filtered cursors    │
                 └──────────────────────────────────┘
```

### 7.2 各层职责

**Binding adapter** 只负责 HTTP、JSON-RPC、Bearer/profile 规则、解析限制和错误映射；不得把 transport DTO 直接当 domain object。

**Application layer** 负责身份解析、幂等恢复、命令编排、事务边界和响应组装。当前授权、lease、generation、相关 revision、状态和唯一性必须在写事务内最终检查；事务外认证/预检不能作为最终授权依据。每个 mutating command 必须明确 expected revision 指向哪个对象。

**Domain core** 负责 Task 工作约定、Assignment 唯一性、lease generation/token fencing、recovery gate、Run/Submission/Decision 事实分离、Relation 不携权和状态迁移。核心不依赖 HTTP、SQLite、某个 Agent SDK 或 LLM。

**Lease maintenance** 是运行维护端口，不是业务 scheduler。它只根据 bridge server clock 物化到期 Assignment、写 fence/expiry 事实和唤醒查询可见性；不做匹配、重试、父子聚合或支付。

**Persistence adapter** 以 SQLite/WAL 作为 M1 profile。canonical rows、事件和幂等结果在同一事务提交；事件是审计/消费 feed，M1 不宣称仅凭事件即可重建全部数据库。

**Query/replay projection** 只读 canonical state 和已提交事件，根据当前授权过滤并生成 cursor；普通读取不附加 claim 资格筛选。M1 可直接读取同一权威存储，无需另建异步读库或消息代理；这里的读写分离是接口与职责分离。projection 不能直接改变权威状态。

### 7.3 命令、查询、事件三条路径

```text
命令（参考 SQLite binding，完整守卫见 M1 状态契约）：
client → authenticate / decode → 幂等探测事务
       ├─ 命中：当前结果读取授权 → 结束事务 → 返回历史业务结果
       └─ 未命中：结束探测事务 → 对有权目标独立物化 expiry/授权失效
                  → 新写事务内重新查身份/幂等/authz
                  → revision / generation / lease / state / uniqueness 最终守卫
                  → canonical rows + events + idempotency result → COMMIT → response
       （内部物化改变 revision 时返回明确 stale，客户端重读；不静默改写 CAS）

查询：
client → authenticate → current read authz → canonical state / 一致快照
       → field filter → cursor/page → response

事件：
mutation transaction → append committed event
read consumer → snapshot(W) → replay after W → current authz/filter → at-least-once delivery
consumer → durable projection + checkpoint + event_id dedupe（消费者责任）
```

命中回执或拒绝请求后同样必须结束事务；图中分支不表示保持写锁返回。返回历史结果不重新执行该操作，也不跳过当前身份及结果读取权限检查。校验与写入使用同一事务内的权威状态和时间，维护线程共用领域规则，不能绕过它直接改表。

查询可附加“按查询时间已到期”的 derived 提示，但须与持久化状态、revision 及计算时间区分。它不是已提交的 expiry 事件，也不是保证 claim 成功的承诺；消费者恢复事实应基于 canonical 数据和事件。expiry 物化与 claim 的入口 CAS 关系见状态契约，采用先独立物化再检查新命令 CAS；内部物化不能造成每次合法重试都回滚的循环。

### 7.4 消费者初始化、重建与连续读取

首版采用一致查询快照与提交水位配对的语义：

1. R 建立覆盖其授权集合和过滤条件的快照，获得快照身份、提交事件水位 W 和首个分页 cursor。所有分页属于相同快照，不能把每页各自最新的状态拼成一个快照。
2. R 装载快照中的公开 Task、Assignment、Run、Submission、Decision 和 Relation 事实；快照与 W 必须来自同一一致读取边界。此要求不限定必须持有跨网络请求的 SQLite 长事务，具体有界快照方式在存储设计中选择。
3. 装载完成后从 W **之后** replay；分页期间产生的提交不得漏读。事件有稳定 event_id，并携带重建公开事实所需的变更或可解析引用，具体 envelope 见 wire contract。
4. R 一致保存本地投影、去重记录及 checkpoint；重复事件可重复读取，但不能重复产生本地业务效果。到达当前水位后可以继续读取后来提交的事件；过滤后空页也要给出正确续读位置。
5. 每次读取仍校验当前权限。快照/cursor 绑定的授权版本变化、快照过期、cursor 失效或保留区间不足，必须显式失败并重新初始化；R 按新的授权集合替换活动投影，不能把缺口当成“没有变化”。快照初始化期间发生这些情况应丢弃未完成快照重来。

不能采用 `query 最新状态 → 取当前最新 cursor → 从该 cursor 之后读` 作为无漏读保证：两步之间提交的事实会丢失。消费者的快照恢复保证也不等于仅凭事件能重建 daemon 全库，更不意味着桥能撤回消费者过去已获得的内容。快照容量/期限、事件保留、cursor 语义、字段授权和错误结果见协议表。

## 8. 逻辑存储边界

M1 可采用以下逻辑表或等价结构：

- `principals`, `agents`, `credentials`, `grants`
- `tasks`, `task_contracts`, `task_relations`
- `assignments`（generation、lease、recovery policy、fencing hash）
- `runs`, `run_reports`, `submissions`, `decisions`
- `artifact_refs` / `evidence_refs`（只保存引用、摘要和访问约束）
- `events`（per-stream revision + global cursor position）
- `idempotency_records`

Task contract 内容应与操作 revision 分开表达；Assignment/Run/Submission/Decision 保留稳定 ID 和不可变历史。Task 显式关联当前待验收 Submission；Decision 的唯一性按 submission_id 实施，不能使用 `(task_id, content_revision)` 排斥合法重做。generation 用于承接代次 fencing，不充当内容版本或事件位置。字段命名、索引和表是否拆分属于 persistence adapter 选择，不能成为 wire contract 的隐含要求。

## 9. v1 输出物

### 设计输出

1. [Core Definition v1](core.md)、本能力蓝图与 [M1 acceptance profile](../validation/acceptance.md)。
2. `delivery-acceptance-v1` 状态/一致性矩阵：每条命令的 actor、guard、revision、原子写集、事件顺序和幂等结果。
3. Owner/Issuer/Agent/Acceptance Principal 授权表；parent Relation 不携带权限；`manual_reopen`/`safe_reopen` 入口明确。
4. Task、Assignment、Run、Submission、Decision、Relation、ArtifactRef、Event 的最小 Schema。
5. JSON-RPC request/response/error、cursor、idempotency 和 golden vectors。
6. 两个独立客户端和一个只读消费者的黑盒互操作测试计划，包含 P/C 明确交接、重做再验收、历史回执恢复、事务竞争、无 claim 权限读取及快照接续事件。

### 参考实现输出

1. 可独立启动的 bridge daemon 与 SQLite/WAL store。
2. 两个不同客户端集成：至少一个不是同一套客户端代码的双开。
3. 一个只读 replay/query consumer；不依赖 Web UI。
4. 过期维护、重启恢复、幂等、claim race、fencing、Submission/Decision 分离的集成证据。
5. 公开示例任务和资源引用，使 B 与验收者确实能访问输入/产物。

## 10. v1 通过条件与停止条件

### 通过条件

- 两个独立 Agent 客户端完成任务交接，B 不依赖 A 私有会话。
- 一个 Task 的 Assignment 排他、过期和旧代次 fencing 可验证。
- Run、Submission、Decision 可独立查询并按事件恢复。
- safe/manual recovery gate 在到期、重开、重复请求和重启后有确定结果。
- 上层读取程序不读内部存储也能恢复统一事实。
- 发生拒绝、断线、丢响应和旧 Agent 迟到写入时，不产生错误的成功、重复 Submission 或外部执行保证。
- 同一内容版本下 S1 被拒绝、显式重做后 S2 可被接受；历史决定不能推进当前 Task。
- 无 claim 权限的读取方能看到其全部可读事实；首次初始化期间的并发提交和后续事件均不丢失。
- 父 Assignment 失效不抹除独立 Owner/验收授权；child 创建和发布有独立权限检查及可审计结果。

### 停止条件

工作顺序为：**修订并完成契约和黑盒计划 → 复核冻结 → 实现 daemon/客户端/消费者 → 执行真实互操作与故障测试**。设计阶段产出可执行的测试计划，不以尚不存在的 daemon 必须先通过测试作为开工条件。没有完成状态矩阵、授权表、最小 wire contract 和独立客户端黑盒计划前，不进入 daemon 实现。通过 JSON Schema 静态校验、同源客户端双开或读内部 SQLite，均不足以宣布 v1 完成。

上述细化产物现已列入 [M1 协议包](../../protocol/m1/README.md)：方法状态矩阵、授权、幂等与回执、snapshot/cursor、Schema、错误和固定向量。协议基线与静态检查不等于通用稳定版本或运行通过；后续按实现任务单建设参考服务及客户端。

## 11. 后续扩展方向

在 v1 通过后，才按适配器增加 A2A mapping、MCP tool sidecar、CloudEvents envelope、push/WebSocket/SSE、更多数据库、外部任务投影、公共多租户、信誉与结算。它们只能观察或通过明确 command 改变 canonical state，不能把 adapter、UI 或外部平台直接提升为 bridge authority。

## 12. 本轮修订追踪

| 审查问题 | 本文落点 | 需要验证的结果 |
|---|---|---|
| 内容版本唯一 Decision 阻塞重做 | 4、5.4、6、8 | S1 rejected 后，S2 可在同一内容版本被 accepted |
| 幂等恢复与旧 token 拒绝冲突 | 6、7.3 | 有权读取的原回执可恢复；失效代次的新写入仍被拒绝 |
| 事务外 guard 竞争窗口 | 7.2、7.3 | revoke 与旧 submit 竞争不能产生撤销后的新交付 |
| Read Consumer 被 claim 资格过滤 | 5.2、7.2、7.4 | 仅 read/replay 的 R 可枚举 offered 和终态任务 |
| 租约失效被误读为主体全部权限失效 | 6 | 独立 Owner/验收权限按自身授权继续检查 |
| 快照与 replay 之间漏事实 | 7.4 | 分页期间提交的事件可从 W 之后恢复 |
| 父子演示目标及发布不清 | 5.3 | B 只领取已发布 C；P/C 对象及授权可逐项追溯 |

本轮只修订规划文档及验收计划，不宣称实现或测试已完成。当前材料已迁入独立项目的 docs/ 与 protocol/；原 `.omx/` 仅作为保留的迁移来源。确切文件以 manifest 和 Git 提交为准。

## 13. 协议细化阶段接续

[M1 协议入口](../../protocol/m1/README.md) 汇总本阶段文件、取舍及验证边界。新增不可复活 binding 实例、完整批次审计与历史读取入口；核心目标、单 Task 单当前承接、直接授权与父子不级联保持不变。
