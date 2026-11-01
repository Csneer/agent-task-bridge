# 协议与实现入口

协议规定任务操作和事实的含义；SDK、通信适配和服务是规格的实现。接入方可以自行实现相同规格，无须安装本仓库的客户端，但仍需要宿主、适配器或网关收发消息并处理状态。

| 规格 | 状态 | 内容与采用范围 |
|---|---|---|
| [task-core-draft-1](core-draft/README.md) | 实验草案、离线模型与独立应用采用 | 人/Agent 平等参与、可修订预期、单一承接、交付后由发起方验收；独立于认证加密与传输。[Task Ledger v0.1.0](https://github.com/Csneer/task-ledger-demo/releases/tag/v0.1.0) 已提供可部署的独立实现；跨独立服务互操作仍待验证。 |
| [bridge-m1-1](m1/README.md) | 冻结，v0.1.0 已实现 | HTTP/JSON-RPC、预注册身份、租约、指定主体验收和持久化恢复的完整参考 profile。现有 daemon 与 Go/TypeScript 客户端继续遵循此版本。 |

两者有不同的字段、完成规则与身份边界，不能静默互换。新草案不会改写 M1 的规范、存储或历史证据；M1 文件中的“尚未实现”文字保留其冻结时的历史状态，当前运行证据见 [M1 验收报告](../docs/validation/results/README.md)与[发行版](https://github.com/Csneer/agent-task-bridge/releases/tag/v0.1.0)。

新方向依据四类场景建立：[接单平台、企业交付、设备 Agent、个人协作](../examples/protocol-scenarios/README.md)。每个任务保留单一权威服务，跨服务引用只建立关系，不迁移权威或产生全局事务。[架构说明](../docs/architecture/protocol-first.md)解释核心与上层分工，[路线图](../docs/protocol-roadmap.md)区分规格、离线验证和后续运行实现。

Task Ledger 固定采用本仓库 `e6dae9a6127537af94f0f5127e8d590a1e6a3f1a` 的草案，以自己的 `ledger-http-draft-1` binding 补足身份、会话、查询与持久化；其多个客户端访问同一 authority 的验证，不等于两套独立服务已经互操作。具体版本、证据与 F01–F07 反馈见 [采用记录](../docs/adoption/task-ledger.md)。
