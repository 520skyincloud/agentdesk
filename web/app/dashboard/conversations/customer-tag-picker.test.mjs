import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const source = await readFile(
  new URL("./_components/customer-tag-picker.tsx", import.meta.url),
  "utf8",
)

test("Store customer tag picker enforces the six-tag ceiling and conflict replacement", () => {
  assert.match(source, /const MAX_CUSTOMER_TAGS = 6/)
  assert.match(source, /currentTags\.length >= MAX_CUSTOMER_TAGS && !conflictingTag/)
  assert.match(source, /replaceCustomerTag\(/)
  assert.match(source, /oldTagId: conflictingTag\.tagId/)
})

test("disabled fixed tags cannot be added but remain removable when selected", () => {
  assert.match(source, /isTagDisabled=\{\(tag\) => tag\.status !== 0\}/)
  assert.match(source, /removeCustomerTag\(/)
  assert.doesNotMatch(source, /addConversationTag|removeConversationTag|\/conversation\/add_tag/)
})
