# M1 参考实现任务单

日期：2026-10-30。状态：**M1-01 至 M1-12 均为 done；最终提交对应的完整回归、真实 H01、代码审查与架构验收均已通过。**

实施入口是本仓库 [M1 协议包](../protocol/m1/README.md) 的已冻结版本及其校验值，不能使用旧草案、历史 v9 或其他项目内部接口替代。本文保留冻结后的工作包及验收标准，并随当前实施更新状态；最终证据记录于 `docs/validation/results/`。

目标是一个 Go 参考 daemon（SQLite/WAL、单 authority、预注册、私网、pull），两个独立 HTTP 客户端集成，以及一个只读消费者。首个通过条件仍是 [H01 真实交接及 V01–V25](validation/vectors.md)：B 能仅凭公开约定与授权资源完成工作，R 能恢复统一事实。Python 仅用于现有协议检查，不作为 daemon 实现语言。

完成依据：[最终验收报告](validation/results/README.md)。实现提交为 `f537f936fafa95fd74be63801fe4b5ac057ce1b4`，协议仍为 `bridge-m1-1`；Go 429 项（含子测试）、TypeScript 123 项、消费者 15 项及真实 HTTP 集成 1 项全部通过。真实 H01 使用该提交构建，B 与 X 分别由独立新上下文 Agent 实际执行。

## 1. 模块归属与协作规则

下表列出实际模块归属。HTTP、命令、查询和维护在 `internal/application` 中分文件实现，统一复用事务、授权及结束承接操作；未为计划目录形式复制领域逻辑。

| 模块 | 唯一主要责任 | 工作包 |
|---|---|---|
| `internal/domain/`、`internal/ports/`、`internal/wirevalue/` | 规范对象、错误/事件类型、精确 JSON/JCS 与时间值、事务/时钟/秘密端口 | M1-01；此后公共接口变更由集成负责人统筹 |
| `internal/store/sqlite/`、`internal/secrets/` | 存储迁移、事务、唯一约束、持久事件/回执、敏感回执加密与 key loading | M1-02 |
| `internal/auth/`、`internal/config/` | 身份/绑定/OwnerCapability、预注册导入、当前授权判定和配置审计 | M1-03 |
| `internal/application/commands.go` | 18 个写方法的统一事务编排、状态转移、回执与有序事件构建 | M1-04 |
| `internal/application/maintenance.go`、`service.go` | 权威时钟、启动补扫、周期到期/授权失效物化；复用领域结束承接操作 | M1-05 |
| `internal/application/http.go`、`cmd/bridge/` | HTTP/JSON-RPC binding、启动/关闭/ready、capabilities、错误映射 | M1-06 |
| `internal/application/queries.go` | 当前查询、授权快照与事件 replay、历史回执读取 | M1-07 |
| `clients/go/` | 独立客户端 A 的公开协议实现与演示入口 | M1-08 |
| `clients/typescript/` | 独立客户端 B 的公开协议实现与演示入口 | M1-09 |
| `examples/read-consumer/` | 只读投影、checkpoint、重建与历史审计入口 | M1-10 |
| `tests/blackbox/`、`tests/fixtures/` | RPC 黑盒、故障宿主、证据输出；不直接改写存储制造业务状态 | M1-11 |
| `examples/handoff/`、`docs/validation/results/` | 真实工作资源、两端执行说明、H01 与最终证据汇总 | M1-12 |

存储 owner 负责数据库 schema；状态 owner 负责 transition；授权 owner 负责 predicate；只有统一命令服务组合它们并提交 Task 聚合事务。不能让三路实现各自拥有一套 Task、错误优先级、幂等表或事务入口。所有最终守卫仍在同一权威事务中读取事实；接口预检不能替代它。

两个客户端可以共享已冻结 Schema 和固定 JSON 样例，不能共享 daemon 私有包、SQLite 表结构或同一套请求/重试实现后宣称独立互操作。R 只持 read 权限，通过公共 HTTP 协议读取，不调用服务内部查询函数。

## 2. 有限依赖与并行窗口

```text
冻结基线 → M1-01 共享对象/端口/解析约束
                  ├→ M1-02 SQLite/秘密/事务
                  └→ M1-03 预注册身份/授权
             M1-02 + M1-03 → M1-04 全部命令与事实事务
                                   ├→ M1-05 时钟/维护
                                   └→ M1-07 查询/快照/事件/回执
             M1-04 + M1-05 + M1-07 → M1-06 HTTP daemon 集成
                                   ├→ M1-08 Go 客户端 A
                                   ├→ M1-09 TypeScript 客户端 B
                                   └→ M1-10 只读消费者 R
             M1-06 + M1-08 + M1-09 + M1-10 → M1-11 全量黑盒反例
             M1-11 → M1-12 真实 H01 与 M1 验收汇总
```

允许提前编写绑定层、客户端的静态编解码测试及故障 fixture，但未满足依赖前不得将对应工作包标为 done。M1-02/M1-03 共享 M1-01 已确定的端口，M1-05/M1-07 复用 M1-04 的事件与结束承接语义。M1-06 需要集成这些结果才能完成。M1-08/09/10 的公开协议实现可由三名不同 owner 并行完成；最终跨组件竞争测试统一由 M1-11 收敛。

不预设人数和耗时；限制并行写入范围比把所有层同时启动更重要。依赖阻塞写入任务状态和具体缺项，不能用 mock 测试通过冒充下游真实完成。

## 3. 工作包及完成条件

### M1-01 — 共享对象、端口与协议解析（done）

- **入口：** 已冻结的 [状态](../protocol/m1/state.md)、[授权](../protocol/m1/auth.md)、[wire](../protocol/m1/wire.md)、[Schema](../protocol/m1/schema.json) 和固定样例；先记录所依据的版本及 checksum。
- **工作：** 建立 Go 模块、领域值与错误/事件类型；固定 read/write transaction、当前授权读取、时钟、token/receipt 密钥和 cursor 端口。选择并锁定 Go 工具链及 SQLite/JSON Schema/JCS 依赖，记录兼容性与维护依据，不自行决定项目许可证或 Web 框架。
- **出口：** 后续 owner 使用同一对象/端口定义；原始数值精确判定、重复键、Unicode、日期、URI、扩展、JCS 与 digest 有真实 Go 检查。错误优先级与 Schema 失败分类只定义一次。
- **验收证据：** G01–G06 的 Go 结构/解析/摘要结果与冻结样例逐项对应，尤其拒绝浮点舍入反例；`go test`、编译与静态检查结果随提交保存。现有 Python/ECMAScript 检查作为对照，不替代 Go 结果。
- **done 条件：** 类型和端口审阅通过；没有为并行方便新造未在协议中的生命周期或 RPC。

### M1-02 — SQLite/WAL 与原子事实存储（done）

- **入口：** M1-01；SQLite 驱动版本已经确定，单 authority 生命周期明确。
- **工作：** 实现 schema/migrations、Task 聚合 CAS、当前 Assignment/Run/Submission/Decision 唯一性、global batch 顺序、成功幂等记录、auth_epoch 和时钟下界持久化。实现随机 token 的验证摘要与成功回执认证加密，密钥从数据库外加载；明确锁超时和事务结束路径。
- **出口：** 同事务提交 canonical facts、公开 batch 与成功回执；回滚不留下部分记录；读事务可捕获对象视图和 W。完整存储正常重启保持 authority、回执与 cursor 所需秘密；不实现 GC、迁移权威或旧备份回滚支持。
- **验收证据：** 存储层可用临时数据库验证唯一约束和回滚；这些内部测试仅证明 adapter。V15/V17/V22 所需暂停点与重启 fixture 接口可供后续黑盒使用，最终公开证据由 M1-11 补齐。故意丢失回执解密密钥时可定位记录存在且返回不可恢复，不重执行。
- **done 条件：** 所有存储写路径通过同一事务端口；秘密不出现在事件、普通读取或日志；数据库失败不被映射为业务成功。

### M1-03 — 预注册、直接授权与不可复活绑定（done）

- **入口：** M1-01 的当前授权端口；与 M1-02 协调配置事务存储，而不自行增加第二套事务模型。
- **工作：** 定义受信本地配置格式和导入命令，支持 Principal/Agent/credential/binding/OwnerCapability。配置变更写入权威存储、管理审计和 auth_epoch；binding 替换原子撤旧建新，旧 ID 不可复活。实现 Owner/Grant 的 action、scope、期限、Agent 绑定及 receipt-read 判定。
- **出口：** 命令/查询都能在其事务中校验当前身份授权；Principal/Agent 停用立即撤销相关 binding 实例，逐 Task 物化交由 M1-05；单凭据撤销不结束同 Principal 其他有效凭据下的承接。没有公开注册 RPC，也不引入递归转授。
- **验收证据：** 独立授权、绑定替换和配置重导入测试；V04/V07/V12/V20/V21/V23/V24 的配置 fixture 与预期拒绝类型。导入路径本地管理证据脱敏，后续通过 RPC 证明没有复活旧承接。
- **done 条件：** 直接权限与 Assignment 来源区分清楚；parent_id 或旧 lease 不产生额外权限；auth_valid_until 覆盖未来生效和截止。

### M1-04 — 18 个写方法与统一事务编排（done）

- **入口：** M1-02、M1-03；共享时间端口可以使用受控测试 clock，但最终守卫必须在事务内采样。
- **工作：** 实现本文件第 4 节全部写方法。固定顺序为幂等探测、独立维护、写事务内重新查幂等/授权/CAS/lease/state、原子事实提交。实现 child 原子创建但不改父 revision、submit 原子结束承接、按 Submission 唯一 Decision、恢复 gate、显式重做与所有控制方法。
- **出口：** 每事实事务 Task revision 恰增一、generation 只随 claim 增长；每对象输出最终 after-image；batch 保存 actor/audit/action/request_id/reason。Grant 撤销若支持当前 Assignment，授权与承接结束同事务完成。命令服务提供可复用的结束承接操作给维护，不允许维护复制状态表。
- **验收证据：** 状态表每方法的允许/禁止格与错误优先级测试；V01–V07、V11–V13、V15–V21 的 service 集成测试。明确 submit/accept/reject/close/reopen 的对象 ID 和 revision 断言；不是只比 HTTP 状态码。
- **done 条件：** 18 方法都有正向、授权/状态拒绝和原键恢复覆盖；历史回执命中不重新执行已结束 lease guard；无占位业务实现。

### M1-05 — 权威时钟与租约维护（done）

- **入口：** M1-04 的结束承接与事件操作，M1-02 的时钟下界和配置失效事实。
- **工作：** 实现生产 clock、启动补扫与 1 秒维护循环；租约与 binding/grant 截止取交集。写锁等待后再采样最终时间，时钟异常按契约停止接纳相关新写；在正常无积压 fixture 内 5 秒物化。绑定被撤销时不能因重启用而恢复原 Assignment。
- **出口：** daemon ready 前补扫完成；无人发业务命令也产生 expiry/失效事实；旧 timer 对 completed 或已替换 Assignment 为 no-op。维护与客户端共享结束承接语义、事件顺序和 Task CAS，不运行匹配或业务调度。
- **验收证据：** V06/V13/V14/V16/V23/V25 的受控时间/锁顺序测试；重复扫描不重复事件；停机跨越截止后正常重启，公开查询和事件观察一次失效。测试宿主记录调度点，不靠不确定 sleep 判断先后。
- **done 条件：** 到期即时拒写与随后物化分开验证；时间倒退不复活已结束对象；不声称外部进程已经停止。

### M1-06 — HTTP/JSON-RPC daemon 集成（done）

- **入口：** M1-04/M1-05/M1-07 的可运行接口；binding 编解码可在 M1-01 后提前开发，完整出口须等待依赖。
- **工作：** 实现独立启动入口、`POST /rpc`、bearer 认证、TLS/loopback 配置、27 方法 dispatch、capabilities、请求/结果容量、确定错误封装与日志脱敏。notification 不执行且 204，batch 整体拒绝；关闭/ready/锁超时不制造虚假成功。
- **出口：** 可用临时目录启动并重启 daemon，读取相同 authority；各方法只有一个真实 handler；本地配置导入与外置密钥不通过公开 RPC 暴露。
- **验收证据：** 经真实 HTTP 检查 27 方法成功/拒绝 shape、HTTP 401/4xx/503 和 JSON-RPC 错误映射；畸形 JSON、重复键、精确数字与大小限制；公开输出/日志搜索无 bearer/lease token。编译与启动 smoke 有可复现命令。
- **done 条件：** 从全新临时存储启动到主流程提交/验收能通过公开接口完成；依赖未完成时不能把占位 capabilities 或 mocked handler 计为通过。

### M1-07 — 查询、快照、事件与回执读取（done）

- **入口：** M1-02/M1-03/M1-04 的事实、授权及 batch 格式。
- **工作：** 实现 task.get/record.get/discover、snapshot.open/page、events.open/read、receipt.get。快照在同一读视图捕获 W 和对象，分页不混入新状态；cursor 绑定主体/Agent/scope/epoch/授权时间边界，跨正常重启可续读合法 replay。普通读取不按 claim 资格过滤。
- **出口：** 首次快照后可读 W 以后完整 batch；历史起点可读 W 以前审计；空页推进与 at_head 后续读行为明确。失效/完成快照及时回收配额；授权变化明确要求重建。receipt.get 只能读取自身键空间当前有权结果，未知结果不伪称未执行。
- **验收证据：** V03/V04/V08–V10/V17/V24/V25；分页间插入 submit，完整 batch 不拆页；cursor 串主体/损坏/过期、自然授权边界、缓存重启丢失各有确定输出；公开字段不含 token，唯一例外是原 Principal 有权恢复自己的私有 claim 回执。
- **done 条件：** 独立 HTTP 读取可重建所需公开对象与控制历史；不以直接访问数据库作为消费者实现。

### M1-08 — 独立 Go 客户端 A（done）

- **入口：** M1-06 和冻结 Schema；可提前以静态样例开发，但不可导入 daemon 的 internal 包。
- **工作：** 独立实现 HTTP 调用、字段验证、幂等键管理、未知结果原请求恢复、无未知/在途副本时明确 stale 后重读、lease 监督器。支持 Owner 发布 P、A claim/start P、以独立权限 createChild C/publish C，并导出脱敏交接证据。
- **出口：** A 将目标/输入/约束/期望输出写入公共 Task；父租约维持与 child 操作分开；旧回执不能直接触发外部恢复执行。
- **验收证据：** G01–G06 在 Go 客户端运行；V03/V13/V17/V25 恢复行为经 HTTP 验证；child 发布前 B 不可领取，操作 P/C 标识明确。
- **done 条件：** 客户端可单独构建运行，仅依赖公共接口；真实 H01 证据留给 M1-12，不能因脚本请求成功提前宣称完成交接。

### M1-09 — 独立 TypeScript 客户端 B（done）

- **入口：** M1-06 和冻结 Schema；不复用 A 的请求/重试源代码。
- **工作：** 独立实现 discovery、claim/start/report/submit、lease 恢复与 artifact 引用提交；对接实际执行 Agent 或由操作者调用的工作入口。保留数值精确校验与未知 namespaced extensions，不在 JS JSON.parse 舍入后补做整数检查。
- **出口：** B 只从 C 的公开合同和有权资源取得工作输入；产物保存为可被验收主体访问的固定引用，bridge 不代抓 URI。
- **验收证据：** G01–G06 的 TypeScript 结果与 Go 对比；V03/V16/V17/V20/V25 的公开协议错误处理；构建/typecheck 与独立启动证据。
- **done 条件：** 与 A 使用不同客户端集成代码；实际工作内容和验收将在 M1-12 完成，不能用预制 submit payload 冒充 Agent 完成工作。

### M1-10 — 独立只读消费者 R（done）

- **入口：** M1-06/M1-07；R 配置仅包含 read，不授予 claim 或管理权。
- **工作：** 独立程序先装载一致快照，再按 cursor 读取完整 batch；event_id 去重、投影与 checkpoint 一致持久化；授权失效后替换活动视图。提供历史审计查询，不能把 snapshot 误当全历史控制记录。
- **出口：** 可列出 Task 与 Assignment/Run/Submission/Decision/Relation，并说明来源水位、最后 checkpoint 和重建状态；应用层自行决定父任务后续，不由消费者自动提交或汇总。
- **验收证据：** V08–V10/V24/V25；在本地投影提交前故障后重启，无先推进 checkpoint 丢失事实；完整加载与 replay 后的公开对象可与 RPC 单对象读取比对。
- **done 条件：** 无 SQLite daemon 文件访问、无 claim 权限仍可恢复 offered 和终态事实；消费成功不被服务端臆断为业务已处理。

### M1-11 — 全量黑盒与故障回归（done）

- **入口：** M1-06/M1-08/M1-09/M1-10；测试宿主的 clock/pause/crash 注入点经过确认，生产 RPC 不暴露这些控制。
- **工作：** 将 V01–V25 展开成可运行 fixture，V19 覆盖全部允许/禁止格；按明确前提固定错误优先级。覆盖同键并发、不同键 claim、renew/submit/revoke/expiry 顺序、配置停用重启用、正常完整存储重启及敏感回执密钥缺失。补充所有 profile 资源边界和 transport 负例。
- **出口：** 每条结果含 not_run/pass/fail/blocked、daemon/client commit、协议版本/checksum、fixture、脱敏请求响应、commit/event ID、调度点和证据路径。数据库内部测试只能辅助定位，最终断言由公共 RPC/事件给出。
- **验收证据：** 25 条反例与全部方法矩阵的覆盖报告；故障前后无部分 Submission/Decision/成功回执的公开证据；不存在只验证“非 200”或只比较最终 Task 状态的替代断言。
- **done 条件：** 所有必需向量 pass；blocked/not_run 不计通过，定位到环境或实现缺口后保持未完成；无关的新能力不能用来绕过原反例。

### M1-12 — 真实 H01 交接与 M1 验收汇总（done）

- **入口：** M1-11；准备一个可重复产生候选产物的真实技术说明/分析任务。A、B 与验收主体的实际执行入口和资源访问已配置，R 仅 read。
- **工作：** O 创建发布 P，A 承接并形成固定输入，独立创建发布 C；B 只依据 C 实际读取并处理输入、形成审阅产物、提交；指定验收主体实际读取产物并决定。维持 P 自身 lease，分别记录父子事实；R 从公共接口初始化、断线恢复并读取过去审计。
- **出口：** 一份包含工作约定、输入与产物 digest、B 的真实工作结果、验收理由、全部稳定对象 ID、两客户端集成来源、R 恢复证据的报告。另证明无父 lease 的合法 Owner 仍可创建 child。
- **验收证据：** H01 的 B/X 资源访问和真实工作痕迹，不只是保存 URL 或重复脚本；独立客户端的代码/构建身份；同一事实在 A/B/R 上一致可读。若缺真实 Agent/操作者执行或资源，报告精确缺项，不能伪造完成。
- **done 条件：** H01 与 V01–V25、G01–G06 的实际结果满足现行验收计划，且资料注明单机/私网、预注册、非 exactly-once 和非外部互斥边界。此时才可声明 M1 参考实现通过，不扩展为公共平台可用。

## 4. 27 个公开方法的责任映射

全部方法均经 M1-06 的同一 transport dispatch；下表指定业务主责，防止遗漏或重复实现。Grant 签发/revoke 的权限由 M1-03 提供，Task 事实事务仍由 M1-04 拥有。

| 主责 | 方法 | 必须出现的公开证据 |
|---|---|---|
| M1-04（4） | `task.create`、`task.createChild`、`task.updateDraft`、`task.publish` | draft/冻结/child 原子性及父 revision 不变；V07/V18 |
| M1-04（4） | `task.claim`、`assignment.renew`、`assignment.release`、`assignment.revoke` | 唯一承接、当前代次、截止/恢复 gate、结束原因；V05/V06/V13/V15/V16/V19 |
| M1-04（3） | `run.start`、`run.report`、`run.submit` | 每 Assignment 唯一 Run、报告独立、submit 原子结束承接；V03/V05/V06/V20/V22 |
| M1-04（5） | `task.accept`、`task.reject`、`task.reopen`、`task.cancel`、`task.close` | Decision 按 Submission 唯一、重做与终态限制、控制审计；V01/V02/V19/V21/V25 |
| M1-04（2） | `grant.issue`、`grant.revoke` | scope/期限、授权与承接原子撤销、独立权限不级联；V07/V12/V23/V24 |
| M1-06（1） | `bridge.capabilities` | 与冻结协议相同的版本、27 方法和固定 Limits；错误 authority/版本不静默降级 |
| M1-07（3） | `task.get`、`record.get`、`task.discover` | 当前授权、对象归属、普通读取与领取提示分开；V08/V20/V25 |
| M1-07（2） | `snapshot.open`、`snapshot.page` | 同一 W、多页一致、容量/授权/时间失效与回收；V09/V10/V24 |
| M1-07（2） | `events.open`、`events.read` | 历史起点、全批交付、cursor 续读与审计；V09/V10/V25 |
| M1-07（1） | `receipt.get` | 原 Principal 键空间、当前读取权、历史敏感结果保护；V03/V04/V17/V24 |

## 5. 验收记录与停止规则

每个工作包应更新为 `todo → in_progress → done`；需要外部输入或实现失败时写明 `blocked/failed` 与证据。完成记录至少包含变更 commit、协议版本、运行命令、结果、关键证据位置和剩余限制。测试结果文件放入 `docs/validation/results/`，只保存脱敏公开样例；凭据、token 和真实敏感业务材料不入库。

单元/adapter 测试用来定位模块正确性，公共 HTTP 黑盒用来证明契约行为，H01 用来证明真实交接价值，三者分别记录。已有静态检查通过不填入任何实现任务的完成列。

若实现暴露规范冲突，先附最小反例提交显式协议修订，再更新 Schema/固定样例/向量和版本依据；不得为了让测试变绿静默改守卫、扩大权限或删掉失败向量。当前计划不含公网、push、支付、信誉、执行沙箱、Web UI、递归授权、自动父子聚合、GC 或双活。
