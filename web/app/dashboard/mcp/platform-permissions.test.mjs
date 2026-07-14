import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("MCP tool execution requires a platform account and call permission", () => {
  assert.match(pageSource, /Boolean\(session\?\.isPlatformAccount\)/)
  assert.match(pageSource, /permissions\.has\("mcp\.call"\)/)
  assert.match(pageSource, /\{canCallTools \? \(/)
  assert.match(pageSource, /handleCallTool/)
})
