#!/usr/bin/env node

import { readFile, writeFile } from "node:fs/promises"

const baseUrl = trimEnv("AGENT_DESK_BASE_URL", "http://127.0.0.1:8083")
const channelId = trimEnv("AGENT_DESK_CHANNEL_ID")
const bridgeToken = trimEnv("AGENT_DESK_BRIDGE_TOKEN")
const hookApiUrl = trimEnv("WECOM_HOOK_API_URL", "http://127.0.0.1:8060/")
const hookWsUrl = trimEnv("WECOM_HOOK_WS_URL", "ws://127.0.0.1:8061/message/")
const hookClient = trimEnv("WECOM_HOOK_CLIENT")
const selfUserIdEnv = trimEnv("WECOM_SELF_USER_ID")
const ignorePcMessages = ["1", "true", "yes", "on"].includes(
  trimEnv("WECOM_HOOK_IGNORE_PC_MESSAGES", "false").toLowerCase(),
)
const pollIntervalMs = Math.max(Number(trimEnv("POLL_INTERVAL_MS", "3000")), 1000)
const stateFile = trimEnv("STATE_FILE", ".wecom-hook-bridge-state.json")

if (!channelId || !bridgeToken) {
  console.error("Missing AGENT_DESK_CHANNEL_ID or AGENT_DESK_BRIDGE_TOKEN")
  process.exit(1)
}

if (typeof WebSocket === "undefined") {
  console.error("This Node.js runtime has no global WebSocket. Please use Node.js 22+.")
  process.exit(1)
}

let state = await loadState()
let currentClient = hookClient
let selfUserId = selfUserIdEnv
let wsConnected = false

console.log(
  `wecom-hook bridge started, channel=${channelId}, api=${hookApiUrl}, ws=${hookWsUrl}, client=${currentClient || "auto"}`,
)

connectWebSocket()
void outboundLoop()
void stateLoop()

function connectWebSocket() {
  const ws = new WebSocket(hookWsUrl)

  ws.addEventListener("open", () => {
    wsConnected = true
    console.log("[hook] websocket connected")
  })

  ws.addEventListener("message", (event) => {
    void handleHookMessage(event.data).catch((error) => {
      console.error(`[hook] handle message failed: ${error instanceof Error ? error.message : String(error)}`)
    })
  })

  ws.addEventListener("close", () => {
    wsConnected = false
    console.error("[hook] websocket closed, reconnecting in 3s")
    setTimeout(connectWebSocket, 3000)
  })

  ws.addEventListener("error", () => {
    wsConnected = false
  })
}

async function outboundLoop() {
  while (true) {
    try {
      await syncOutbound()
    } catch (error) {
      console.error(`[bridge] outbound loop failed: ${error instanceof Error ? error.message : String(error)}`)
    }
    await sleep(pollIntervalMs)
  }
}

async function stateLoop() {
  while (true) {
    await saveState()
    await sleep(10_000)
  }
}

async function handleHookMessage(raw) {
  const event = parseEvent(raw)
  if (!event || typeof event !== "object") {
    return
  }

  if (event.client != null && !currentClient) {
    currentClient = String(event.client)
  }

  if (event.type === 11026 || event.type === 11179 || event.type === 11035) {
    const userId = String(event.data?.user_id || "").trim()
    if (userId && !selfUserId) {
      selfUserId = userId
      console.log(`[hook] detected self user=${selfUserId}`)
    }
    return
  }

  if (event.type === 11024 || event.type === 11028) {
    console.log(`[hook] login event type=${event.type}`)
    return
  }

  if (event.type === 11074 || event.type === 11078 || event.type === 11213) {
    rememberRoom(event)
    return
  }

  if (![11041, 11042, 11043, 11044, 11047, 11050, 11051, 11123].includes(Number(event.type))) {
    return
  }

  const data = event.data || {}
  const chatId = String(data.conversation_id || data.room_conversation_id || "").trim()
  if (!chatId) {
    return
  }
  const senderUserId = String(data.sender || data.user_id || "").trim()
  if (selfUserId && senderUserId === selfUserId) {
    return
  }
  if (ignorePcMessages && Number(data.is_pc || 0) === 1) {
    return
  }

  const content = extractContent(event)
  if (isRecentOutboundEcho(chatId, content)) {
    state.seenInbound[buildHookMsgId(event)] = Date.now()
    return
  }

  const msgId = buildHookMsgId(event)
  if (state.seenInbound[msgId]) {
    return
  }

  const chatType = chatId.startsWith("R:") ? 2 : 1
  const chatName = chatType === 2 ? resolveRoomName(chatId, event) : String(data.sender_name || "").trim()
  const msgType = resolveMsgType(event)

  console.log(`[hook] inbound chat=${chatId} sender=${data.sender_name || senderUserId} content=${preview(content)}`)
  await postAgentDesk("/api/third/wecom-cli/inbound", {
    channelId,
    bridgeToken,
    chatId,
    chatType,
    chatName,
    msgId,
    senderUserId,
    senderName: String(data.sender_name || senderUserId || "").trim(),
    sendTime: String(data.send_time || ""),
    msgType,
    content,
    rawPayload: JSON.stringify(event),
  })
  state.seenInbound[msgId] = Date.now()
}

async function syncOutbound() {
  const data = await postAgentDesk("/api/third/wecom-cli/outbox/poll", {
    channelId,
    bridgeToken,
    limit: 20,
  })
  const items = Array.isArray(data?.items) ? data.items : []
  for (const item of items) {
    try {
      const content = String(item.content || "")
      const chatId = String(item.chatId || "").trim()
      const result = await sendText(chatId, content)
      console.log(`[hook] outbound chat=${chatId} content=${preview(content)}`)
      rememberOutboundEcho(chatId, content)
      await postAgentDesk("/api/third/wecom-cli/outbox/sent", {
        channelId,
        bridgeToken,
        outboxId: Number(item.outboxId),
        externalMsgId: String(result?.data?.msgid || result?.msgid || result?.data?.msgseq || ""),
        externalResult: JSON.stringify(result),
      })
    } catch (error) {
      await postAgentDesk("/api/third/wecom-cli/outbox/failed", {
        channelId,
        bridgeToken,
        outboxId: Number(item.outboxId),
        error: error instanceof Error ? error.message : String(error),
      })
    }
  }
}

async function sendText(conversationId, content) {
  if (!conversationId) {
    throw new Error("missing conversation_id")
  }
  const payload = withClient({
    type: 11029,
    data: {
      conversation_id: conversationId,
      content,
    },
  })
  const result = await postHook(payload)
  const errorCode = Number(result?.data?.error_code || 0)
  if (errorCode !== 0) {
    throw new Error(result?.data?.error_message || `hook send failed: ${errorCode}`)
  }
  return result
}

async function postHook(payload) {
  const resp = await fetch(hookApiUrl, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  })
  const text = await resp.text()
  let json = {}
  if (text.trim()) {
    json = JSON.parse(text)
  }
  if (!resp.ok) {
    throw new Error(`hook request failed: ${resp.status} ${text}`)
  }
  return json
}

async function postAgentDesk(path, payload) {
  const resp = await fetch(`${baseUrl}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  })
  const json = await resp.json()
  if (!resp.ok || json?.success === false) {
    throw new Error(json?.message || `AgentDesk request failed: ${resp.status}`)
  }
  return json?.data
}

function parseEvent(raw) {
  if (typeof raw !== "string") {
    raw = String(raw || "")
  }
  const text = raw.trim()
  if (!text) {
    return null
  }
  return JSON.parse(text)
}

function withClient(payload) {
  if (!currentClient) {
    return payload
  }
  return { ...payload, client: Number.isNaN(Number(currentClient)) ? currentClient : Number(currentClient) }
}

function rememberRoom(event) {
  const data = event.data || {}
  const chatId = String(data.room_conversation_id || data.conversation_id || "").trim()
  const roomName = String(data.room_name || data.nickname || "").trim()
  if (!chatId || !roomName) {
    return
  }
  state.rooms ||= {}
  state.rooms[chatId] = { roomName, updatedAt: Date.now() }
}

function resolveRoomName(chatId, event) {
  const data = event.data || {}
  return (
    String(data.room_name || "").trim() ||
    String(state.rooms?.[chatId]?.roomName || "").trim() ||
    String(data.sender_name || "").trim() ||
    chatId
  )
}

function resolveMsgType(event) {
  switch (Number(event.type)) {
    case 11041:
      return "text"
    case 11042:
      return contentTypeLabel(event.data?.content_type)
    case 11043:
      return "link"
    case 11044:
      return "gif"
    case 11047:
      return "file"
    case 11050:
      return "video"
    case 11051:
      return "mini_program"
    case 11123:
      return "revoke"
    default:
      return `type_${event.type}`
  }
}

function extractContent(event) {
  const data = event.data || {}
  if (event.type === 11041) {
    return String(data.content || "")
  }
  if (event.type === 11123) {
    return "[撤回消息]"
  }
  const label = contentTypeLabel(data.content_type)
  if (label === "image") {
    return "[图片]"
  }
  if (label === "file") {
    return `[文件] ${data.cdn?.file_name || ""}`.trim()
  }
  if (label === "video") {
    return "[视频]"
  }
  if (label === "voice") {
    return "[语音]"
  }
  if (label === "mini_program") {
    return "[小程序]"
  }
  return `[${label || `消息${event.type}`}]`
}

function contentTypeLabel(contentType) {
  switch (Number(contentType)) {
    case 2:
      return "text"
    case 14:
    case 101:
      return "image"
    case 4:
      return "voice"
    case 6:
      return "file"
    case 43:
      return "video"
    case 80:
      return "mini_program"
    default:
      return contentType ? `content_${contentType}` : ""
  }
}

function buildHookMsgId(event) {
  const data = event.data || {}
  return [
    "hook",
    event.client || currentClient || "",
    event.type || "",
    data.server_id || "",
    data.local_id || "",
    data.conversation_id || data.room_conversation_id || "",
    data.sender || data.user_id || "",
    data.send_time || "",
  ].join("|")
}

async function loadState() {
  try {
    return JSON.parse(await readFile(stateFile, "utf8"))
  } catch {
    return { rooms: {}, seenInbound: {}, sentEcho: {} }
  }
}

async function saveState() {
  const cutoff = Date.now() - 24 * 60 * 60 * 1000
  const echoCutoff = Date.now() - 30 * 60 * 1000
  state.seenInbound = Object.fromEntries(
    Object.entries(state.seenInbound || {}).filter(([, value]) => Number(value) > cutoff),
  )
  state.sentEcho = Object.fromEntries(
    Object.entries(state.sentEcho || {}).filter(([, value]) => Number(value) > echoCutoff),
  )
  await writeFile(stateFile, `${JSON.stringify(state, null, 2)}\n`, "utf8")
}

function rememberOutboundEcho(chatId, content) {
  if (!content.trim()) {
    return
  }
  state.sentEcho ||= {}
  state.sentEcho[`${chatId}|${content.trim()}`] = Date.now()
}

function isRecentOutboundEcho(chatId, content) {
  const timestamp = Number(state.sentEcho?.[`${chatId}|${String(content || "").trim()}`] || 0)
  return timestamp > 0 && Date.now() - timestamp < 30 * 60 * 1000
}

function trimEnv(name, fallback = "") {
  return String(process.env[name] || fallback).trim()
}

function preview(value) {
  const text = String(value || "").replace(/\s+/g, " ").trim()
  return text.length > 80 ? `${text.slice(0, 80)}...` : text
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}
