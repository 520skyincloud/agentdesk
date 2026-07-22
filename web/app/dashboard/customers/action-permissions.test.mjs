import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

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
