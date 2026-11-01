# Task Core — 人与 Agent 的任务互操作草案

版本：`task-core-draft-1`。状态：**实验规格 + 离线参考模型，已有独立应用 Task Ledger 的开发者预览；不是本仓库 v0.1.0 M1 daemon 支持的协议，也不是行业标准。**

任务由人或 Agent 发起，写明预期结果，由参与者承接和交付，最后由发起方判断是否完成。接入者使用共同的消息与行为约定，可以保留自己的语言、模型、账号、网络和存储方案。

## 最小采用约定

- 每个任务用 `{authority_id, task_id}` 标识，拥有一个权威服务；不同任务可以属于不同服务。
- `ActorRef = {kind: human|agent, id}` 表达参与者。发起、执行、评审和审计是任务角色，人的身份不自带管理权限。
- 创建必须给出 `goal` 与 `expected_result`；输入、交付要求和业务扩展可选。发起者可以显式修订要求，保留每一版。
- 同一任务同时最多一个当前承接。执行、进展、正式交付和验收决定分别记录。
- 所有正式提交进入 `submitted`；发起者接受后才成为 `completed`。协议不判断交付内容是否正确，也不要求预先固定验收标准。
- 命令幂等、精确版本冲突、旧承接拒绝和事件含义有统一规则。业务评分、阶段门禁、设备网络和跨任务汇总由上层处理。

[语义与操作规范](semantics.md)定义行为，[JSON Schema](schema.json)定义结构；两者共同构成此草案。符合 Schema 只说明消息结构正确，不能代替状态、主体和对象归属检查。

## 一个创建消息

```json
{
  "protocol_version": "task-core-draft-1",
  "command_id": "create-review-1",
  "task": {"authority_id": "team-work", "task_id": "review-42"},
  "expected_revision": 0,
  "operation": "task.create",
  "body": {
    "goal": "审阅发布方案",
    "expected_result": "交付风险判断、需要修改的事项和发布建议"
  }
}
```

这条消息不携带 bearer、密钥、租约 token 或身份注册资料。宿主根据已建立的身份与策略，另外传入可信 actor 和操作准入；模型中的 `HostContext` 是测试宿主接口，**不是允许客户端自报身份或权限的网络字段**。

随后参与者使用 `task.claim`、`run.start`、`run.report` 和 `run.submit` 推进任务。发起者收到实际交付后用 `task.decide` 接受或拒绝；需要重做时明确 `task.reopen`。人和 Agent 都使用同一组操作。与任务有关的讨论或评审结果可以通过关联任务和业务应用提供，不会自动完成原任务。

## 分层与参考实现

| 本规格定义 | 宿主、适配或业务服务提供 |
|---|---|
| 任务与参与者引用、角色语义 | 身份解析、代表关系、账号注册和权限策略 |
| 命令、结果、错误和事件 | HTTP、JSON-RPC、MQTT 等具体传输映射及服务发现 |
| 单一承接、版本和交付判断 | 持久化、连接、加密、令牌、资源访问和操作执行 |
| 关联关系与可读取事实 | 模型目录、评分、匹配、企业门禁、统计和汇总 |

SDK 和本仓库 daemon 都不是采用新草案的强制依赖；仍需某个宿主、客户端或网关实际收发消息并实现语义。本仓库提供离线模型；独立应用 [Task Ledger Demo](https://github.com/Csneer/task-ledger-demo) 已发布采用本草案的 Go 服务与 `ledger-http-draft-1` binding。该 binding 是应用项目的实现约定，尚未成为本协议的公共 HTTP profile。现有及后续 binding 都须保留核心行为，并明确自己的能力、身份、交付与恢复保证。

Task Ledger v0.1.0 固定采用本仓库 `e6dae9a` 的 Schema 和语义来源，已积累同 authority 的真实客户端、网页、安装与冷态恢复证据；真人日常验收及跨独立服务互操作仍待完成。版本来源与采用反馈见 [Task Ledger 实践记录](../../docs/adoption/task-ledger.md)。

已发布的 [M1](../m1/README.md) 继续提供带 HTTP/Bearer/TLS、预注册授权、租约和持久化恢复的参考服务。它与新草案的字段、状态和完成方式不同，没有自动转换或同版本覆盖。

## 四类场景与验证

[场景轨迹](../../examples/protocol-scenarios/README.md)分别覆盖接单平台、企业流程、设备任务和个人协作。所有轨迹使用同一消息模型，业务匹配、人工输入和设备数据是明确标注的 fixture。

仓库根目录运行：

```sh
.venv/bin/python tools/check-core-draft.py
.venv/bin/python -m unittest discover -s tests/protocol_core -p 'test_*.py' -v
```

检查器使用已有 `requirements-contract.txt` 中的 JSON Schema 验证依赖；Python 只承担规格检查。它校验结构并执行状态反例，不启动新 daemon，不验证真实人机交互、设备连接、持久化或独立服务互操作。

后续工作见[协议路线图](../../docs/protocol-roadmap.md)，分层依据见[架构说明](../../docs/architecture/protocol-first.md)。本草案尚未冻结，后续语义变化必须记录并重新验证，不能据此宣称旧 M1 实现自动兼容。
