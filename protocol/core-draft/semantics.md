# task-core-draft-1 语义与操作规范

状态：实验草案。配套 [Schema](schema.json)、[采用说明](README.md)和[场景轨迹](../../examples/protocol-scenarios/README.md)。这里的要求适用于新草案；不会修改 `bridge-m1-1` 的冻结含义。

## 1. 协议范围与参与者

本草案定义可由不同实现共同遵循的任务命令、结果、错误和事实。每个 TaskRef `{authority_id, task_id}` 指向单一权威服务内的任务。相同 task_id 在不同 authority 下是不同任务。authority 不随任务流转改变；关联外部 TaskRef 不产生迁移、跨服务事务、权限或状态联动。

ActorRef `{kind, id}` 的 kind 为 `human` 或 `agent`，id 为宿主解析的不透明标识。引用的比较区分 kind、大小写和原始 Unicode 字符，不做隐式规范化。各 authority 的宿主负责把本地或外部身份映射到明确引用；核心不会仅凭同名字符串建立跨平台信任。

参与者类型和任务角色独立。人或 Agent 都能创建、承接和执行任务；创建时的可信 actor 成为 immutable issuer。该草案只有 issuer 能修订预期、决定交付、重开或取消任务。review、审批、授权和审计可以是普通关联任务，人可以承接这些任务，但完成 review 不自动取得父任务的决定权。上层政策可驱动合法 issuer 发出决定，不能把某次提交伪装成 issuer 验收。

核心不规定认证、账号、注册、令牌、加密、密钥库、网络连通或数据库。宿主必须提供可信身份与操作准入；核心仍检查当前承接者、issuer、对象归属、版本和状态。没有宿主或适配器实际发送和处理消息，阅读规格本身不会产生任务流转。

## 2. 消息、身份上下文与采用能力

命令封套固定为：

```text
{protocol_version, command_id, task: TaskRef, expected_revision, operation, body}
```

protocol_version 必须为 `task-core-draft-1`。command_id、task_id、authority_id 与各事实 ID 都是不透明字符串。create 使用 expected_revision=0；其他新命令提供当前 Task revision。没有任意 `set_status` 操作。

角色只需发送其业务所需操作，不要求每个客户端实现整个服务器。声称完整实现本草案的权威服务需要实现下表 11 个操作及相应结果/事件规则；只实现子集时必须明确公布支持范围，遇到其他操作返回 `UNSUPPORTED_OPERATION`，不得静默忽略。版本不匹配返回 `UNSUPPORTED_VERSION`，不按字段相似性降级。本轮不规定能力发现的网络方法，也不声称已有新版本的 binding。

离线宿主上下文为：

```text
{actor: ActorRef, can_execute: boolean, can_read_history: boolean}
```

这是模型的受信输入，**不是来自客户端的授权字段**。真实宿主根据已解析身份、目标任务、具体命令或回执及当前政策计算准入。`can_execute` 允许新操作进入语义检查；`can_read_history` 允许读取该 actor 的历史成功结果。二者互不推导，true 也不能绕过 issuer/current-assignee 等核心角色约束。

持久化与消息交付由服务实现承担；一个成功结果必须代表所描述的 Task、事实、事件和回执已经作为一个不可分割的提交生效。需要跨重启提供这些保证的实现必须自己保存对应状态。离线模型仅在一个进程实例内模拟原子性与回执，不提供 durable 存储、线程并发服务或网络承诺。

## 3. 预期、执行依据与事后判断

`task.create.body` 必须有非空 `goal` 和 `expected_result`。可选字段为 delivery_requirements、constraints、inputs、context、extensions 和 relations。交付要求帮助说明希望得到什么，不是核心强制执行的固定验收算法。候选 Agent 列表、验收清单、grant 和租约不是必填项。

Task 保存 `expectation_version` 和 append-only `expectations`。每版包含完整的 goal、expected_result 和当时存在的可选预期字段，以及 version、changed_by、reason。创建生成第 1 版；`task.revise` 提交完整新预期与原因，省略可选字段表示新版本不再包含该字段。它不覆盖旧版，也不修改 issuer 或创建时的 relations。

新 Run 在 `run.start` 时明确采用**当时当前的** basis_version。随后即使 issuer 修订预期，既有 Run 的依据也保持不变；本草案中采用新依据需要结束旧承接、重新承接并启动新的 Run。`run.submit.basis_version` 必须等于 Run 的 basis，不能悄悄把旧工作标记为新要求下完成。

Submission 保存实际结果、产物引用及执行依据。issuer 看过实际结果后可接受或拒绝；即使预期已经变化，也可以接受旧依据的结果。Decision 同时保存 Submission 的 basis_version 和决定时当前的 evaluated_version，并记录实际理由。它记录的是 issuer 的判断，核心不推断内容合格、支付资格或外部工程已完成。

## 4. 操作与状态

Task 状态只有 `offered / claimed / active / submitted / completed / rejected / failed / cancelled`。completed 和 cancelled 是终态。submitted 不等于 completed；所有完成路径都必须经过 issuer 的接受决定。

下表 body 字段除特别标记外均必填，精确结构见 Schema。执行定位字段简写为 A=`assignment_id,generation`，R=`A,run_id`。

| 操作 | body | 行为与守卫 |
|---|---|---|
| `task.create` | goal、expected_result；其他预期字段和 relations 可选 | TaskRef 尚不存在，expected_revision=0；issuer=可信 actor，创建 offered、revision=1、expectation_version=1。 |
| `task.revise` | goal、expected_result、reason；其他预期字段可选 | issuer，非终态；追加预期版本，Task 状态和既有执行依据不变。 |
| `task.claim` | 空对象 | offered 且无当前承接；可信 actor 成为 assignee，generation 加一，状态 claimed。匹配与资格准入由宿主先判断。 |
| `run.start` | A、basis_version | 当前 assignee、claimed，basis 等于当前预期版本；创建唯一 Run，Task/Assignment active，Run running。 |
| `run.report` | R、message；extensions 可选 | 当前 assignee 的 active Run；追加 Report，Task 仍 active，不产生交付或完成。 |
| `run.submit` | R、basis_version、summary；artifacts、extensions 可选 | 当前 assignee 的 active Run，basis 等于该 Run；创建 Submission，Task submitted，同时结束承接。 |
| `run.fail` | R、reason | 当前 assignee 的 active Run；记录执行失败，Task failed；不制造拒绝 Decision，不自动重试。 |
| `task.decide` | submission_id、outcome、reason | issuer，Task submitted，必须指向当前 pending Submission；accepted→completed，rejected→rejected。 |
| `assignment.release` | A、reason | 当前 assignee 或 issuer，Task claimed/active；结束当前承接并回到 offered，不自动选择或启动下一参与者。 |
| `task.reopen` | reason | issuer，Task failed/rejected；回到 offered，保留全部历史；是否允许重做由宿主准入政策决定。 |
| `task.cancel` | reason | issuer，任意非终态；Task cancelled，结束当前活跃承接，不宣称外部进程或物理动作停止。 |

要求细化与业务反馈可以通过 run.report 或关联任务传达；真正改变任务预期仍需 issuer 显式 task.revise。关系不会触发父子状态变化，单任务完成也不会自动创建下一阶段。

### 事实和指针的原子联动

| 成功命令 | Assignment / Run / 新事实 | Task 当前指针 |
|---|---|---|
| claim | 新 Assignment=claimed；没有 Run | 设置 current_assignment_id |
| start | Assignment=active；新 Run=running | 设置 current_run_id |
| report | 新 Report，不改变执行状态 | 不变 |
| submit | Assignment=completed；Run=submitted；新 Submission | 清除 current_assignment_id/current_run_id；设置 pending_submission_id |
| fail | Assignment=failed、Run=failed，保存原因 | 清除 current/pending 指针 |
| release | Assignment=released；已有活跃 Run=released，保存原因；未 start 不制造 Run | 清除 current/pending 指针 |
| decide | 新 Decision；既有 Assignment/Run/Submission 不重写 | 清除 pending_submission_id |
| reopen | 保留所有旧事实；不创建新 Assignment/Run | current/pending 均为空 |
| cancel | 仅当前活跃 Assignment/Run 变为 cancelled；已 submitted/failed 的历史不改写 | 清除 current/pending 指针 |
| revise | Task 追加预期版本；旧 Run/Submission/Decision 不变 | 不变 |

一份 Assignment 至多创建一份 Run，一份 Run 至多创建一份 Submission，每份 Submission 至多有一个最终 Decision。新的尝试使用新的 Assignment、generation 和 Run，不复活旧事实。实体 ID 在 `(TaskRef, record_type)` 中唯一；不同任务中同样的 assignment_id 不代表同一个承接，不能脱离 TaskRef 使用。

## 5. 重复请求、冲突和 JSON 规则

幂等键空间为 `(authority_id, actor.kind, actor.id, command_id)`，跨该 actor 在同一 authority 的全部任务与操作。重试须保留完整原命令，包括 task、expected_revision、body；同键改参数不是新意图。

处理次序：

1. 精确解析并校验可信上下文、版本、操作与消息结构。
2. 按可信 actor 查找历史回执。存在时先要求 can_read_history；不允许则返回 NOT_ADMITTED，不能披露结果。允许且原命令等价时返回保存结果，replayed=true；不等价则 IDEMPOTENCY_CONFLICT。
3. 回执未命中才要求 can_execute，再检查存在性和当前 revision。
4. 检查操作所需角色、Assignment ID/generation、Run 归属、状态和 basis。Assignment ID/generation 不匹配优先返回 STALE_ASSIGNMENT，再核对当前 assignee；task.decide 先核对 issuer，再检查状态和 pending Submission。
5. 守卫全通过后一次性提交 Task、事实、事件和成功回执。拒绝不改变状态、不产生成功事件，也不占用成功幂等键。

历史重放不再检查旧 expected_revision、旧角色守卫或当前承接，因此完成后可以取回原提交结果；它也不会恢复旧承接或变成新的执行授权。结果包含的是历史提交时的 Task 快照，不保证仍是当前状态。未找到回执本身不能证明另一份在途请求不会成功；真实 binding 必须处理自己的在途与未知响应边界。

模型实例生命周期内保留全部成功回执与事件。草案没有 GC/过期回执或允许重用旧成功键的规则；未来持久服务不能擅自删除回执后将相同键当新命令执行。

JSON 使用 UTF-8，拒绝重复对象键、非有限数、非法 Unicode。revision、generation、version 为 0..9007199254740991 内的整数（对象的具体起点见 Schema）。业务扩展允许小数，数值绝对值不超过 `1.7976931348623157e308`，比较前保留精确十进制值。

命令等价遵循：对象键顺序无关；数组顺序相关；缺字段与 null 不同；boolean 与 number 不同；`1` 与 `1.0` 数学上相等；Unicode 字符不做规范化。不能用会把 true 当 1 的比较，也不能先做浮点舍入再比较原始输入。离线检查器 `parse_json` 使用精确 Decimal；调用 Python API 时若传入已舍入 float，模型无法恢复它最初的 JSON 数字词法，精确原始消息检查必须从 parse_json 进入。

## 6. 结果、事件与读取

成功结果为 `{protocol_version, command_id, replayed, task, records, event}`。task 是该提交的完整 Task after-image；records 是该命令改变的事实 after-images，封套为 `{record_type, record}`，类型为 Assignment、Run、Report、Submission、Decision。

返回顺序：claim=[Assignment]；start=[Assignment, Run]；submit=[Assignment, Run, Submission]；report=[Report]；decide=[Decision]；fail/release/cancel 按实际改变的 Assignment、Run 排列；create/revise/reopen 的 records 为空，Task 更新仍在 task/event.task_snapshot 中。

事件为 `{protocol_version, event_id, command_id, task:TaskRef, revision, actor, operation, task_snapshot, records, reason?}`。需要 reason 的命令将它保留在事件中，包括没有新增其他记录的 reopen，以及取消已提交任务。一个成功命令对应一个事件和一个新 Task revision；重复回执不追加事件。event_id 在 authority 内唯一且不透明，不能从它推断 Task 或跨 authority 的顺序。

消费者按 `(authority_id,event_id)` 去重，按同一 Task 的 revision 检查先后与缺口，以 `(TaskRef,record_type,record_id)` 更新事实。事件中有完整 Task 与发生变化的记录，不包含传输凭据。跨 authority 无全局序号或原子提交承诺。

Schema 的 Snapshot=`{task,records}` 表达当前 Task 和全部保留事实。离线 Model.snapshot 与 Model.events 是受信测试读取接口；没有给它们声称网络访问控制。实际查询、订阅、游标、重连、数据持久化和权限范围须由后续 binding 另行明确，不能把本草案的内存列表称为已验证的持久事件服务。

## 7. 错误

错误结构为 `{protocol_version:"task-core-draft-1", kind}`。绑定层负责把它映射到自己的响应；核心不指定 HTTP code。

| kind | 含义 |
|---|---|
| INVALID_MESSAGE | JSON/Schema 无效、非法数字/Unicode、未知核心字段或越界 |
| UNSUPPORTED_VERSION / UNSUPPORTED_OPERATION | 不支持指定版本或操作 |
| NOT_ADMITTED | 宿主未允许该次新执行或历史读取 |
| TASK_EXISTS / TASK_NOT_FOUND | 创建目标已存在或当前目标不存在 |
| STALE_REVISION | 新操作的 expected_revision 与当前 Task 不同 |
| STALE_ASSIGNMENT | 非当前承接代次、ID 或执行 Run 归属 |
| ROLE_MISMATCH | actor 不具备该 Task 的 issuer/current-assignee 角色 |
| INVALID_STATE | 当前生命周期、pending Submission 或计数容量不允许操作 |
| INVALID_BASIS | 新执行未采用当前预期版本，或提交试图改变既有 Run 依据 |
| IDEMPOTENCY_CONFLICT | 同一 actor/authority/command_id 对应不同命令 |

这些错误不代表任务实际业务失败；执行者报告不能继续执行使用 run.fail。外部执行成功但上报未知、资源不可达或旧设备仍在工作，都不能由核心自行推断为完成或未执行。

## 8. 资源、扩展和不包含的能力

ResourceRef 的 uri 是非空资源定位声明，media_type、version、digest 可选。本草案不抓取资源、不验证其可达性，也不指定 digest 算法或保证引用内容不可变；需要固定字节的上层约定应明确版本/摘要及验证方式。模型保存的是声明，不是资源服务或真实性证明。

业务数据放在点分命名空间的 extensions 下，例如 `org.example.marketplace`。扩展可以描述评分、模型、企业阶段或设备数据，但不自动产生筛选、权限、验收或状态转移。Schema 不认识的核心字段被拒绝。

当前无租约计时、自动过期、网络 binding、注册/评分、企业编排、设备发现、支付、全局任务目录、同任务权威迁移或生产持久化实现。已有 M1 可继续运行；将新语义接到真实服务，需要单独的实现、迁移与互操作验收。
