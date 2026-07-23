# B14 受控 Schema Cleanup

> 当前状态：工具已具备，生产执行仍为 `No-Go`。必须先修复 FastGPT HTTPS 和目标 MySQL 握手，完成丽斯文旅 / 高铁南站店的真实 B13 全链验收、正式停机、仓库外加密备份和独立恢复验证。

## 1. 作用和边界

`/app/schema-cleanup` 是唯一允许执行 B14 破坏性 DDL 的独立命令。它不被 server、AutoMigrate 或普通 DML migration 调用，也不接受任意表名、列名或索引名参数。

固定删表白名单：

- `t_ai_config`
- `t_tenant_ai_model_grant`
- `t_store_ai_model_setting`
- `t_conversation_tag`
- `t_knowledge_document`
- `t_knowledge_faq`
- `t_knowledge_chunk`

固定删列白名单：

- `t_conversation_service_session.tag_ids_json`
- `t_ai_agent.ai_config_id`
- `t_agent_run_log.ai_config_id`
- `t_ai_usage_event.ai_config_id`
- `t_skill_run_log.ai_config_id`

仅允许随对应旧列删除的历史索引：

- `idx_t_ai_agent_ai_config_id`
- `idx_t_agent_run_log_ai_config_id`
- `idx_t_ai_usage_event_ai_config_id`
- `idx_t_skill_run_log_ai_config_id`

发现额外同名列、额外索引、外键、视图或触发器引用时，命令只报告并阻断，不自动扩大白名单。

## 2. 三阶段操作

### 2.1 Inspect

`inspect` 只读输出固定目标的存在状态、待删表行数、待删列所在表行数、非空引用计数、索引和阻断引用。SQLite 必须是已经存在的持久库；命令不会创建空数据库。

```bash
docker compose --env-file "/absolute/secure/production.env" run --rm --no-deps \
  -e AGENT_DESK_BACKGROUND_WORKERS_ENABLED=false \
  --entrypoint /app/schema-cleanup agent-desk \
  --action inspect \
  --config /app/config/config.yaml \
  --pretty
```

`ready=true` 只表示 Schema 目标符合固定白名单，不代表 B13、停机、备份或恢复门禁通过。

### 2.2 Prepare

先完成以下事实：

1. 同一发布镜像的 B13 `tag_gray` 报告为 `passed`，报告明确包含迁移后重新解析的 pilot Store ID。
2. 正式 `8083` 和全部 worker 已停止，配置中 `backgroundWorkers.enabled=false`。
3. 加密备份位于仓库外、权限受限且已有预记录 SHA-256。
4. 备份已经恢复到不同数据库端点；源库和恢复库的 Schema、数据及 migration 指纹完全一致。
5. 当前源库仍与恢复报告中的备份源快照完全一致。

安全目录父级必须为 `0700`，报告和备份必须为 `0600`。`operation-dir` 必须位于 Git 仓库外且尚不存在。

```bash
SECURE_ROOT="/absolute/secure/b14-window"

docker compose --env-file "/absolute/secure/production.env" run --rm --no-deps \
  -e AGENT_DESK_BACKGROUND_WORKERS_ENABLED=false \
  -v "${SECURE_ROOT}:/secure" \
  --entrypoint /app/schema-cleanup agent-desk \
  --action prepare \
  --config /app/config/config.yaml \
  --environment production \
  --repository-root /app \
  --operation-dir /secure/operation \
  --release-report /secure/release-tag-gray.json \
  --restore-report /secure/restore-verification.json \
  --backup-artifact /secure/backup.age \
  --backup-sha256 "<pre-recorded-sha256>" \
  --pilot-tenant-name "丽斯文旅" \
  --pilot-store-name "高铁南站店" \
  --shutdown-confirmation STOPPED_8083_AND_ALL_WORKERS \
  --pretty
```

命令依据 Tenant 和 Store 业务身份查询最终 ID，不使用来源 Store ID `3`，也不默认 `301`。准备成功后会在安全目录生成：

- `plan.json`：HMAC 绑定的数据库快照、证据文件和固定清理清单；
- `operation.token`：`0600` 一次性随机令牌；
- 终端输出的 `requiredConfirmation`：只含环境和随机操作 ID，不含令牌或秘密。

默认报告最大年龄为 2 小时，计划有效期为 30 分钟。可缩短，但报告年龄不得超过 24 小时、计划不得超过 2 小时。

### 2.3 Execute

由两人复核 `inspect` 和 `prepare` 输出后，把上一步原样输出的 `requiredConfirmation` 传入：

```bash
docker compose --env-file "/absolute/secure/production.env" run --rm --no-deps \
  -e AGENT_DESK_BACKGROUND_WORKERS_ENABLED=false \
  -v "${SECURE_ROOT}:/secure" \
  --entrypoint /app/schema-cleanup agent-desk \
  --action execute \
  --config /app/config/config.yaml \
  --environment production \
  --operation-dir /secure/operation \
  --shutdown-confirmation STOPPED_8083_AND_ALL_WORKERS \
  --confirm "<requiredConfirmation>" \
  --pretty
```

执行前命令会再次验证：

- 生产配置仍指向 `8083` 且 worker 关闭；
- 计划未过期、HMAC 和令牌一致、令牌未使用；
- B13 报告、恢复报告和备份文件未变化；
- 当前数据库全量快照与 prepare 及恢复源快照一致；
- pilot Tenant/Store 身份和最终 ID 未变化；
- 实时 Tenant 完整性及 `tag_gray` readiness 仍通过；
- 固定 Schema 清单和引用盘点未变化。

全部通过后才会先写入不可重放的 `consumed.json`、擦除令牌内容，再执行 DDL。成功或失败都会尝试写入 `result.json`。令牌一旦消费，不允许重放。

## 3. 失败处理

- 令牌消费前失败：数据库未修改；修复证据或状态后重新 `prepare`，不得修改原计划文件。
- 令牌消费后失败：可能已发生部分 MySQL 自动提交 DDL。立即保持停机，按已验证备份恢复整库，再重新完成 B13、备份和独立恢复，不得直接重试。
- 不允许通过新增 CLI 参数、手工改 `plan.json`、删除 `consumed.json` 或恢复任一旧表绕过门禁。

## 4. 执行后验收

1. 再次运行 `inspect`，要求 `ready=true` 且 `changeCount=0`。
2. 使用同一镜像重跑 Tenant 完整性和 pilot `tag_gray` readiness。
3. 验证七张旧表和五个旧列在 SQLite/MySQL 均不存在。
4. 验证 migration 指纹、Tenant/Store 隔离、AI 回复、FastGPT、转人工、规则派单、标签和账单链路。
5. 只用同一发布镜像重启正式 `8083`。

B14 后不支持恢复旧应用或单独恢复旧表。严重问题只能恢复 cleanup 前整库备份并整体回退发布。
