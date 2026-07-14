import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("AI config writes require a platform account and explicit action permissions", () => {
  assert.match(pageSource, /Boolean\(session\?\.isPlatformAccount\)/)
  assert.match(pageSource, /permissions\.has\("aiConfig\.create"\)/)
  assert.match(pageSource, /permissions\.has\("aiConfig\.update"\)/)
  assert.match(pageSource, /permissions\.has\("aiConfig\.delete"\)/)
  assert.match(pageSource, /showCreate=\{canCreate\}/)
  assert.match(pageSource, /showEdit=\{canUpdate\}/)
  assert.match(pageSource, /showActionsColumn=\{canUpdate \|\| canDelete\}/)
  assert.match(pageSource, /deleteItem=\{canDelete \?/)
  assert.match(pageSource, /toggle: canUpdate/)
  assert.match(pageSource, /enabled: canUpdate/)
})
