import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")
const reconciliationSource = await readFile(
  new URL("./_components/store-tag-reconciliation-dialog.tsx", import.meta.url),
  "utf8",
)
const customerApiSource = await readFile(
  new URL("../../../lib/api/customer.ts", import.meta.url),
  "utf8",
)

test("customer mutations follow explicit action permissions while detail remains visible", () => {
  assert.match(pageSource, /permissions\.has\("customer\.create"\)/)
  assert.match(pageSource, /permissions\.has\("customer\.update"\)/)
  assert.match(pageSource, /permissions\.has\("customer\.delete"\)/)
  assert.match(pageSource, /showCreate=\{canCreate\}/)
  assert.match(pageSource, /showEdit=\{canUpdate\}/)
  assert.match(pageSource, /deleteItem=\{canDelete \?/)
  assert.match(pageSource, /key: "detail"/)
  assert.match(pageSource, /\.\.\.\(canUpdate/)
})

test("customer detail keeps tags separated by Store relation", () => {
  assert.match(pageSource, /relation\.customerTags/)
  assert.match(pageSource, /<CustomerTagBadges tags=\{relation\.customerTags\}/)
  assert.match(pageSource, /permissions\.has\("conversation\.tag"\)/)
  assert.match(pageSource, /<CustomerTagHistoryDialog conversationId=\{relation\.lastConversationId\}/)
})

test("Store relation tag reconciliation reuses customer detail and existing permission", () => {
  assert.match(pageSource, /permissions\.has\("conversation\.tag"\)/)
  assert.match(pageSource, /session\?\.isPlatformAccount === true/)
  assert.match(pageSource, /session\?\.roles\?\.includes\("tenant_admin"\)/)
  assert.match(pageSource, /activeStoreRelations\.length >= 2/)
  assert.match(pageSource, /<StoreTagReconciliationDialog/)
  assert.match(reconciliationSource, /<OptionCombobox/)
  assert.match(reconciliationSource, /StoreCustomerTagReconcileStrategy\.PreserveSource/)
  assert.match(reconciliationSource, /StoreCustomerTagReconcileStrategy\.PreserveTarget/)
  assert.match(reconciliationSource, /StoreCustomerTagReconcileStrategy\.ClearRebuild/)
  assert.match(reconciliationSource, /confirmed: true/)
  assert.match(customerApiSource, /\/api\/dashboard\/customer\/reconcile_store_relation_tags/)
})
