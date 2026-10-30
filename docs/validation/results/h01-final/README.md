# 最终源码构建的 H01 真实交接

结论：**PASS**。实现提交为 `f537f936fafa95fd74be63801fe4b5ac057ce1b4`。本次为最终修复后的独立新运行，数据库、凭据、Task、Assignment、Run、Submission 和 Decision 全部重新创建。

Go A 客户端创建并承接 P、凭独立授权发布 C；TypeScript B 客户端 pull/claim/start/report 并通过 HTTP 读取三个固定摘要资源。全新上下文的 B Agent 仅取得公共任务包，自行计算、撰写 review.json；全新上下文的 X verifier 重新读取原始输入和实际产物，独立核算后接受审阅。参考方案的发布建议仍是 hold，这与接受审阅交付物是两种不同决定。

最终公共状态与稳定 ID 见 [verified.json](verified.json)：P 保持 active，C 为 accepted；子 Assignment completed、Run submitted，唯一 Decision 指向唯一 Submission。父任务只有一次 claim，零 expiry。R 在提交前初始化，之后用新实例恢复，并重复读取旧 cursor 验证 event_id 去重；另读完整保留历史。当前公开对象共 30 个，本次历史读取包含 43 个批次。

证据包括输入、任务约定、真实产物与验收理由、资源 GET 摘要、公开对象、事件审计、源码/可执行文件摘要和 Go build 信息。实际生成的 bearer 与 lease token 已逐值扫描，未进入导出文件。二进制显示 `vcs.modified=true` 的原因仅为当时未跟踪的证据文档；所有受版本控制的源码与实现提交完全一致，见 provenance.json。

| 文件 | 用途 |
|---|---|
| `task.json`、三个输入文件 | B 实际收到的公共工作约定和不可变资源 |
| `review.json`、`reviewer-decision.json` | 独立 B 产物与独立 X 的具体核验理由 |
| `b-opened.json`、`b-submitted.json`、`x-inspection.json`、`x-decided.json` | 真实资源读取、正式交付与桥内验收事实 |
| `public-records.json`、`public-audit.json` | R 通过公共 HTTP 得到的对象和历史批次 |
| `resource-access.json` | 9 次资源读取及未携带 bridge 凭据的检查 |
| `authorization-checked.json` | 无父 lease 的合法 Owner 创建 child；无创建权的读/领取者被拒绝 |
| `provenance.json`、`build-info.txt` | 实现提交、客户端/消费者来源及实际构建身份 |
| `manifest.json` | 本目录导出文件的 SHA-256 与长度 |

运行结束已停止本机临时服务。产物和输入完整保存在本目录，即使示例 loopback URL 停止服务仍可复核字节摘要；重新运行遵循 `examples/handoff/README.md`。本实验不提供执行沙箱，也不证明通用外部副作用 exactly-once。
