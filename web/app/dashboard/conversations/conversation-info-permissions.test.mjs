import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const source = await readFile(
  new URL("./_components/conversation-info-panel.tsx", import.meta.url),
  "utf8",
)

test("conversation info auxiliary reads follow their explicit view permissions", () => {
  assert.match(source, /canViewCustomer = permissions\.has\("customer\.view"\)/)
  assert.match(source, /canViewTickets: permissions\.has\("ticket\.view"\)/)
  assert.match(source, /canViewTags = permissions\.has\("tag\.view"\)/)
  assert.match(source, /if \(!permissions\.canViewCustomer\)/)
  assert.match(source, /if \(!permissions\.canViewTags\)/)
  assert.match(source, /permissions\.canViewTickets \? \(/)
})

test("conversation info mutations follow customer and tag action permissions", () => {
  assert.match(source, /canUpdateCustomer: canViewCustomer && permissions\.has\("customer\.update"\)/)
  assert.match(source, /canManageTags: canViewTags && permissions\.has\("conversation\.tag"\)/)
  assert.match(source, /permissions\.canManageTags \? \(/)
  assert.match(source, /permissions\.canUpdateCustomer \? \(/)
  assert.match(source, /if \(!permissions\.canUpdateCustomer \|\| customerEditSaving\)/)
  assert.doesNotMatch(source, /company\.update|canUpdateCompany/)
})

test("assigned conversation tags stay visible without tag tree access", () => {
  assert.match(source, /<ConversationTagBadges/)
  assert.match(source, /tags=\{conversation\.tags\}/)
  assert.match(source, /availableTags=\{availableTags\}/)
})
