import assert from "node:assert/strict"
import test from "node:test"

import {
  findCurrentConversationReadTarget,
  mergeMessagesPreservingOrder,
} from "./ordered-message-merge.ts"

const merge = (current, incoming) => ({ ...current, ...incoming })

test("prepends an older history page without globally sorting message ids", () => {
  const current = [
    { id: 2, conversationId: 30, content: "current-2" },
    { id: 4, conversationId: 30, content: "current-4" },
  ]
  const older = [
    { id: 90, conversationId: 10, content: "history-90" },
    { id: 100, conversationId: 10, content: "history-100" },
  ]

  const result = mergeMessagesPreservingOrder(current, older, "prepend", merge)

  assert.deepEqual(
    result.map((item) => `${item.conversationId}:${item.id}`),
    ["10:90", "10:100", "30:2", "30:4"],
  )
})

test("appends realtime messages and updates duplicates in their existing position", () => {
  const current = [
    { id: 8, conversationId: 30, content: "old" },
    { id: 9, conversationId: 30, content: "nine" },
  ]
  const incoming = [
    { id: 8, conversationId: 30, content: "updated" },
    { id: 10, conversationId: 30, content: "ten" },
  ]

  const result = mergeMessagesPreservingOrder(current, incoming, "append", merge)

  assert.deepEqual(result.map((item) => item.id), [8, 9, 10])
  assert.equal(result[0].content, "updated")
})

test("selects the latest writable message from the current physical conversation", () => {
  const messages = [
    { id: 900, conversationId: 10, historicalOnly: false },
    { id: 20, conversationId: 30, historicalOnly: false },
    { id: 21, conversationId: 30, historicalOnly: true },
  ]

  assert.equal(findCurrentConversationReadTarget(messages, 30)?.id, 20)
  assert.equal(findCurrentConversationReadTarget(messages, 99), undefined)
})
