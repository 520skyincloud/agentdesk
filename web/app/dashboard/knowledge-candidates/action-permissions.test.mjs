import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("knowledge candidate review actions reuse knowledge base update permission", () => {
  assert.match(pageSource, /permissions\.has\("knowledgeBase\.update"\)/)
  assert.match(pageSource, /if \(!canManage\)/)
  assert.match(pageSource, /\.\.\.\(canManage/)
  assert.match(pageSource, /\{canManage \? \(/)
  assert.match(pageSource, /open=\{canManage && !!editing\}/)
  assert.match(pageSource, /conversationId > 0/)
})
