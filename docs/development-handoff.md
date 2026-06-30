# AgentDesk 开发续接说明

本文件用于换电脑后继续当前 Codex 会话和 AgentDesk 开发。

## 代码与运行状态

- 远端仓库：`git@github.com:520skyincloud/agentdesk.git`
- 最新主分支提交：`083937b` 起包含完整迁移备份；本文件之后的提交会包含 Codex 会话备份。
- 当前主要分支：`main` 与 `wxwork-protocol-agentdesk` 保持同步。
- 完整运行备份：`backups/migration-20260630-090044/`
- 恢复脚本：`scripts/restore_full_backup.sh`

新电脑恢复项目：

```bash
git clone git@github.com:520skyincloud/agentdesk.git
cd agentdesk
chmod +x scripts/restore_full_backup.sh
./scripts/restore_full_backup.sh backups/migration-20260630-090044
```

## Codex 会话备份

当前长会话的原始 Codex rollout 文件已备份到：

- `backups/codex-session/rollout-agent-desk-main-019e81e2-a5e3-7c70-9c68-0dbfb36dd257.jsonl.gz.part-*`
- `backups/codex-session/thread-019e81e2-a5e3-7c70-9c68-0dbfb36dd257.json`

因为原始会话文件较大，已按 50MB 切分，避免超过 GitHub 单文件限制。

新电脑重组会话备份：

```bash
chmod +x scripts/restore_codex_session_backup.sh
./scripts/restore_codex_session_backup.sh
```

脚本会生成：

- `.codex-session-restore/rollout-agent-desk-main-019e81e2-a5e3-7c70-9c68-0dbfb36dd257.jsonl`
- `.codex-session-restore/thread-019e81e2-a5e3-7c70-9c68-0dbfb36dd257.json`

如果新电脑 Codex 客户端支持导入本地 rollout/thread 文件，可以导入上述 JSONL。若不支持，开新 Codex 线程后把本文件和 `.codex-session-restore` 里的 JSONL 作为上下文即可继续开发。

## 最近关键改动

- 企微员工号协议唯一依据固定为 `https://wework.apifox.cn/llms.txt`。
- CLI / 企业微信客服号作为产品入口已废弃，新主链路是企微员工号协议 SAAS + AgentDesk 会话工作台。
- 会话页新增账号是两列弹窗：现场扫码与远程门店自助开户链接。
- 实例池支持清理未登录临时占用：`resolve_login_binding`，不自动解绑真实登录账号。
- 每个员工号绑定独立智能客服配置，原全局智能客服入口只保留兼容。
- 全托管/半托管/非托管模式影响转人工提醒走总部网页端还是门店群。
- 知识库 guard 已修正：知识库未命中不再直接固定兜底，先做意图判断；办入住、定位、小程序、转人工、寒暄、确认等走智能服务链路。
- 完整运行数据已备份到 `backups/migration-20260630-090044/`。

## 继续开发注意事项

- 修改企微员工号协议前，必须查 `wework.apifox.cn` 对应接口页面，不猜字段。
- 后端遵守 `models -> repositories -> services -> handlers`。
- 前端业务接口统一走 `web/lib/api/admin.ts`，不要在页面组件里裸 `fetch`。
- 改完后至少跑：

```bash
pnpm --dir web typecheck
docker run --rm -v "$PWD":/src -v agentdesk-go-cache:/go/pkg/mod -v agentdesk-go-build:/root/.cache/go-build -w /src golang:1.26-alpine sh -lc '/usr/local/go/bin/go test ./internal/handlers/dashboard ./internal/bootstrap ./internal/services ./internal/ai/runtime/executor -run "TestKnowledgePolicy|TestAIHandoff|TestAgentTeamSchedule|TestAuth" -count=1'
docker compose build agent-desk && docker compose up -d agent-desk
```
