import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("company CRUD and status actions use their matching permissions", () => {
  assert.match(pageSource, /permissionSet\.has\("company\.create"\)/)
  assert.match(pageSource, /permissionSet\.has\("company\.update"\)/)
  assert.match(pageSource, /permissionSet\.has\("company\.delete"\)/)
  assert.match(pageSource, /if \(!canCreate\) throw new Error/)
  assert.match(pageSource, /if \(!canUpdate\) throw new Error/)
  assert.match(pageSource, /if \(!canDelete\) throw new Error/)
  assert.match(pageSource, /if \(canUpdate\) \{\s*rowActions\.push\(/)
  assert.match(pageSource, /updateStatus: updateCompanyStatusWithPermission/)
  assert.match(pageSource, /showCreate=\{canCreate\}/)
  assert.match(pageSource, /showEdit=\{canUpdate\}/)
  assert.match(pageSource, /deleteItem=\{canDelete \? deleteCompanyWithPermission : undefined\}/)
  assert.match(pageSource, /showActionsColumn=\{rowActions\.length > 0 \|\| canDelete\}/)
})

test("company account navigation follows channel view permission", () => {
  assert.match(pageSource, /permissionSet\.has\("channel\.view"\)/)
  assert.match(pageSource, /if \(!canViewAccounts\)/)
  assert.match(pageSource, /if \(canViewAccounts\) \{\s*rowActions\.push\(/)
  assert.match(pageSource, /key: "accounts"/)
})

test("internal company page does not expose tenant model credentials", () => {
  assert.doesNotMatch(pageSource, /aiConfig\.(view|update)/)
  assert.doesNotMatch(pageSource, /CompanyAIModelSettingsDialog/)
  assert.doesNotMatch(pageSource, /API Key|Base URL|模型设置/)
})
