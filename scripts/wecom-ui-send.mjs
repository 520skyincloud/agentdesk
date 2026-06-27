#!/usr/bin/env node

import { execFile } from "node:child_process"
import { promisify } from "node:util"

const execFileAsync = promisify(execFile)

const args = parseArgs(process.argv.slice(2))
const chat = String(args.chat || "").trim()
const message = String(args.message || "").trim()
const dryRun = Boolean(args["dry-run"])
const appName = String(args.app || process.env.WECOM_APP_NAME || "企业微信").trim()
const searchDelayMs = Math.max(Number(args["search-delay-ms"] || process.env.WECOM_UI_SEARCH_DELAY_MS || 1200), 300)
const sendDelayMs = Math.max(Number(args["send-delay-ms"] || process.env.WECOM_UI_SEND_DELAY_MS || 300), 100)

if (!chat) {
  throw new Error("missing --chat")
}
if (!message) {
  throw new Error("missing --message")
}

if (dryRun) {
  console.log(JSON.stringify({ ok: true, dryRun: true, chat, contentLength: message.length }))
  process.exit(0)
}

await setClipboard(message)

const script = `
tell application ${q(appName)} to activate
delay 0.5

tell application "System Events"
  tell process ${q(appName)}
    set frontmost to true
    keystroke "f" using command down
    delay 0.3
    keystroke ${q(chat)}
    delay ${searchDelayMs / 1000}
    key code 36
    delay ${sendDelayMs / 1000}
    keystroke "v" using command down
    delay 0.15
    key code 36
  end tell
end tell
`

await execFileAsync("osascript", ["-e", script], { maxBuffer: 1024 * 1024 })
console.log(JSON.stringify({ ok: true, driver: "ui", chat, contentLength: message.length, msgid: `wxwork_ui:${Date.now()}` }))

async function setClipboard(text) {
  await execFileAsync("osascript", ["-e", `set the clipboard to ${q(text)}`], { maxBuffer: 1024 * 1024 })
}

function parseArgs(items) {
  const parsed = {}
  for (let index = 0; index < items.length; index += 1) {
    const item = items[index]
    if (!item.startsWith("--")) {
      continue
    }
    const key = item.slice(2)
    const next = items[index + 1]
    if (!next || next.startsWith("--")) {
      parsed[key] = true
      continue
    }
    parsed[key] = next
    index += 1
  }
  return parsed
}

function q(value) {
  return JSON.stringify(String(value))
}
