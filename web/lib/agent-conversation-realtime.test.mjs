import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

import { shouldReloadConversationListForRealtimePatch } from "./agent-conversation-realtime.ts"

test("reloads conversation list when realtime patch changes list membership fields", () => {
  assert.equal(
    shouldReloadConversationListForRealtimePatch({
      conversationId: 1,
      status: 3,
      currentAssigneeId: 101,
    }),
    true
  )
})

test("keeps local patching for message summary and unread-only changes", () => {
  assert.equal(
    shouldReloadConversationListForRealtimePatch({
      conversationId: 1,
      lastMessageId: 9,
      lastMessageSummary: "hello",
      agentUnreadCount: 1,
    }),
    false
  )
})

test("refreshes the affected conversation for Store customer-tag events", async () => {
  const source = await readFile(
    new URL("../hooks/use-agent-conversation-realtime.ts", import.meta.url),
    "utf8",
  )
  assert.match(source, /eventType === "customer_tag\.changed"/)
  assert.match(source, /store\.refreshConversation\(conversationId\)/)
})
