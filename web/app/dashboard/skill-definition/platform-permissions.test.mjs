import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")
const crudSource = await readFile(
  new URL("../../../components/dashboard/crud/dashboard-crud-page.tsx", import.meta.url),
  "utf8",
)

test("skill definition writes require a platform account and explicit action permissions", () => {
  assert.match(pageSource, /Boolean\(session\?\.isPlatformAccount\)/)
  assert.match(pageSource, /permissions\.has\("skillDefinition\.create"\)/)
  assert.match(pageSource, /permissions\.has\("skillDefinition\.update"\)/)
  assert.match(pageSource, /permissions\.has\("skillDefinition\.delete"\)/)
  assert.match(pageSource, /showCreate=\{canCreate\}/)
  assert.match(pageSource, /showEdit=\{canUpdate\}/)
  assert.match(pageSource, /deleteItem=\{canDelete \?/)
  assert.match(pageSource, /toggle: canUpdate/)
  assert.match(pageSource, /visible: \(item\) => canDelete && item\.status/)
})

test("dashboard CRUD can hide create without hiding refresh", () => {
  assert.match(crudSource, /showCreate\?: boolean/)
  assert.match(crudSource, /showCreate = true/)
  assert.match(crudSource, /showCreate \? \(/)
})
