# Task Ledger：任务核心的独立应用实践

日期：2026-11-01。Task Ledger Demo / Task Orbit 已发布 **[v0.1.0 Developer Preview](https://github.com/Csneer/task-ledger-demo/releases/tag/v0.1.0)**，以独立 Go 服务采用 `task-core-draft-1`，提供 SQLite 持久化、HTTP binding、Python Skill 和网页工作区。这为草案增加了真实应用和客户端证据；本仓库的新核心参考模型仍是离线模型。

本记录汇总采用范围与反馈，不宣称完整协议符合性、跨独立服务互操作或真人日常验收已经完成。Task Ledger 与本仓库已发布的 M1 daemon 是不同实现，不能将两者的版本、客户端或验收结果互换。

## 固定版本与来源

| 项目 | 本次记录对应的版本 |
|---|---|
| Task Ledger 应用 | `v0.1.0` / [`5170849fc2914f5484e64e03f49cb40cc89816ec`](https://github.com/Csneer/task-ledger-demo/commit/5170849fc2914f5484e64e03f49cb40cc89816ec) |
| 采用的任务核心 | `task-core-draft-1` / Bridge [`e6dae9a6127537af94f0f5127e8d590a1e6a3f1a`](https://github.com/Csneer/agent-task-bridge/commit/e6dae9a6127537af94f0f5127e8d590a1e6a3f1a) |
| 语义与结构来源 | [固定语义规范](https://github.com/Csneer/agent-task-bridge/blob/e6dae9a6127537af94f0f5127e8d590a1e6a3f1a/protocol/core-draft/semantics.md)、[采用来源清单](https://github.com/Csneer/task-ledger-demo/blob/5170849fc2914f5484e64e03f49cb40cc89816ec/protocol/upstream.json)、[结构文件校验和](https://github.com/Csneer/task-ledger-demo/blob/5170849fc2914f5484e64e03f49cb40cc89816ec/protocol/upstream/SHA256SUMS) |
| 应用 HTTP binding | [`ledger-http-draft-1`](https://github.com/Csneer/task-ledger-demo/blob/5170849fc2914f5484e64e03f49cb40cc89816ec/docs/binding.md)，由 Task Ledger 单独定义 |
| 本仓库既有参考服务 | `v0.1.0` / `bridge-m1-1`；其冻结规范、存储和客户端保持原版本 |

Task Ledger 逐字节复制上游 Schema，并通过固定提交引用语义规范；Schema 的 SHA-256 为 `844ee0aaa3b1c1217fd649de39291bb4ca7b7408269a9da07c1ec99840243a95`。其来源不会随本仓库 `main` 自动更新。

本轮同步的是采用事实和反馈，**没有变更核心语义、Schema、M1 冻结文件或摘要，也不要求 Task Ledger 升级 pin**。未来若改变行为，应按明确的兼容影响和回归证据进行版本审查，而不是重算摘要覆盖既有输入。

## 核心与宿主的实际分工

| 层次 | Task Ledger 中的采用方式 |
|---|---|
| 任务核心 | 人和 Agent 均可发起和承接；沿用 11 个核心操作、单一当前承接、显式预期修订、交付与不可变 issuer 的决定；任务关系不授予权限或级联完成 |
| 宿主与 binding | 提供 Bearer 身份映射、项目和参与者目录、定向领取资格、ActivitySession 与证明、HTTP 错误映射、快照和签名 cursor；这些是应用约定，不是上游核心已有的网络能力 |
| 持久化与恢复 | Go 状态机与 SQLite/WAL 事务保存事实、事件和成功回执；完整应用请求参与幂等，Skill 持久保存原始请求意图；正常重启与最新停机状态迁移分别验证 |
| 产品与执行 | Task Orbit 展示任务链、历史、重做和验收；Agent 从现有助手会话运行后主动领取，页面不自动启动进程；handoff、自动 launcher 和跨 authority 协作未实现 |

完整映射见 [Task Ledger 协议采用说明](https://github.com/Csneer/task-ledger-demo/blob/5170849fc2914f5484e64e03f49cb40cc89816ec/protocol/README.md)。同一 Actor 的多个进程仍需宿主处理执行归属，服务内部的单一承接不等于外部副作用只会发生一次。

## 已有证据及其边界

| 证据 | 已核对结果 | 不覆盖的结论 |
|---|---|---|
| [Go 核心与事务](https://github.com/Csneer/task-ledger-demo/blob/5170849fc2914f5484e64e03f49cb40cc89816ec/docs/validation/s2-results.md) | 独立 Go 实现、原子事实/事件/回执、冲突与恢复测试；固定 Schema 和结构向量检查 | 结构检查不等于完整行为符合性，内部测试不代替客户端互操作 |
| [四个真实 Agent 客户端](https://github.com/Csneer/task-ledger-demo/blob/5170849fc2914f5484e64e03f49cb40cc89816ec/docs/validation/s3-agent-acceptance.md) | 本机 Codex、Pi、Claude Code 与远端 Codex 通过公开 HTTP 完成同一 authority 上的委派、交付、issuer 验收与重启接续；远端使用加密 SSH 转发 | 多个客户端访问一个服务，不是两个独立 authority 实现互操作；自动化 `human` 角色不是实际人的 review |
| [浏览器与发行准备](https://github.com/Csneer/task-ledger-demo/blob/5170849fc2914f5484e64e03f49cb40cc89816ec/docs/validation/release-prep-20261101.md) | 17 组浏览器流转、13 组可用性检查、amd64 解包安装、最新完整停机状态迁移、历史回执/cursor 接续与持久部署核对 | 不证明任意旧备份回滚、磁盘损坏恢复、arm64 实机运行或真人日常验收 |
| [有界负载摘要](https://github.com/Csneer/task-ledger-demo/blob/5170849fc2914f5484e64e03f49cb40cc89816ec/docs/validation/release-prep-20261101/capacity-results.json) | 单次 60 秒、4 个有节奏的读客户端，另含 20 次创建及同键重放和一次领取竞争；快照查询 1,428 次，p95 86.27 ms，无非预期错误 | 仅绑定报告中的运行代码、硬件和配置，不是协议性能承诺、饱和容量、长期稳定性或 retention 结论 |
| [最终 tag CI 与发布](https://github.com/Csneer/task-ledger-demo/actions/runs/41281793487) | `5170849` 对应的测试、浏览器、打包与安装/恢复检查通过，发布同一次运行生成的 Linux 包及校验清单 | 发布授权不替代产品验收；arm64 仍仅交叉编译 |

报告各自绑定其记录的代码和环境，不将较早客户端或负载实验的二进制摘要改写为最终包摘要。发布准备报告中的“尚未发布”“尚未触发托管 CI”是当时状态；最终发行事实由上表的 tag、提交与 CI 单独补充。完整真人使用应继续按 [Task Ledger 验收清单](https://github.com/Csneer/task-ledger-demo/blob/5170849fc2914f5484e64e03f49cb40cc89816ec/docs/human-acceptance.md) 记录。

## 回馈协议的议题

以下沿用下游 [F01–F07 设计发现](https://github.com/Csneer/task-ledger-demo/blob/5170849fc2914f5484e64e03f49cb40cc89816ec/docs/protocol-findings.md) 编号。设计发现、已实现的宿主处理和已执行的验证分别保留；这些条目不是已确认的核心协议 bug 或已采纳的新规范。

| 编号 | 当前处理与证据状态 | 上游后续方向 |
|---|---|---|
| F01 · issuer 与验收责任 | 保留不可变 issuer；需要人验收的工作由人发起。真实 HTTP 已拒绝父任务 `human` 主体代验收 Agent 发起的子任务（403） | 收集发起者永久离线、代表决定等实际需求；若提出扩展，须定义授权、撤销和审计，不隐式移交 issuer |
| F02 · 同 Actor 多进程 | 宿主实现 ActivitySession、证明与 Assignment 绑定；会话证明检查已有 HTTP 反例。复制全部凭据与证明后的同主体进程不获得隔离保证 | 评估执行实例或 fencing 的可互操作扩展；先补明确威胁模型与恢复竞争证据 |
| F03 · 网络读取与接续 | `ledger-http-draft-1` 已实现 HTTP、快照、水位与签名 cursor，并有客户端、重启、跨 Actor 拒绝和篡改反例 | 可作为公共 HTTP/读取 profile 的输入；仍需另一独立服务实现验证，不能直接将 Ledger binding 宣布为核心标准 |
| F04 · 应用封套与原子性 | 已实现的项目关联、ClaimPolicy 与 Task 创建处于应用事务边界，完整封套参与幂等；含 wake 的创建请求被拒绝，不声称已实现 wake outbox | 提炼核心命令与应用字段的组合、等价性及失败规则；外部进程启动不能被数据库事务自动保证 |
| F05 · 会话与部分成功恢复 | 会话与 core Run 分离，Skill 在发送前保存原请求与 ID；正常重启、未知写和发行安装链路已有分层验证 | 优先形成客户端采用与恢复指南，保留各步骤回执；会话结束不能自动完成、释放或失败任务 |
| F06 · 注册与唤起 | 参与者目录已有实现；自动 launcher、wake/dispatch 执行器未实现。注册资格、启动事实和领取仍是不同概念 | 后续若引入可选激活 profile，单独验证参数隔离、配置撤销、未知启动结果与副作用对账 |
| F07 · 历史增长与保留 | 当前保留成功回执和事件，设置明确响应/快照边界；已有短时负载与冷态恢复证据，未证明长期容量或历史清理方案 | 先测长期增长、落后消费者与恢复成本，再讨论 retention；不能删掉幂等历史后允许旧请求重新执行 |

下游修复的长组合键标识、SQLite 初始化 BUSY、查询 BLOB 预算和私有文件权限属于实现与资源管理问题，见 [S3 实现修正记录](https://github.com/Csneer/task-ledger-demo/blob/5170849fc2914f5484e64e03f49cb40cc89816ec/docs/validation/s3-agent-acceptance.md#已发现并修复的实现问题)。它们未引出本轮核心语义修订。

下一步应以真实试用反馈补齐上述证据，再决定哪些经验形成采用指南、binding/profile 提案或核心修订。跨独立服务互操作仍按 [协议路线图](../protocol-roadmap.md) 的出口单独验证。
