import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("dispatch workbench separates read access from handover actions", () => {
  assert.match(pageSource, /permissions\.has\("conversation\.handover"\)/)
  assert.match(pageSource, /permissions\.has\("agentTeam\.view"\)/)
  assert.match(pageSource, /if \(!canHandover\)/)
  assert.match(pageSource, /if \(!canViewTeams\)/)
  assert.match(pageSource, /const data = await fetchAgentTeamsAll\(\)/)
  assert.match(pageSource, /colSpan=\{canHandover \? 6 : 5\}/)
  assert.match(pageSource, /\{canHandover \? \(/)
  assert.match(pageSource, /open=\{canHandover && Boolean\(dialog\)\}/)
})
