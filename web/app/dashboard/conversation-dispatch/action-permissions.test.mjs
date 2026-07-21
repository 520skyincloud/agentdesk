import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("dispatch workbench separates read access from granular orchestration actions", () => {
  assert.match(pageSource, /permissions\.has\("conversation\.assign"\)/)
  assert.match(pageSource, /permissions\.has\("conversation\.transfer"\)/)
  assert.match(pageSource, /permissions\.has\("conversation\.recycle"\)/)
  assert.match(pageSource, /if \(!permissions\.has\("conversation\.handover"\)\) return/)
  assert.match(pageSource, /permissions\.has\("agentTeam\.view"\)/)
  assert.match(pageSource, /if \(!canAssign\)/)
  assert.match(pageSource, /if \(type === "assign"\) return canAssign/)
  assert.match(pageSource, /if \(type === "transfer"\) return canTransfer/)
  assert.match(pageSource, /return canRecycle/)
  assert.match(pageSource, /if \(!canViewTeams\)/)
  assert.match(pageSource, /const data = await fetchAgentTeamsAll\(\)/)
  assert.match(pageSource, /colSpan=\{canManageActions \? 6 : 5\}/)
  assert.match(pageSource, /\{canAssign \? \(/)
  assert.match(pageSource, /\{canTransfer \? \(/)
  assert.match(pageSource, /\{canRecycle \? \(/)
  assert.match(pageSource, /open=\{Boolean\(dialog && canUseAction\(dialog\.type\)\)\}/)
})

test("dispatch workbench uses tenant-scoped realtime refresh with hidden-tab fallback", () => {
  assert.match(pageSource, /createRealtimeConnectionManager/)
  assert.match(pageSource, /createAdminWebSocketUrl/)
  assert.match(pageSource, /payload\.type\?\.startsWith\("conversation\."\)/)
  assert.match(pageSource, /document\.visibilityState === "hidden"/)
  assert.match(pageSource, /60_000/)
})
