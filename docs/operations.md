# v0.1.0 运维边界

本指南适用于 Linux、单 authority、单 daemon、SQLite/WAL 和预注册主体。先完成 [最小示例](../examples/minimal/README.md)。不要把预览版部署为匿名公网接单服务，也不要对同一个 authority 做双活。

## 初始化、启动与停止

`bridge init --dir NEW_DIR` 要求目标不存在、父目录存在。成功后目录为 0700、文件为 0600，生成数据库、外置密钥、管理配置、独立 bearer 文件和 bootstrap 元数据。失败时保留并报告私有 staging 目录供检查；不要在不确定是否已经安装成功时反复覆盖初始化。

默认凭据与 Agent binding 有效期 30 天；可用 `--valid-for` 设置大于零且不超过 `8760h` 的期限。初始化只创建新 authority，不负责后续续期。

启动命令：

```sh
bridge --db "$HOME/bridge-demo/bridge.sqlite" --key "$HOME/bridge-demo/bridge.key"
```

`GET /ready` 返回 204 表示启动补扫完成；RPC 为 `/rpc`。只有数字 loopback 监听允许明文 HTTP。跨主机配置成对的 `--tls-cert` / `--tls-key`，客户端仍须校验证书。数据库旁的进程锁拒绝第二个 daemon/importer。

Ctrl-C、SIGINT 或 SIGTERM 触发停止接收请求，并在关闭存储前等待已有请求，HTTP 关闭等待上限为 10 秒。确认进程已退出再执行配置导入、拷贝或迁移。正常重启使用原数据库与原密钥，不重复导入旧配置。

## 凭据和授权更新

`admin.json` 是完整声明式配置，初始化时只保存 token_hash；`credentials.json` 单独保存 bearer。真实接入只向每个参与者交付它自己的凭据，保护传输和存储。

在到期前准备完整的新管理配置：

1. 保留仍需启用的 Principal、Agent、binding、credential 与 OwnerCapability。
2. 新 bearer 使用密码学随机值，以新的 credential ID 和有效期加入；需要更新 Agent 绑定期限时使用新的 binding ID。既有实例的身份与期限不能原地修改。
3. 省略的主体会停用；省略的 binding、credential 或 OwnerCapability 会永久撤销该实例。父子关系不授予任何额外权限。
4. 停止 daemon，使用受保护的更新配置导入，再正常启动：

```sh
bridge --db "$HOME/bridge-demo/bridge.sqlite" --key "$HOME/bridge-demo/bridge.key" \
  --config /private/updated-admin.json --import-only
bridge --db "$HOME/bridge-demo/bridge.sqlite" --key "$HOME/bridge-demo/bridge.key"
```

`/private/updated-admin.json` 是部署方事先准备的完整 0600 文件，不能直接把示例路径作为现成配置使用。更新最小示例的 credentials/bootstrap 文件以匹配新凭据和时间。成功导入记录管理审计、推进全局授权版本，读取方需要按协议重建上下文。不要通过导入旧 bootstrap 恢复已撤销授权。

客户端 journal 绑定身份与 authority。轮换同一 Principal 的 bearer 时，显式保留和使用原 journal；默认 journal 路径可能因凭据指纹变化而改变。恢复未知命令继续使用原键与原参数，不能换键重复执行。

## 数据、备份与恢复

`bridge.key` 是外部持久密钥，保护敏感历史回执并参与 cursor 验证。v0.1.0 不支持原地轮换这个密钥；不要用重新生成的 key 替换它。密钥丢失时历史敏感回执可能返回 `RECEIPT_UNAVAILABLE`，不代表原操作未提交。数据库整体并未因此加密。

停机备份步骤：停止 daemon 和所有 importer，确认没有并行写入者；保存完整的最新运行目录，包括数据库、任何仍存在的 WAL/SHM 文件、外部密钥和管理资料。客户端 journal、输入/输出资源需要分别保存。备份按秘密资料保护，避免把它放进源码仓库。不要在 daemon 运行时直接复制 SQLite 主文件，也不要手工删除 WAL 以“修复”数据库。

**只允许使用完整的最新停机状态继续运行，且该状态之后不能发生任何权威写入。** 例如停机后把整套最新状态移到新主机，并确保旧实例永不同时运行。恢复还必须保留原密钥、相容存储格式及正确时钟。

任意旧备份回滚不受支持：它会丢失已对外承诺的 revision、lease 代次、事件和幂等回执，可能让已经发生的外部操作重新执行。普通备份不构成在线灾难恢复、双活或时间点回滚方案。遇到数据库损坏、密钥丢失或最新权威状态不明时，停止接单并保留证据，不猜测成功/失败、不清空回执、不复用旧 authority 状态。

事件、成功回执和历史在 M1 不做 GC。监控磁盘容量并保留写入余量；容量限制触发明确失败，不能依赖自动清理。主机时钟显著倒退会拒绝服务为 `UNAVAILABLE`，需排查时钟后再恢复，不能通过修改数据库时钟下界绕过检查。

## 日志和观测

默认文本日志；使用 `--log-format json` 供日志采集器解析。请求完成日志包含固定 RPC 方法名、HTTP status、协议 error_kind 与 duration_ms；未知方法只记录 `unknown`，不记录原文。JSON-RPC 错误可能返回 HTTP 200，必须同时看 error_kind。

`/ready` 探测不进入请求日志与计数。维护失败记录固定错误类别，正常每秒维护不刷屏。正常退出时输出进程累计请求/错误和维护/失败计数；重启后计数从零开始，不是持久化审计。日志不含 bearer、lease token、请求参数、任务内容、原始请求 ID 或自由文本 reason。

受信嵌入接口可用 `bridge.Options.ObserveHTTP` / `ObserveMaintenance` 和 `Service.Stats()`。回调在事务和锁释放后调用、隔离 panic，但同步执行，调用方需保证并发安全且及时返回；Stats 各字段原子读取，不保证所有字段来自同一瞬间。没有新增公共 metrics RPC 或网络端点。

## 升级

先阅读目标发布说明，检查产品版本、精确协议版本和存储兼容承诺；停机并保存完整最新状态后再更换程序。v0.1.0 没有通用在线迁移或降级承诺，不能仅凭相同文件名推断数据库兼容。客户端连接时要求精确 `bridge-m1-1`，不自动猜测兼容或降级。

若升级后发生了任何写入，禁止用升级前备份直接回滚接单；是否可继续必须由明确的迁移/恢复方案决定。外部执行器恢复工作前必须重新通过当前 fenced 操作，历史 claim 成功回执本身不提供当前执行权。
