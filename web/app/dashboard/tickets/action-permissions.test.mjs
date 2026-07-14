import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")
const detailSource = await readFile(new URL("./_components/ticket-detail-dialog.tsx", import.meta.url), "utf8")
const editSource = await readFile(new URL("./_components/edit.tsx", import.meta.url), "utf8")
const conversationTicketSource = await readFile(
  new URL("./_components/create-ticket-from-conversation-dialog.tsx", import.meta.url),
  "utf8",
)
const ticketApiSource = await readFile(new URL("../../../lib/api/ticket.ts", import.meta.url), "utf8")
const customerLinkSource = await readFile(
  new URL("../../../components/customer-link-or-create-dialog.tsx", import.meta.url),
  "utf8",
)
const conversationPageSource = await readFile(new URL("../conversations/page.tsx", import.meta.url), "utf8")
const conversationInfoSource = await readFile(
  new URL("../conversations/_components/conversation-info-panel.tsx", import.meta.url),
  "utf8",
)

test("ticket list and detail actions follow explicit permissions", () => {
  assert.match(pageSource, /permissions\.has\("ticket\.create"\)/)
  assert.match(pageSource, /permissions\.has\("ticket\.assign"\)/)
  assert.match(pageSource, /permissions\.has\("agentProfile\.view"\)/)
  assert.match(pageSource, /permissions\.has\("tag\.view"\)/)
  assert.match(pageSource, /\{canCreate \? \(/)
  assert.match(pageSource, /open=\{canCreate && createOpen\}/)

  for (const permission of [
    "ticket.update",
    "ticket.assign",
    "ticket.changeStatus",
    "ticket.progress",
    "customer.view",
    "customer.create",
    "customer.update",
  ]) {
    assert.match(detailSource, new RegExp(`permissions\\.has\\("${permission.replace(".", "\\.")}\\"\\)`))
  }
  assert.match(detailSource, /open=\{canAssign && assignOpen\}/)
  assert.match(detailSource, /open=\{canUpdate && editOpen\}/)
  assert.match(detailSource, /open=\{canAddProgress && progressOpen\}/)
})

test("ticket content updates cannot carry assignment changes", () => {
  const updatePayload = ticketApiSource.match(/export type UpdateTicketPayload = \{([\s\S]*?)\n\}/)?.[1] ?? ""
  assert.doesNotMatch(updatePayload, /currentAssigneeId/)
  assert.match(editSource, /canAssign && !itemId/)
  assert.match(editSource, /includeInitialAssignee && currentAssigneeId > 0/)
  assert.match(conversationTicketSource, /currentAssigneeId: canAssign/)
})

test("customer link or create flow checks both context and customer permissions", () => {
  assert.match(customerLinkSource, /permissions\.has\("conversation\.linkCustomer"\)/)
  assert.match(customerLinkSource, /permissions\.has\("ticket\.update"\)/)
  assert.match(customerLinkSource, /permissions\.has\("customer\.view"\)/)
  assert.match(customerLinkSource, /permissions\.has\("customer\.create"\)/)
  assert.match(customerLinkSource, /if \(!canSearchExisting\)/)
  assert.match(customerLinkSource, /if \(!canCreateCustomer\)/)
  assert.match(conversationInfoSource, /permissions\.has\("conversation\.linkCustomer"\)/)
})

test("conversation workbench actions follow their existing permissions", () => {
  assert.match(conversationPageSource, /permissions\.has\("ticket\.create"\)/)
  assert.match(conversationPageSource, /permissions\.has\("conversation\.transfer"\)/)
  assert.match(conversationPageSource, /permissions\.has\("conversation\.close"\)/)
  assert.match(conversationPageSource, /open=\{canCreateTicket && createTicketOpen\}/)
  assert.match(conversationPageSource, /open=\{canTransferConversation && transferOpen\}/)
  assert.match(conversationPageSource, /open=\{canCloseConversation && closeOpen\}/)
})
