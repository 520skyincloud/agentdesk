#!/usr/bin/env node

const agentDeskBaseUrl = trimEnv("AGENT_DESK_BASE_URL", "http://127.0.0.1:8083")
const hookApiUrl = trimEnv("WECOM_HOOK_API_URL", "http://127.0.0.1:8060/")
const hookWsUrl = trimEnv("WECOM_HOOK_WS_URL", "ws://127.0.0.1:8061/message/")
const timeoutMs = Math.max(Number(trimEnv("DOCTOR_TIMEOUT_MS", "3000")), 1000)

let failed = false

await checkHttp("AgentDesk", agentDeskBaseUrl)
await checkHttp("WeCom hook API", hookApiUrl, {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ type: 11035, data: {} }),
})
await checkWebSocket("WeCom hook WebSocket", hookWsUrl)

if (failed) {
  process.exit(1)
}

async function checkHttp(label, url, options = {}) {
  try {
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), timeoutMs)
    const resp = await fetch(url, { ...options, signal: controller.signal })
    clearTimeout(timer)
    console.log(`[ok] ${label}: HTTP ${resp.status}`)
  } catch (error) {
    failed = true
    console.error(`[fail] ${label}: ${error instanceof Error ? error.message : String(error)}`)
  }
}

async function checkWebSocket(label, url) {
  if (typeof WebSocket === "undefined") {
    failed = true
    console.error("[fail] WebSocket: current Node.js runtime has no global WebSocket")
    return
  }
  await new Promise((resolve) => {
    let settled = false
    const timer = setTimeout(() => finish(false, "timeout"), timeoutMs)
    const ws = new WebSocket(url)

    ws.addEventListener("open", () => finish(true))
    ws.addEventListener("error", () => finish(false, "connection error"))

    function finish(ok, message = "") {
      if (settled) {
        return
      }
      settled = true
      clearTimeout(timer)
      try {
        ws.close()
      } catch {}
      if (ok) {
        console.log(`[ok] ${label}: connected`)
      } else {
        failed = true
        console.error(`[fail] ${label}: ${message}`)
      }
      resolve()
    }
  })
}

function trimEnv(name, fallback = "") {
  return String(process.env[name] || fallback).trim()
}
