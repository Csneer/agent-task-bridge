# 贡献与本地验证

开发环境：Linux、Go 1.27+、Node.js 22+、npm、Python 3.10+。Python 只用于协议验证，daemon 运行不依赖 Python、p-drive、Codex 或 RTK。先阅读 [AGENTS.md](AGENTS.md)、[协议入口](protocol/m1/README.md) 和与改动有关的契约。

## 准备依赖

在完整仓库根目录执行：

```sh
python3 -m venv .venv
.venv/bin/python -m pip install -r requirements-contract.txt
npm ci --prefix clients/typescript
npm ci --prefix examples/read-consumer
npm --prefix clients/typescript run build
```

安装需要包源或本地缓存。若解释器缺少 ensurepip，请安装发行版对应的 Python venv 支持包。TypeScript 构建依赖仓库中的冻结协议，不能只复制 SDK 源码目录构建。

## 验证

使用新的结果目录运行完整检查；已有向量证据不会被覆盖：

```sh
bash tools/verify-runtime.sh runtime/validation-local-1
npm --prefix clients/typescript run test:package
```

前者运行 Go race/vet/build、TypeScript、只读消费者、真实 HTTP 集成和冻结协议检查，结果保存在指定目录。Node 必须可用，避免最小 HTTP 示例被跳过。后者在仓库外临时目录执行 npm pack/install、导入、CLI 和 TypeScript 消费者编译。真实 H01 的独立 Agent 工作与验收仍按 [场景说明](examples/handoff/README.md) 执行，不能以 transport fixture 代替。

只检查冻结包或复现发布制品时：

```sh
.venv/bin/python tools/verify-package.py
bash tools/build-release.sh v0.1.0 dist/release-local
bash tools/smoke-release.sh dist/release-local v0.1.0
```

制品目录须为空；版本必须与 SDK package.json 一致。构建生成 Linux amd64/arm64 daemon 与 Go CLI、协议包、SDK tarball、依赖清单及 SHA256SUMS。smoke 在 Linux amd64 执行解压后的程序并独立安装 SDK；arm64 仅交叉编译，不代表运行通过。Go module graph 清单去除本机路径，包含 build/test-only 依赖；二进制包附带 `THIRD_PARTY_LICENSES/`。依赖清单不是 SBOM。CI 的标签发布 job 仅在同次完整验证成功后上传这些制品，0.x 标记为 prerelease。

## 提交约定

提交应描述可观察行为和验证结果。新增或调整状态、授权、CAS、租约、幂等、快照或事件行为时，提供能暴露问题的公共协议测试。保留 Task/Assignment/Run/Submission/Decision 边界，不从父子关系推导权限或状态汇总。

`protocol/m1/manifest.json` 中的冻结文件不可静默改写。协议变更按 [版本规则](docs/versioning.md) 建立新基线；不能重算同版本摘要掩盖差异。历史验证证据保持原源码身份，新验证写新位置。

请勿提交真实 bearer、lease token、数据库、私有 journal、个人会话或 `.env`。一般缺陷可提交 GitHub issue；敏感问题按 [安全策略](SECURITY.md) 私密报告。
