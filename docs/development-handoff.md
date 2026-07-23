# AgentDesk 历史交接说明（已废弃）

> 本文件原先记录 2026-06-30 的整库备份与 Codex 会话归档方法。相关归档包含运行配置、客户数据或开发会话，不应进入源码仓库，已在统一集成 B13 发布安全治理中移除。

当前开发与合并只能依据：

- 真实运行代码；
- `docs/development/tenant-ai-unified-integration-plan.md`；
- `docs/development/integration-manifest.tsv`；
- `docs/design/reply-runtime-engine.md`。

不得依据本文件恢复旧 FAQ、Qdrant、旧 AIConfig、旧 Agent、旧企微字段、旧 hook bridge、旧模型授权池或旧转人工链路。

运行数据库、上传文件、部署 `.env`、密钥和 Codex 会话必须保存到仓库外的加密备份介质。Git 只保留不含业务数据与秘密的备份策略说明。
