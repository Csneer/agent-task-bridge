# M1 参考实现运行说明

协议为 `bridge-m1-1`。参考 daemon 使用 Go 1.27、纯 Go SQLite 驱动与 WAL；命令行运行环境目前为 Linux。客户端通过 HTTP/JSON-RPC 调用，生产运行不依赖 p-drive、Codex、RTK 或 Python。首版范围为单 authority、单 daemon、预注册主体和单机/私网。

## 构建与启动

推荐先初始化独立私有目录。发布包安装见 [快速开始](quickstart.md)，从源码构建后也可使用同一入口：

```sh
go build -o runtime/bin/bridge ./cmd/bridge
runtime/bin/bridge init --dir "$HOME/bridge-demo"
runtime/bin/bridge --db "$HOME/bridge-demo/bridge.sqlite" --key "$HOME/bridge-demo/bridge.key"
```

目标必须不存在、父目录必须存在。初始化会原子安装 authority，生成分离的摘要管理配置和原始 bearer 文件；默认 credential/binding 有效期 30 天。启动后使用 [最小示例](../examples/minimal/README.md) 完成端到端闭环。首次初始化及到期前更新规则见 [运维说明](operations.md)。

已有完整受信配置的部署也可使用底层启动参数：

```sh
runtime/bin/bridge --db runtime/bridge.sqlite --key runtime/bridge.key \
  --init-key --config /private/bootstrap.json --listen 127.0.0.1:8080
```

这里的 `/private/bootstrap.json` 是部署方自行准备的 `bridge.Config`，不是 `bridge init` 输出的同名元数据文件；后者的管理配置名为 `admin.json`。

`--init-key` 独占创建 32 字节随机密钥，已有文件时失败。密钥与配置文件要求仅所有者可读写（0600）；运行目录应为 0700。真实凭据、密钥、请求 journal 和 SQLite 文件不进入 Git。`/ready` 返回 204 才表示启动补扫完成；RPC 路径为 `/rpc`。

正常重启使用同一数据库和密钥，省略初始化参数与配置。若使用上面的 `bridge init` 路径：

```sh
runtime/bin/bridge --db "$HOME/bridge-demo/bridge.sqlite" --key "$HOME/bridge-demo/bridge.key"
```

保留外部密钥才能解密敏感历史回执和验证 cursor；丢失密钥不会让已经提交的操作重执行，敏感回执返回 `RECEIPT_UNAVAILABLE`。停机备份须保存完整最新数据库状态及外部密钥；只在此后没有任何权威写入时才能继续使用。运行时直接复制 SQLite 主文件、任意旧备份回滚和外置密钥原地轮换均不受支持，详见 [运维说明](operations.md)。事件与成功回执在 M1 不做 GC。

明文仅允许数字 loopback 地址。跨主机监听必须同时设置 `--tls-cert` 和 `--tls-key`。进程使用数据库旁的文件锁，拒绝第二个 daemon/importer 同时运行。SIGINT/SIGTERM 停止接收并等待已有 HTTP 请求，随后停止维护并关闭存储。

## 日志与版本

`bridge version` / `bridge --version` 输出产品版本、构建 commit、时间与协议版本。默认文本日志，`--log-format json` 输出结构化请求 method/status/error_kind/duration_ms、维护失败和退出累计计数。HTTP 200 仍可能是 JSON-RPC 错误，需同时看 error_kind；`/ready` 不记录请求观察。日志不包含 token、参数、任务内容或自由文本 reason。详细限制和受信嵌入 hook 见 [运维说明](operations.md#日志和观测)。

## 预注册与配置更新

受信配置是 `bridge.Config` 的 JSON 表达，包含 `authority_id`、`principals`、`agents`、`bindings`、`credentials` 和 `owner_capabilities`。字段定义见 [config.go](../internal/config/config.go)，可复现临时配置见 [H01 示例](../examples/handoff/README.md)。示例按运行生成凭据，不能作为固定共享密码使用。

导入是完整声明式更新：省略已有主体会停用，省略已有绑定/凭据/OwnerCapability 会永久撤销对应实例。绑定和凭据的身份/期限不可原地更换；重新启用使用新实例 ID。凭据可输入随机 `token`，进入数据库前转换成摘要；也可输入预计算 `token_hash`。不要在命令行参数或日志输出 token。

停机后导入事先准备的完整 0600 新配置，数据库与密钥路径必须指向同一个既有 authority：

```sh
runtime/bin/bridge --db "$HOME/bridge-demo/bridge.sqlite" --key "$HOME/bridge-demo/bridge.key" \
  --config /private/updated.json --import-only
runtime/bin/bridge --db "$HOME/bridge-demo/bridge.sqlite" --key "$HOME/bridge-demo/bridge.key"
```

成功导入持久化管理审计并推进全局授权版本，旧消费者上下文需要重建。不能在正常重启时反复导入旧 bootstrap 来恢复已经撤销的权限。导入不更改 authority 身份，不重写发布后的 Task 约定。

## 架构与恢复边界

`cmd/bridge` 负责进程、外置密钥、TLS、监听和关闭；`bridge` 暴露受信嵌入及测试入口。`internal/application` 在同一事务中编排授权、CAS、租约守卫、状态、事实、事件和成功回执。`internal/auth` 只评估当前权限，`internal/ports` 定义事务接口，`internal/store/sqlite` 实现持久化，`internal/wirevalue` 处理精确 JSON、JCS 与 Schema。HTTP、查询与维护暂放在 application 的独立文件中，避免为模块拆分复制事务或守卫逻辑。

写命令先查询历史回执，然后独立物化到期，最后在写锁内重新鉴权和检查 revision/lease。新提交的 Task revision、对象 after-image、完整事件批次和幂等回执原子持久化。claim 回执加密保存；普通对象、事件和日志不携带租约 token。SQLite 使用 `BEGIN IMMEDIATE`、WAL、`synchronous=FULL` 与 5 秒锁等待。

每秒运行租约维护，启动补扫完成后 ready。权威时钟下界持久化，小于一秒的反向抖动被钳制；较大的倒退拒绝服务为 `UNAVAILABLE`。测试的 Clock/Hook 仅在受信嵌入接口开放，不是公共 RPC。系统保证桥内唯一当前承接权，外部旧进程及副作用仍须由接入方约束。

只读消费者把完整快照安装后才接续 W 之后的事件；每批事实和 checkpoint 一起持久化。全局授权版本或时间边界变化会要求完整重建。历史控制动作使用 `events.open/read` 读取，不能从当前 Task 快照推测历史决定。

两个客户端各自实现协议解析、请求和恢复，不复用 daemon 私有包。网络超时或响应未知时保留原键原参数；单个副本被拒绝不能证明另一在途副本没有提交。历史成功回执与当前执行权分开，恢复外部执行前必须重新通过当前 fenced 命令。

## 验证命令

```sh
go test -race ./...
go vet ./...
go build ./...
npm --prefix clients/typescript run typecheck
npm --prefix clients/typescript test
npm --prefix examples/read-consumer run check
npm --prefix examples/read-consumer test
.venv/bin/python tools/verify-package.py
```

冻结文档描述的是基线制定时的状态；当前运行证据单独写入 `docs/validation/results/`，不通过改写冻结文件更新历史。测试结果、真实 H01 和独立审查各有证据后，才可声明 M1 验收完成。
