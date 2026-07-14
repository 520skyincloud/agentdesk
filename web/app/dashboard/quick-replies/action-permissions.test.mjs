import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("quick reply mutations follow explicit action permissions", () => {
  assert.match(pageSource, /permissions\.has\("quickReply\.create"\)/)
  assert.match(pageSource, /permissions\.has\("quickReply\.update"\)/)
  assert.match(pageSource, /permissions\.has\("quickReply\.delete"\)/)
  assert.match(pageSource, /showCreate=\{canCreate\}/)
  assert.match(pageSource, /showEdit=\{canUpdate\}/)
  assert.match(pageSource, /showActionsColumn=\{canUpdate \|\| canDelete\}/)
  assert.match(pageSource, /deleteItem=\{canDelete \?/)
  assert.match(pageSource, /rowActions=\{/)
})
