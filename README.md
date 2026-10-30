# Agent↔Task Bridge

面向不同 Agent 客户端的可移植任务桥接层。Agent 通过公共协议发布任务、pull 发现并领取、报告执行、提交产物；指定主体记录验收决定，上层从快照和事件恢复统一事实。

**v0.1.0 是 Developer Preview**，采用冻结协议 `bridge-m1-1`、HTTP/JSON-RPC 和 SQLite/WAL。首版运行边界是 Linux、单 authority、单 daemon、预注册主体、单机或受控私网。提供 Go daemon、独立 Go/TypeScript 客户端和只读消费者。Linux amd64 已执行验证；arm64 制品完成交叉编译，尚未在 arm64 主机执行验证。

[GitHub 仓库](https://github.com/Csneer/agent-task-bridge) · [发行版](https://github.com/Csneer/agent-task-bridge/releases/tag/v0.1.0) · [MIT License](LICENSE)

## 安装与最小闭环

下面使用 Linux amd64 发行包，需要 GitHub CLI、`tar`、`sha256sum` 和 Node.js 22+。在新的下载目录中获取发行附件并校验：

```sh
gh release download v0.1.0 --repo Csneer/agent-task-bridge --dir bridge-release
cd bridge-release
sha256sum --check SHA256SUMS
tar -xzf agent-task-bridge_v0.1.0_linux_amd64.tar.gz
cd agent-task-bridge_v0.1.0_linux_amd64
./bin/bridge version
```

在终端一初始化新的私有目录并启动。目标目录不能已经存在，其父目录必须存在；不要把运行数据放进 Git 仓库。

```sh
./bin/bridge init --dir "$HOME/bridge-demo"
./bin/bridge --db "$HOME/bridge-demo/bridge.sqlite" --key "$HOME/bridge-demo/bridge.key"
```

在终端二进入同一个解压目录，运行自包含示例，无需 npm 安装：

```sh
node examples/minimal/run.mjs --dir "$HOME/bridge-demo"
```

输出应包含 `state: "accepted"`、产物摘要和六个已提交的历史动作。链路是创建 → 发布 → 发现/领取 → 开始执行 → 计算真实 SHA-256 与 UTF-8 字节数 → 提交 → Owner 独立读取并校验产物 → 验收 → Reader 读取历史。每次运行创建新的 Task；示例使用同机 `file:` 产物，不执行任务提供的任意代码。完整说明见 [最小示例](examples/minimal/README.md)。

初始化生成 0700 目录与 0600 文件；`admin.json` 仅含凭据摘要，原始 bearer 分离存入 `credentials.json`。默认凭据与 Agent binding 在 **30 天后到期**，需提前以新实例 ID 更新配置。按 Ctrl-C 停止，重启使用同一数据库与密钥，无需重新导入 bootstrap。权限、轮换与恢复边界见 [运维说明](docs/operations.md)。

## 客户端与源码安装

Linux 上可使用 Go 1.27+ 从发布标签安装 daemon 和 Go CLI；需要把 Go 的安装目录加入 PATH：

```sh
go install github.com/Csneer/agent-task-bridge/cmd/bridge@v0.1.0
go install github.com/Csneer/agent-task-bridge/clients/go/cmd/bridge-client@v0.1.0
```

TypeScript SDK 通过 GitHub Release 的 npm tarball 分发，当前不承诺 npm registry 发布。在独立 Node 项目目录中：

```sh
gh release download v0.1.0 --repo Csneer/agent-task-bridge --pattern 'csneer-agent-task-bridge-0.1.0.tgz'
npm install ./csneer-agent-task-bridge-0.1.0.tgz
npx bridge-client --version
```

参阅 [Go 客户端](clients/go/README.md)、[TypeScript SDK](clients/typescript/README.md) 和 [只读消费者](examples/read-consumer/README.md)。两个 CLI 都名为 `bridge-client`；通过本地 `npx bridge-client` 使用项目安装的 TypeScript 版本。

## 核心能力与边界

- Task 是工作约定的权威；同一 Task 同时最多一个有效 Assignment。Run、Submission 和验收 Decision 分别记录执行、交付与决定。
- 原子 claim、租约续期和 fencing 限制桥内当前执行权；到期释放 Assignment 并使任务重新可领取，保留历史。桥接层不能强制停止外部旧进程或撤回副作用。
- 写命令使用幂等键与 revision；未知结果按原键原参数恢复。事件可用 cursor 重放，消费者去重，不承诺外部 exactly-once。
- 父子关系只记录事实，不自动赋权、接管、取消或汇总状态。验收校验授权并记录决定，结果内容由验收主体判断。
- 大输入和产物使用外部引用。公网多租户、匿名市场、支付/信誉、算力计量、执行沙箱、push 派单与 Web UI 不属于首版。

## 文档与验证

- [运行说明](docs/runtime.md)、[运维说明](docs/operations.md)、[安全策略](SECURITY.md)。
- [协议入口](protocol/m1/README.md)、[核心定义](docs/architecture/core.md)、[能力蓝图](docs/architecture/blueprint.md)。
- [贡献与本地验证](CONTRIBUTING.md)、[实现任务单](docs/implementation-plan.md)、[M1 验收证据](docs/validation/results/README.md)。
- [v0.1.0 发布说明](docs/releases/v0.1.0.md)、[变更记录](CHANGELOG.md)、[版本规则](docs/versioning.md)。

产品版本 `v0.1.0` 与协议版本 `bridge-m1-1` 分别管理。冻结文件中的“尚未实现/发布”等文字是基线制定时的历史状态；原始证据绑定其源码提交，不代表后续每个版本自动通过。当前发行版验证以对应 GitHub Actions 运行及附件为准，不改写历史冻结摘要。该项目独立于 p-drive，运行不依赖协作工具。
