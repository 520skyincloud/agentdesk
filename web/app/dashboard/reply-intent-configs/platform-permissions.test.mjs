import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("reply intent writes require a platform account and explicit AI config permissions", () => {
  assert.match(pageSource, /Boolean\(session\?\.isPlatformAccount\)/)
  assert.match(pageSource, /permissions\.has\("aiConfig\.update"\)/)
  assert.doesNotMatch(pageSource, /aiConfig\.(create|delete)/)
  assert.match(pageSource, /if \(!canManage\) throw new Error/)
  assert.match(pageSource, /showCreate=\{canManage\}/)
  assert.match(pageSource, /showEdit=\{canManage\}/)
  assert.match(pageSource, /deleteItem=\{canManage \? deleteIntentWithPermission : undefined\}/)
  assert.match(pageSource, /showActionsColumn=\{canManage\}/)
  assert.match(pageSource, /rowActions=\{\s*canManage\s*\? \[/)
  assert.match(pageSource, /updateStatus: updateIntentStatusWithPermission/)
})

test("reply intent list omits all-option filters from the API query", () => {
  assert.match(
    pageSource,
    /name: "intentProfileId"[^\n]*defaultValue: "all"[^\n]*allValue: "all"/
  )
  assert.match(
    pageSource,
    /name: "status"[^\n]*defaultValue: "all"[^\n]*allValue: "all"/
  )
})
