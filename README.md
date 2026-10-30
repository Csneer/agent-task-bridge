# Agent↔Task Bridge

面向不同 Agent 客户端的可移植任务桥接层：发布工作约定，pull 发现并领取唯一当前承接权，报告执行、提交产物、记录验收，并向上层提供可恢复的统一事实。

当前交付为 **M1 协议实施基线 `bridge-m1-1`**。它不是已运行的服务，真实双客户端交接与故障验收仍待实现后执行。参考服务计划使用 Go、SQLite/WAL、HTTP/JSON-RPC，首版限定单 authority、单机/私网和预注册主体。

## 从这里开始

- [协议入口](protocol/m1/README.md)：状态/事务、授权、wire、Schema 与固定样例。
- [核心定义](docs/architecture/core.md) 与 [能力蓝图](docs/architecture/blueprint.md)：目标、边界和架构职责。
- [实现任务单](docs/implementation-plan.md)：依赖、模块责任、验收证据和阶段出口。
- [黑盒向量](docs/validation/vectors.md)：H01 跨客户端真实交接和 V01–V25 反例。
- [冻结记录](docs/validation/freeze-review.md) 与 [版本规则](docs/versioning.md)：本次证据、基线完整性和后续变更流程。

## 本地验证

静态检查需要 Python 3.10+、Node.js 22+；Python 只用于协议验证工具，不决定参考服务语言。从仓库根执行，工具不依赖 p-drive 服务、Codex、RTK、旧仓库或网络资源：

```sh
python3 -m venv .venv
.venv/bin/python -m pip install -r requirements-contract.txt
.venv/bin/python tools/verify-package.py
```

依赖安装需要包源或已有本地 wheel；安装完成后验证可离线运行。使用 RTK 的工作环境在上述命令前加 `rtk proxy`。检查器会验证冻结文件摘要、相对链接、方法映射、JSON Schema 样例、精确 JSON 解析和 Python/ECMAScript 摘要一致性。需要单独定位时运行：

```sh
.venv/bin/python protocol/m1/check-contract.py
node protocol/m1/check-hashes.mjs
```

若 Linux 发行版的 Python 缺少 ensurepip，可安装该解释器对应的 venv 包；已有 uv 的环境也可用 `uv venv .venv`、`uv pip install --python .venv/bin/python -r requirements-contract.txt` 建立同样的隔离依赖。验证器只依赖解释器和锁定的 Python 包，不要求 uv 本身。

静态通过不代表 lease/CAS/授权、SQLite 崩溃恢复或 H01 已通过；运行测试状态另列在任务单及冻结记录中。

## 项目边界

Task 是桥内工作约定的权威。同一 Task 同时最多一个有效 Assignment；这保证桥内授权排他，不保证外部旧进程或副作用停止。父子关系只记录事实，不授予权限、不汇总状态。账号市场、支付、信誉、执行沙箱、通用工作流与 Web UI 均后置。

该项目独立于 p-drive。规划来源保留于原工作区，13 个迁移文件的原始摘要见 [迁移清单](docs/provenance/migration.json)。当前本地仓库未配置发布远端；本阶段没有 daemon 或客户端源码。分发许可证和公共发布计划尚未选择。
