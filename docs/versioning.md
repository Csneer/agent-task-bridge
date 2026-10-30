# 协议版本与冻结规则

当前基线是 `bridge-m1-1`，流程为 `delivery-acceptance-v1`。冻结是给首个参考实现建立明确、可追溯的输入；它不表示生产成熟、外部标准获认可或所有运行验收已经通过。

## 冻结范围

[manifest.json](../protocol/m1/manifest.json) 列出冻结规范、Schema、固定样例和验证工具的路径、字节数与 SHA-256。清单自身不包含自身摘要；Git 提交保存清单与冻结记录。清单的 canonical body 摘要供审查记录绑定确切文件集合，不用时间戳或“最新版本”文字替代。

清单格式为 `manifest_version=1`、protocol_version、profile、status、files[] 和 content_digest。files 按路径 ASCII 顺序排列，各项为 path/bytes/sha256；content_digest 是对不包含 content_digest 的整个清单按 UTF-8、ASCII 键排序、无多余空白的 JSON 编码计算的 SHA-256。该格式只用 ASCII 字符及安全整数，不涉及通用 JCS 浮点规则。

`docs/validation/design-review.md` 为迁移前的历史审查；[freeze-review.md](validation/freeze-review.md) 才是本次冻结记录。[implementation-plan.md](implementation-plan.md) 是执行任务单，进展可更新而不改变协议版本。历史结果不能被后续覆盖为已执行的产品测试。

## 兼容性与修改

1. 修改请求、响应、错误、权限、状态、时间、幂等、cursor 或可观察限制，都需要新协议版本（例如 `bridge-m1-2`），记录触发反例和兼容影响；不能保持 `bridge-m1-1` 却重新生成摘要掩盖行为变化。
2. 纯文字勘误、链接修正或验证工具修复若不改变既定语义，可以保留 wire 版本，但须单独记录勘误、审查和新基线清单；旧 Git 提交仍可精确复现。新增否定样例若暴露先前语义不明确，按语义修订处理。
3. 客户端先认证调用 `bridge.capabilities` 并要求精确 protocol_version。M1 服务实例只承载一个版本，客户端在任何业务请求前发现不匹配便停止，不猜测方法兼容或自动降级。后续在线升级需单独定义版本协商与在途请求处理，不在本基线隐式支持。
4. 请求摘要包含协议版本，版本改变必须重新生成 golden digest。已有 authority 的存储与幂等回执不能在未定义迁移的情况下换版本复用。
5. 实现发现协议不可执行时先留下具体反例，修订契约和测试向量，再调整代码；不得用实现细节倒推另一套同名语义。

## 发布边界

本次建立本地 Git 基线和实现任务单，不创建公网服务、不推送远端、不作外部 SDK 兼容承诺。后续参考实现必须报告实际支持的版本、固定限制、部署条件与运行证据。
