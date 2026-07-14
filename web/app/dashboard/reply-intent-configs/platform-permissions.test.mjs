import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("reply intent writes require a platform account and explicit AI config permissions", () => {
  assert.match(pageSource, /Boolean\(session\?\.isPlatformAccount\)/)
  assert.match(pageSource, /permissions\.has\("aiConfig\.create"\)/)
  assert.match(pageSource, /permissions\.has\("aiConfig\.update"\)/)
  assert.match(pageSource, /permissions\.has\("aiConfig\.delete"\)/)
  assert.match(pageSource, /if \(!canCreate\) throw new Error/)
  assert.match(pageSource, /if \(!canUpdate\) throw new Error/)
  assert.match(pageSource, /if \(!canDelete\) throw new Error/)
  assert.match(pageSource, /showCreate=\{canCreate\}/)
  assert.match(pageSource, /showEdit=\{canUpdate\}/)
  assert.match(pageSource, /deleteItem=\{canDelete \? deleteIntentWithPermission : undefined\}/)
  assert.match(pageSource, /showActionsColumn=\{canUpdate \|\| canDelete\}/)
  assert.match(pageSource, /rowActions=\{\s*canUpdate\s*\? \[/)
  assert.match(pageSource, /updateStatus: updateIntentStatusWithPermission/)
})
