#!/usr/bin/env node

import { execFile } from "node:child_process"
import { promisify } from "node:util"

const execFileAsync = promisify(execFile)

const cliBin = trimEnv("WECOM_CLI_BIN", "wecom-cli")
const chatType = Number(trimEnv("WECOM_CHAT_TYPE", "1")) === 2 ? 2 : 1
const chatIds = trimEnv("WECOM_CHAT_IDS")
  .split(",")
  .map((item) => item.trim())
  .filter(Boolean)
const minutes = Math.max(Number(trimEnv("WECOM_PROBE_MINUTES", "60")), 1)

const now = new Date()
const begin = new Date(now.getTime() - minutes * 60 * 1000)

console.log(`wecom instance probe`)
console.log(`cli=${cliBin}`)
console.log(`window=${formatTime(begin)} -> ${formatTime(now)}`)

await section("contacts", async () => {
  const result = await safeWecom("contact", "get_userlist", {})
  const users = Array.isArray(result?.userlist) ? result.userlist : []
  console.log(`count=${users.length}`)
  for (const user of users.slice(0, 50)) {
    console.log(`- userid=${value(user.userid)} name=${value(user.name)} alias=${value(user.alias)}`)
  }
})

await section("recent chats", async () => {
  let cursor = ""
  let count = 0
  do {
    const result = await safeWecom("msg", "get_msg_chat_list", {
      begin_time: formatTime(begin),
      end_time: formatTime(now),
      ...(cursor ? { cursor } : {}),
    })
    const chats = Array.isArray(result?.chats) ? result.chats : []
    count += chats.length
    for (const chat of chats) {
      console.log(
        `- chat_id=${value(chat.chat_id || chat.chatid)} chat_name=${value(chat.chat_name)} raw=${JSON.stringify(chat)}`,
      )
    }
    cursor = result?.has_more ? String(result?.next_cursor || "") : ""
  } while (cursor)
  console.log(`count=${count}`)
})

await section("manual chat messages", async () => {
  if (chatIds.length === 0) {
    console.log("skip: set WECOM_CHAT_IDS=id1,id2 to inspect known chats")
    return
  }
  for (const chatId of chatIds) {
    const result = await safeWecom("msg", "get_message", {
      chat_type: chatType,
      chatid: chatId,
      begin_time: formatTime(begin),
      end_time: formatTime(now),
    })
    const messages = Array.isArray(result?.messages) ? result.messages : []
    console.log(`chat=${chatId} type=${chatType} messages=${messages.length}`)
    for (const msg of messages.slice(-20)) {
      console.log(
        `- time=${value(msg.send_time)} user=${value(msg.userid)} type=${value(msg.msgtype)} content=${preview(extractContent(msg))}`,
      )
    }
  }
})

async function section(name, fn) {
  console.log(`\n## ${name}`)
  try {
    await fn()
  } catch (error) {
    console.log(`ERROR ${error instanceof Error ? error.message : String(error)}`)
  }
}

async function safeWecom(category, method, args) {
  const { stdout, stderr } = await execFileAsync(cliBin, [category, method, JSON.stringify(args)], {
    maxBuffer: 10 * 1024 * 1024,
    env: process.env,
  })
  if (stderr.trim()) {
    console.error(stderr.trim())
  }
  const outer = JSON.parse(stdout)
  const textContent = outer?.result?.content?.find?.((item) => item?.type === "text")?.text
  if (!textContent) {
    return outer
  }
  const inner = JSON.parse(textContent)
  if (Number(inner.errcode || 0) !== 0) {
    throw new Error(`${category}.${method} failed: ${inner.errmsg || inner.errcode || "unknown"}`)
  }
  return inner
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

function trimEnv(name, fallback = "") {
  return String(process.env[name] || fallback).trim()
}

function value(input) {
  const text = String(input || "").trim()
  return text || "-"
}

function preview(input) {
  const text = String(input || "").replace(/\s+/g, " ").trim()
  return text.length > 120 ? `${text.slice(0, 120)}...` : text
}

function formatTime(date) {
  const pad = (input) => String(input).padStart(2, "0")
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}
