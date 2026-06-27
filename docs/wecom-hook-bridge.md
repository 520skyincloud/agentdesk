# 企业微信 Hook 桥接测试说明

这个桥接用于把 Windows 企业微信 hook 框架接入 AgentDesk。

## 组件

- Windows 企业微信客户端：需要和 hook 框架匹配的版本。
- 微灵 hook 框架：本地 HTTP 指令服务默认 `8060`，WebSocket 消息服务默认 `8061/message/`。
- AgentDesk：负责会话、AI 回复、知识库、outbox。
- `scripts/wecom-hook-bridge.mjs`：连接 hook 与 AgentDesk。

## 消息链路

```text
企业微信外部联系人/群
  -> 微灵 WebSocket 事件 11041
  -> wecom-hook-bridge
  -> /api/third/wecom-cli/inbound
  -> AgentDesk AI/知识库
  -> /api/third/wecom-cli/outbox/poll
  -> 微灵 HTTP 指令 11029
  -> 企业微信发出回复
```

## Windows 门店端启动

1. 启动企业微信并登录门店员工号。
2. 启动 `微灵.exe`，确认本机有 `8060` 和 `8061` 服务。
3. 在 AgentDesk 里准备一个企业微信 CLI/Hook 通道，拿到 `channelId` 和 `bridgeToken`。
4. 复制 `.wecom-hook-bridge.env.example` 为 `.wecom-hook-bridge.env`，填入通道参数。
5. 如果桥接跑在门店 Windows 机器上，`AGENT_DESK_BASE_URL` 要填云端/穿透地址，例如 `http://kefuceshi.omnireva.com`。只有 AgentDesk 和 hook 都在同一台机器时，才使用 `http://127.0.0.1:8083`。
6. 启动桥接：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/start-wecom-hook-bridge.ps1
```

如果同一个通道之前启用了 `wecom-cli-bridge`，先停止旧桥接，避免两个桥接抢同一个 outbox。

macOS LaunchAgent 停止示例：

```bash
launchctl unload ~/Library/LaunchAgents/com.agentdesk.wecom-cli-bridge.plist
```

## Mac/开发机自检

```bash
node scripts/wecom-hook-doctor.mjs
```

如果 `8060/8061` 失败，说明 hook 框架没有在当前机器启动。Mac 上不能直接运行这个 Windows hook 框架。

## Mac 运行方式

Mac 不能原生运行 `微灵.exe`/DLL hook。推荐把 Windows 企业微信和微灵放在 Windows 虚拟机或另一台 Windows 电脑上，Mac 只跑桥接。

Windows 端需要确认防火墙放行 `8060` 和 `8061`，并能从 Mac 访问。

Mac 启动示例：

```bash
./scripts/start-wecom-hook-bridge-mac.sh --hook-host 192.168.2.88
```

如果 AgentDesk 不在本机，指定云端/穿透地址：

```bash
./scripts/start-wecom-hook-bridge-mac.sh --hook-host 192.168.2.88 --agent-desk http://kefuceshi.omnireva.com
```

## 多门店方式

每家门店一套独立 `.wecom-hook-bridge.env`：

- `AGENT_DESK_CHANNEL_ID` 绑定该门店通道。
- AgentDesk 后台把该通道绑定到门店知识库。
- Windows 端登录该门店企业微信员工号。

这样云端只维护统一 AgentDesk，门店端只负责企业微信收发。
