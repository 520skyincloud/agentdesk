import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("AI Agent mutations follow explicit tenant action permissions", () => {
  assert.match(pageSource, /permissions\.has\("aiAgent\.create"\)/)
  assert.match(pageSource, /permissions\.has\("aiAgent\.update"\)/)
  assert.match(pageSource, /permissions\.has\("aiAgent\.delete"\)/)
  assert.match(pageSource, /showCreate=\{canCreate\}/)
  assert.match(pageSource, /showEdit=\{canUpdate\}/)
  assert.match(pageSource, /showActionsColumn=\{canUpdate \|\| canDelete\}/)
  assert.match(pageSource, /deleteItem=\{canDelete \?/)
  assert.match(pageSource, /toggle: canUpdate/)
  assert.match(pageSource, /rowActions=\{/)
  assert.match(pageSource, /sort=\{/)
})
