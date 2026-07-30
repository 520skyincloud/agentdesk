# 知悉微宝文档索引

本文档目录用于区分当前权威说明、部署手册、设计契约和历史交接材料。阅读顺序建议从
项目首页开始，再按部署或开发目标进入对应文档。

## 快速入口

- [项目首页](../README.md)
- [完整生产部署手册](deployment/deployment-guide.md)
- [生产密钥与外部凭据手册](deployment/production-secrets.md)
- [贡献指南](../CONTRIBUTING.md)
- [安全说明](../SECURITY.md)

## 当前权威设计

- [租户、AI 与计费统一集成方案](development/tenant-ai-unified-integration-plan.md)
- [AI 回复运行时](design/reply-runtime-engine.md)
- [规则派单引擎](design/conversation-dispatch-engine.md)
- [客服组、排班与小组](design/agent-team-squad-scheduling.md)
- [运营分析与人工回复质检](design/service-analytics-and-quality.md)
- [托管 FastGPT 门店知识库](design/fastgpt-managed-store-knowledge.md)
- [企微员工号门店范围](design/wxwork-managed-store-scope-implementation.md)
- [到店联动链接引擎](design/arrival-link-engine.md)
- [租户公司注册](design/multi-tenant-company-registration.md)

## 部署与运维

- `deployment/deployment-guide.md`：从空数据库部署、HTTPS、外部服务、备份、升级、回滚和验收。
- `deployment/production-secrets.md`：变量格式、密钥生成、归属、注入、轮换和恢复边界。
- `deployment/server-deployment-20260728.md`：特定生产环境的历史发布与验收记录，不是通用安装脚本。

## 开发与合并

- `development/integration-manifest.tsv`：统一集成文件归属、禁止项、验证命令和完成状态。
- `development/tenant-ai-unified-integration-plan.md`：当前统一架构和决策追溯。
- `development/customer-acquisition-link-engine-handoff.md`：企业微信获客助手到店主链的实现、
  配置、验证、合并和回滚交接。
- `development/tenant-company-acceptance.md`：租户与门店账号验收。

## 历史材料

以下文件用于追溯历史，不应直接作为当前运行链依据：

- `development-handoff.md`
- `development/customer-audit-merge-handoff.md`
- `development/tenant-ai-integration-merge-handoff.md`
- `wecom-hook-bridge.md`
- `generated/`

出现文档冲突时，先检查真实代码调用链，再以当前权威设计和自动化测试为准。
