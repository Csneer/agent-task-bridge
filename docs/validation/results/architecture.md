# M1 独立架构验收

日期：2026-10-30。源码：`f537f936fafa95fd74be63801fe4b5ac057ce1b4`。独立 architect 角色只读核对核心定义、蓝图、冻结协议、实现计划和实际源码。

结论：**APPROVE / CLEAR，限 M1 参考实现范围**。没有发现 M1-01 至 M1-12 的重大架构遗漏、协议改义或未经约定的范围扩张。运行回归与 H01 仍以各自报告为准。

核对的结构事实：

- 协议仍以 JSON Schema、方法映射、状态/授权/wire 契约定义。Go daemon 不成为语言专属协议；独立 Go/TypeScript 客户端不调用服务端私有包，R 仅走公共读取接口。
- Task、Assignment、Run、Submission、Decision 分开持久化；SQLite 唯一索引与事务守卫共同约束当前 Assignment、Run、Submission 和 Decision 的唯一性。
- application 经 ports 编排事务；存储使用 `BEGIN IMMEDIATE`，先处理已有回执和独立维护，再于最终写事务重读身份、权限、revision、lease 和状态。事实、完整事件批次及成功回执一起提交。
- child 创建原子保存 child/Relation/grants，不更改父 revision；Relation 不产生执行权，父子生命周期不自动级联。
- 维护复用同一 `endAssignment` 操作，启动补扫后才 ready；时钟异常拒绝服务，原 binding 不可通过重新启用复活。
- 快照在同一读取边界捕获 W；事件 cursor 和授权版本/时间上下文绑定；消费者按完整批次持久化投影与 checkpoint。
- daemon 负责桥内状态与权限，没有加入支付、信誉、算力交易、任意代码执行、push、自动父子汇总或 Web UI。

独立重新计算的 16 个冻结 manifest 文件的字节数和 SHA-256 均匹配。HTTP、命令、查询和维护放在 application 的不同文件，保持单一事务编排；这是包布局调整，不改变职责和协议。

范围限制仍成立：单 authority、单 daemon、SQLite/WAL、预注册、单机/私网；至少一次事件与幂等成功回执不等于外部副作用 exactly-once，桥内 fencing 不保证旧外部进程停止。旧备份恢复、跨机器双活、GC、公网多租户及平台运营不属于本次通过结论。
