# 安全说明

## 报告安全问题

请通过 GitHub 仓库的 Private Vulnerability Reporting 或 Security Advisory 私下报告。
不要在公开 Issue、PR、评论、截图或聊天中提交以下内容：

- 密码、API Key、Token、Cookie、验证码或私钥；
- `.env`、`production.env`、数据库、备份或证书文件；
- 企业微信 SuiteSecret、回调 Token、EncodingAESKey、永久授权码或预授权码；
- 小程序 AppSecret、客户身份原文、员工号 guid 或 `conversation_id`；
- 门店 NewAPI Key、FastGPT Integration Token 或加密主密钥。

报告中请提供受影响版本、复现步骤、预期与实际行为、影响范围和已完成的临时缓解措施。
日志必须先脱敏。

## 支持版本

当前只维护 GitHub `main` 分支及其明确标记的发布 commit。历史分支、历史数据库和旧 hook
bridge 不在安全支持范围内。

## 密钥处理

- 真实密钥只进入部署秘密管理器、权限为 `0600` 的仓库外环境文件或受控凭据页面。
- 门店 NewAPI Key 不进入 Git、环境模板、日志、WebSocket 或普通 API 响应。
- 加密主密钥与数据库备份分开保管。
- 密钥轮换必须同时考虑旧密文、活动会话、回调双方和回滚配置。
- 发生泄露后先撤销或轮换，再清理日志和截图；删除 Git 最新版本不能清除历史泄露。

## 数据保护

- 所有业务查询必须受 Tenant、Store 和权限范围共同限制。
- 客户身份、企业微信成员标识和员工号协议标识属于不同命名空间，不得猜测合并。
- 审计日志只保存完成安全审计所需的最少字段。
- 数据库和资产备份必须加密、限制访问并定期恢复验证。

完整部署安全要求见
[生产密钥与外部凭据手册](docs/deployment/production-secrets.md)。
