# M1 最终验收结果

日期：2026-10-30。结论：**M1 参考实现通过最终验收**。

本次验证源码提交：`f537f936fafa95fd74be63801fe4b5ac057ce1b4`。后续文档提交只保存状态和证据。协议仍为 `bridge-m1-1`，冻结摘要 `558dc29110b0138c31a36b0f19dbb9f34677d23d5508e5c9751afc4c7cd20a13`，16 个冻结文件未更改。

| 验收层 | 实际结果 | 证据 |
|---|---|---|
| Go daemon、授权、事务、协议解析、独立 Go 客户端、HTTP 黑盒 | 429 个测试项（含子测试）通过，`go test -race ./... -count=1`；无失败/跳过替代 | [Go 原始日志](runtime/go-test.jsonl) |
| 编译与静态分析 | `go vet ./...`、`go build ./...`、独立 daemon/client 构建通过 | [验证摘要](runtime/summary.json) |
| TypeScript 独立客户端 | 123/123；typecheck 通过 | [客户端日志](runtime/typescript-test.txt) |
| 独立只读消费者 | 15/15；语法检查通过 | [消费者日志](runtime/consumer-test.txt) |
| 真实 daemon 与 TS/R 集成 | 1/1；实际 HTTP 输入读取、生命周期、并发快照、重连、fencing 和同键恢复 | [集成日志](runtime/real-http-integration.txt) |
| 冻结协议与语言符合性 | 16 文件、27 方法；92 结构/7 摘要/9 原始解析样例；Go/TS 各自验证，静态 ECMAScript 对照通过 | [冻结校验](runtime/frozen-package.txt)、Go/TS 日志 |
| V01–V25 | 25 组均有运行通过项与脱敏请求/响应、commit/event ID、调度点、版本及源码身份 | [逐向量索引](runtime/summary.json) |
| H01 真实 Agent 交接 | 最终源码构建的新任务实例；独立 B 生成真实产物，独立 X 验收 accepted，R 恢复统一事实，P active/C accepted | [最终 H01](h01-final/README.md) |
| 独立审查 | 代码审查 APPROVE；架构验收 CLEAR | [代码审查](review.md)、[架构验收](architecture.md) |

`runtime/V01.jsonl.gz` 至 `V25.jsonl.gz` 为 gzip 压缩的 UTF-8 JSONL；可用 `gzip -dc` 查看。脱敏在观察器写文件前完成，导出再次拒绝任何未脱敏的协议秘密字段。`runtime/manifest.json` 和 H01 各自 manifest 绑定证据文件字节。首轮工作区 H01 保留在 `h01/`，最终通过以 `h01-final/` 的实现提交与产物为准。

V01–V25 覆盖提交/拒绝后重做、单一承接、失联响应、原键重放、当前授权、父子独立、到期恢复、停机补扫、并发 claim/renew/revoke/submit、跨实例绑定不可复活、自然权限时间边界、快照/replay、历史审计，以及真正退出子进程的提交前/提交后崩溃恢复。V19 包含 8 个控制方法 × 9 个状态的 72 格和全部状态的 grant 管理；执行方法另有当前、结束和旧代次测试。

资源测试以实际 HTTP 创建合法业务事实：快照 10,000/10,001 记录，33,554,432/33,554,433 字节，Principal 2/authority 16 配额、300 秒到期、分页 1/100 与无效边界、16/17 initial grants、5/300 秒及合同区间、HTTP body 上限。1 MiB 结果/batch 的编码边界独立测试；大转义 Task 的原子拒绝与正常维护通过 HTTP 验证。事件缺口故障只在停机后破坏存储，随后通过公开 RPC 验证明确拒绝恢复；没有新增生产 GC。

重现命令见 [运行说明](../../runtime.md) 和 [H01 分阶段说明](../../../examples/handoff/README.md)。完整自动验证入口为 `bash tools/verify-runtime.sh <新的结果目录>`；它依赖已安装的 Go、Node/npm 和协议检查虚拟环境，不依赖协作工具。真实 H01 仍需实际 B 工作和独立 X 审阅，自动 transport fixture 不能代替这两步。

已停止最终 H01 的 supervisor、daemon 和资源服务，18781/18782 无监听。数据库、外置密钥和私密 journal 保留于 Git 忽略的本机 runtime 目录，证据包不含它们。当前没有配置远端，也未进行公共发布。

通过范围严格限定于单 authority、单 daemon、预注册、单机/私网、pull、SQLite/WAL 参考实现；任务桥保证桥内授权和状态事实。外部进程强制终止、外部副作用 exactly-once、公网多租户、支付/信誉/算力市场、执行沙箱、push、自动父子汇总、双活、旧备份回滚和 retention/GC 均不在本次验收范围。
