# AgentDesk 迁移备份

本目录保存可随仓库迁移的项目运行备份，用于换电脑后恢复到当前机器上的同一状态。

## 当前备份

- `migration-20260630-090044/`

包含内容：

- `config/agent-desk.yaml`：当前 Docker 运行配置，含数据库、企业微信、存储、向量库等运行参数。
- `config/config.yaml`：本地开发配置快照。
- `docker/docker-compose.yml`：当前 Docker Compose 配置快照。
- `volumes/mysql-cs_ai_agent.sql.gz`：MySQL 业务库逻辑备份。
- `volumes/qdrant-storage.tgz`：Qdrant 向量库卷备份。
- `volumes/agent-desk-data.tgz`：AgentDesk `/app/data` 本地上传/运行数据卷备份。
- `local/repo-data.tgz`：仓库根目录 `data/` 的本地开发数据快照。
- `SHA256SUMS`：备份文件校验值。

注意：该备份包含运行参数和业务数据，可能包含密钥、客户消息、模型配置、渠道配置等敏感信息。仓库若设为私有，仍应限制访问人。

## 新电脑恢复步骤

1. 安装 Docker Desktop、Git。
2. 克隆仓库并进入项目目录。
3. 执行：

```bash
chmod +x scripts/restore_full_backup.sh
./scripts/restore_full_backup.sh backups/migration-20260630-090044
```

4. 打开 `http://localhost:8083/dashboard/`。

脚本会自动校验备份、恢复配置、导入 MySQL、恢复 Qdrant 和 AgentDesk 数据卷，并重新构建启动服务。

## 后续再次备份

如果之后又新增了账号、知识库、消息或配置，需要重新生成一份新的 `migration-*` 目录并提交推送，否则新电脑只能恢复到本目录对应的时间点。
