# 快速开始

[项目介绍](../README.md) · [运行说明](runtime.md) · [运维说明](operations.md)

本指南使用 **v0.1.0 Developer Preview**，通过一次本地任务完成创建、领取、交付、验收和历史读取。协议版本为 `bridge-m1-1`；首版面向单 authority、单 daemon、预注册主体和单机／受控私网。

## 1. 下载并校验

以下步骤适用于 Linux amd64，需要 GitHub CLI、`tar`、`sha256sum` 和 Node.js 22+。最小示例无需 npm 安装，也不需要 Go 编译环境。

在新的下载目录中获取发行附件，先校验再解压：

```sh
gh release download v0.1.0 --repo Csneer/agent-task-bridge --dir bridge-release
cd bridge-release
sha256sum --check SHA256SUMS
tar -xzf agent-task-bridge_v0.1.0_linux_amd64.tar.gz
cd agent-task-bridge_v0.1.0_linux_amd64
./bin/bridge version
```

版本输出应包含 `v0.1.0` 和 `bridge-m1-1`。所有附件均从 [v0.1.0 发行页](https://github.com/Csneer/agent-task-bridge/releases/tag/v0.1.0) 获取；上述下载命令保留全部附件，便于完整检查 `SHA256SUMS`。

Linux arm64 包名为 `agent-task-bridge_v0.1.0_linux_arm64.tar.gz`。amd64 已实际运行验证，arm64 目前仅完成交叉编译。

## 2. 初始化并启动

在终端一选择新的私有运行目录。目标目录不能已经存在，其父目录必须存在；不要把运行数据放进 Git 仓库。

```sh
./bin/bridge init --dir "$HOME/bridge-demo"
./bin/bridge --db "$HOME/bridge-demo/bridge.sqlite" --key "$HOME/bridge-demo/bridge.key"
```

默认监听 `http://127.0.0.1:8080`。初始化生成 0700 目录与 0600 文件，其中 `admin.json` 仅保存凭据摘要，原始 bearer 单独保存在 `credentials.json`。`bridge.key` 必须与数据库一起保留。

默认凭据与 Agent binding 在 **30 天后到期**。初始化输出包含到期时间，后续应提前用新实例 ID 更新配置；初始化本身不是续期命令。完整规则见 [凭据和授权更新](operations.md#凭据和授权更新)。

## 3. 完成一个任务

在终端二进入同一个解压目录，运行自包含示例：

```sh
node examples/minimal/run.mjs --dir "$HOME/bridge-demo"
```

最终 JSON 输出应包含 `state: "accepted"`、产物摘要、字节数，以及六个已提交的历史动作。示例经过以下步骤：

1. Owner 创建并发布任务。
2. 预注册 Agent 发现任务、领取并开始执行。
3. Agent 计算真实 SHA-256 与 UTF-8 字节数，写出 JSON 产物并提交。
4. Owner 独立重读产物、校验摘要，记录验收决定。
5. Reader 通过公共协议读取任务历史。

示例使用同机不可变 `file:` 产物，因此 Agent 与 Owner 需要访问同一个私有目录；它不执行任务提供的任意代码。产物文件保留在运行目录中。

每次运行都创建新的 Task。该短示例没有持久恢复 journal 或自动重试；如果中断或响应丢失，重新运行并不等于恢复原任务。需要恢复能力时使用 [Go 客户端](../clients/go/README.md) 或 [TypeScript SDK](../clients/typescript/README.md)。示例细节见 [最小 HTTP 闭环](../examples/minimal/README.md)。

## 4. 停止与重启

在终端一按 Ctrl-C，等待进程正常退出。重启时使用原数据库与原密钥，无需重新初始化或导入 bootstrap：

```sh
./bin/bridge --db "$HOME/bridge-demo/bridge.sqlite" --key "$HOME/bridge-demo/bridge.key"
```

需要另一个本地端口时，同时调整 daemon 和示例地址：

```sh
./bin/bridge --db "$HOME/bridge-demo/bridge.sqlite" --key "$HOME/bridge-demo/bridge.key" --listen 127.0.0.1:8081
```

```sh
node examples/minimal/run.mjs --dir "$HOME/bridge-demo" --url http://127.0.0.1:8081
```

最小示例只接受数字 loopback HTTP 地址。跨主机 TLS、结构化日志、配置导入、备份恢复和升级说明分别见 [运行说明](runtime.md) 与 [运维说明](operations.md)。

## 安装 Go 命令

Linux 上也可使用 Go 1.27+ 从公开标签安装 daemon 和 Go CLI。将 Go 的安装目录加入 PATH 后执行：

```sh
go install github.com/Csneer/agent-task-bridge/cmd/bridge@v0.1.0
go install github.com/Csneer/agent-task-bridge/clients/go/cmd/bridge-client@v0.1.0
bridge --version
bridge-client --version
```

`go install` 安装命令行程序；上面的最小示例文件仍需从发行包或完整源码仓库取得。使用已安装 daemon 时，将示例步骤中的 `./bin/bridge` 改为 `bridge`。Go 客户端接入与恢复规则见 [客户端说明](../clients/go/README.md)。

## 安装 TypeScript SDK

TypeScript SDK/CLI 需要 Node.js 22+，本版通过 GitHub Release 的 npm tarball 分发，尚未发布到 npm registry。在独立 Node 项目目录中执行：

```sh
gh release download v0.1.0 --repo Csneer/agent-task-bridge --pattern 'csneer-agent-task-bridge-0.1.0.tgz'
npm install ./csneer-agent-task-bridge-0.1.0.tgz
npx bridge-client --version
```

安装包包含编译后的 ESM 库、TypeScript 声明、CLI 和冻结 Schema，不需要源码工作目录。Go 与 TypeScript 的 CLI 都名为 `bridge-client`；通过项目本地的 `npx bridge-client` 使用 TypeScript 版本。

下一步可阅读 [TypeScript SDK](../clients/typescript/README.md)、[只读消费者](../examples/read-consumer/README.md) 或 [公共协议](../protocol/m1/README.md)，将自己的 Agent 或上层应用接入任务流转。
