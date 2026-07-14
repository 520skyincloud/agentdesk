import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("role writes require a platform account and explicit action permissions", () => {
  assert.match(pageSource, /Boolean\(session\?\.isPlatformAccount\)/)
  assert.match(pageSource, /permissionSet\.has\("role\.create"\)/)
  assert.match(pageSource, /permissionSet\.has\("role\.update"\)/)
  assert.match(pageSource, /permissionSet\.has\("role\.assignPermission"\)/)
  assert.match(pageSource, /\{canCreate \? \(/)
  assert.match(pageSource, /sortableDisabled=\{!canUpdate \|\| loading \|\| sorting\}/)
  assert.match(pageSource, /canAssignPermissions=\{canAssignPermissions\}/)
})
