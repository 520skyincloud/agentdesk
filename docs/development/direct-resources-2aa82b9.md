# 直发资源规则上线记录（2026-08-18）

- 基线：`726b0f3`（8-15 金标版），发布 `20260818-directres-2aa82b9`，提交 `2aa82b9`，分支 `codex/checkin-miniprogram-direct`
- 二进制 SHA256：`4af8b190ea559a22f635885e7786048faaf3b02d5fd74075d4b691a6f25bee72`
- 前端：从 726b0f3 自身 web 源码现场构建（避免混版），web/out 已随包部署

## 新增的两条确定性规则（其余代码与 726b0f3 逐字节一致 + 本提交）

1. 办入住执行意愿（给我办入住/帮我办个入住/我要入住/办入住/入住/入组）→ `hotel_variable/mini_program` 直发（e秒安心住卡片）
2. 明确索要卡片（定位发我/发个定位/地址发我/酒店定位/位置发我等）→ `hotel_variable/location` 位置卡片直发

## 边界保护（测试锁定）

- 两间房/办不了/手机不能用等例外与"怎么/流程/在哪"咨询仍走知识链路（知识库自行决定"转接"答案）
- 混合多请求轮次（带逗号或"还要/再发/顺便"）不接管，保留完整任务分解
- "酒店在哪/在哪里"描述性提问保持模型判断（既有契约）

## 回滚

`ln -sfn /opt/agentdesk/releases/20260815-takeover-responsive-726b0f3 /opt/agentdesk/current && systemctl restart agentdesk`
