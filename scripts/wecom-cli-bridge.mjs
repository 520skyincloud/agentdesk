#!/usr/bin/env node

import { execFile } from "node:child_process"
import { readFile, writeFile } from "node:fs/promises"
import { promisify } from "node:util"

const execFileAsync = promisify(execFile)

const baseUrl = trimEnv("AGENT_DESK_BASE_URL", "http://127.0.0.1:8083")
const channelId = trimEnv("AGENT_DESK_CHANNEL_ID")
const bridgeToken = trimEnv("AGENT_DESK_BRIDGE_TOKEN")
const cliBin = trimEnv("WECOM_CLI_BIN", "wecom-cli")
const sendDriver = trimEnv("WECOM_SEND_DRIVER", "cli").toLowerCase()
const uiSenderBin = trimEnv("WECOM_UI_SENDER_BIN", "node")
const uiSenderScript = trimEnv("WECOM_UI_SENDER_SCRIPT", "scripts/wecom-ui-send.mjs")
const chatType = Number(trimEnv("WECOM_CHAT_TYPE", "1")) === 2 ? 2 : 1
const chatIds = trimEnv("WECOM_CHAT_IDS")
  .split(",")
  .map((item) => item.trim())
  .filter(Boolean)
const contactUserIds = trimEnv("WECOM_CONTACT_USER_IDS")
  .split(",")
  .map((item) => item.trim())
  .filter(Boolean)
const autoPollContacts = ["1", "true", "yes", "on"].includes(
  trimEnv("WECOM_AUTO_POLL_CONTACTS", "false").toLowerCase(),
)
const ignoreUserIds = new Set(
  trimEnv("WECOM_IGNORE_USER_IDS")
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean),
)
const pollIntervalMs = Math.max(Number(trimEnv("POLL_INTERVAL_MS", "5000")), 1000)
const stateFile = trimEnv("STATE_FILE", ".wecom-cli-bridge-state.json")

if (!channelId || !bridgeToken) {
  console.error("Missing AGENT_DESK_CHANNEL_ID or AGENT_DESK_BRIDGE_TOKEN")
  process.exit(1)
}

let state = await loadState()
let contactCache = { loadedAt: 0, chats: [] }

console.log(
  `wecom-cli bridge started, channel=${channelId}, chats=${chatIds.length ? chatIds.join(",") : "auto"}, contacts=${autoPollContacts || contactUserIds.length ? "enabled" : "disabled"}, sendDriver=${sendDriver}`,
)

while (true) {
  try {
    await syncInbound()
    await syncOutbound()
    await saveState()
  } catch (error) {
    console.error(`[bridge] ${error instanceof Error ? error.message : String(error)}`)
  }
  await sleep(pollIntervalMs)
}

async function syncInbound() {
  const now = new Date()
  const begin = new Date(now.getTime() - 10 * 60 * 1000)
  const chats = await resolveChats(begin, now)
  for (const chat of chats) {
    const chatId = chat.chatId
    let result
    try {
      result = await wecom("msg", "get_message", {
        chat_type: chat.chatType,
        chatid: chatId,
        begin_time: formatTime(begin),
        end_time: formatTime(now),
      })
    } catch (error) {
      console.error(
        `[bridge] get_message failed chat=${chatId}: ${error instanceof Error ? error.message : String(error)}`,
      )
      continue
    }
    const messages = Array.isArray(result.messages) ? result.messages : []
    for (const message of messages) {
      if (ignoreUserIds.has(String(message.userid || ""))) {
        continue
      }
      const msgId = buildMsgId(chatId, message)
      const content = extractContent(message)
      if (isRecentOutboundEcho(chatId, content)) {
        state.seenInbound[msgId] = Date.now()
        continue
      }
      if (state.seenInbound[msgId]) {
        continue
      }
      console.log(`[bridge] inbound chat=${chatId} type=${message.msgtype || "text"} content=${preview(content)}`)
      await postAgentDesk("/api/third/wecom-cli/inbound", {
        channelId,
        bridgeToken,
        chatId,
        chatType: chat.chatType,
        chatName: chat.chatName,
        msgId,
        senderUserId: String(message.userid || ""),
        senderName: String(message.userid || ""),
        sendTime: String(message.send_time || ""),
        msgType: String(message.msgtype || "text"),
        content,
        rawPayload: JSON.stringify(message),
      })
      state.seenInbound[msgId] = Date.now()
    }
  }
}

async function resolveChats(begin, now) {
  if (chatIds.length > 0) {
    return uniqueChats([
      ...chatIds.map((chatId) => ({ chatId, chatName: "", chatType })),
      ...(await getContactChats()),
    ])
  }
  const discovered = []
  let cursor = ""
  do {
    const result = await wecom("msg", "get_msg_chat_list", {
      begin_time: formatTime(begin),
      end_time: formatTime(now),
      ...(cursor ? { cursor } : {}),
    })
    const chats = Array.isArray(result.chats) ? result.chats : []
    for (const chat of chats) {
      const chatId = String(chat.chat_id || chat.chatid || "").trim()
      if (!chatId) {
        continue
      }
      discovered.push({
        chatId,
        chatName: String(chat.chat_name || "").trim(),
        chatType,
      })
    }
    cursor = result.has_more ? String(result.next_cursor || "") : ""
  } while (cursor)
  if (discovered.length > 0) {
    console.log(`[bridge] discovered ${discovered.length} chat(s)`)
  }
  return uniqueChats([...discovered, ...(await getContactChats())])
}

async function getContactChats() {
  if (contactUserIds.length > 0) {
    return contactUserIds.map((userId) => ({ chatId: userId, chatName: "", chatType: 1 }))
  }
  if (!autoPollContacts) {
    return []
  }
  if (Date.now() - contactCache.loadedAt < 5 * 60 * 1000) {
    return contactCache.chats
  }
  const result = await wecom("contact", "get_userlist", {})
  const users = Array.isArray(result.userlist) ? result.userlist : []
  contactCache = {
    loadedAt: Date.now(),
    chats: users
      .map((user) => ({
        chatId: String(user.userid || "").trim(),
        chatName: String(user.name || user.alias || "").trim(),
        chatType: 1,
      }))
      .filter((chat) => chat.chatId),
  }
  console.log(`[bridge] loaded ${contactCache.chats.length} contact chat(s)`)
  return contactCache.chats
}

function uniqueChats(chats) {
  const seen = new Set()
  return chats.filter((chat) => {
    const key = `${chat.chatType}:${chat.chatId}`
    if (seen.has(key)) {
      return false
    }
    seen.add(key)
    return true
  })
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
      const result = await sendMessage(item)
      console.log(`[bridge] outbound chat=${item.chatId} content=${preview(item.content)}`)
      rememberOutboundEcho(String(item.chatId), String(item.content || ""))
      await postAgentDesk("/api/third/wecom-cli/outbox/sent", {
        channelId,
        bridgeToken,
        outboxId: Number(item.outboxId),
        externalMsgId: String(result.msgid || result.msg_id || ""),
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

async function sendMessage(item) {
  if (sendDriver === "ui") {
    const { stdout } = await execFileAsync(
      uiSenderBin,
      [
        uiSenderScript,
        "--chat",
        String(item.chatName || item.chatId || ""),
        "--message",
        String(item.content || ""),
      ],
      {
        maxBuffer: 1024 * 1024,
        env: process.env,
      },
    )
    try {
      return JSON.parse(stdout)
    } catch {
      return { msgid: `wxwork_ui:${Date.now()}`, stdout }
    }
  }
  return wecom("msg", "send_message", {
    chat_type: Number(item.chatType) === 2 ? 2 : 1,
    chatid: String(item.chatId),
    msgtype: "text",
    text: { content: String(item.content || "") },
  })
}

async function wecom(category, method, args) {
  const { stdout } = await execFileAsync(cliBin, [category, method, JSON.stringify(args)], {
    maxBuffer: 10 * 1024 * 1024,
  })
  const outer = JSON.parse(stdout)
  const textContent = outer?.result?.content?.find?.((item) => item?.type === "text")?.text
  if (!textContent) {
    return outer
  }
  const inner = JSON.parse(textContent)
  if (Number(inner.errcode || 0) !== 0) {
    throw new Error(
      `wecom-cli ${category}.${method} failed: ${inner.errmsg || inner.errcode || "unknown"}`,
    )
  }
  return inner
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

function extractContent(message) {
  if (message?.msgtype === "text") {
    return String(message?.text?.content || "")
  }
  if (message?.msgtype === "image") {
    return "[图片]"
  }
  if (message?.msgtype === "file") {
    return `[文件] ${message?.file?.name || ""}`.trim()
  }
  if (message?.msgtype === "voice") {
    return "[语音]"
  }
  if (message?.msgtype === "video") {
    return "[视频]"
  }
  return `[${message?.msgtype || "消息"}]`
}

function buildMsgId(chatId, message) {
  return [
    chatId,
    message?.userid || "",
    message?.send_time || "",
    message?.msgtype || "",
    extractContent(message),
  ].join("|")
}

async function loadState() {
  try {
    return JSON.parse(await readFile(stateFile, "utf8"))
  } catch {
    return { seenInbound: {}, sentEcho: {} }
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

function formatTime(date) {
  const pad = (value) => String(value).padStart(2, "0")
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}
