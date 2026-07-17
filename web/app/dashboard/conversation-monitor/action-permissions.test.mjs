import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")
const detailSource = await readFile(new URL("./_components/detail.tsx", import.meta.url), "utf8")
const workflowSource = await readFile(new URL("./_components/session-workflow.tsx", import.meta.url), "utf8")

test("conversation monitor maps actions to backend permissions", () => {
  for (const permission of [
    "conversationRecord.view",
    "conversation.assign",
    "conversation.transfer",
    "conversation.close",
  ]) {
    assert.match(pageSource, new RegExp(`permissions\\.has\\("${permission.replace(".", "\\.")}\\"\\)`))
  }

  assert.match(pageSource, /fetchServiceSessionDimensions\(\)/)
  assert.match(pageSource, /dispatchConversation\(item\.conversationId\)/)
  assert.match(pageSource, /markConversationRead\(item\.conversationId\)/)
  assert.match(pageSource, /open=\{canAssign && assignOpen\}/)
  assert.match(pageSource, /open=\{canTransfer && transferOpen\}/)
  assert.match(pageSource, /open=\{canClose && closeOpen\}/)
  assert.match(pageSource, /canAssign=\{canAssign && selectedSession\?\.status === "open"\}/)
  assert.match(pageSource, /canTransfer=\{canTransfer && selectedSession\?\.status === "open"\}/)
  assert.match(pageSource, /canClose=\{canClose && selectedSession\?\.status === "open"\}/)
  assert.doesNotMatch(pageSource, /canAssign=\{false\}/)
  assert.doesNotMatch(pageSource, /canTransfer=\{false\}/)
  assert.doesNotMatch(pageSource, /canClose=\{false\}/)
})

test("monitor detail keeps read action while gating supervisory writes", () => {
  assert.match(detailSource, /\{canAssign \? \(/)
  assert.match(detailSource, /\{canTransfer \? \(/)
  assert.match(detailSource, /\{canClose && !isClosedConversation \? \(/)
  assert.match(detailSource, /onClick=\{\(\) => void onRead\(\)\}/)
})

test("human quality inspection separates read access from scoring access", () => {
  assert.match(pageSource, /canViewQuality=\{canQuality\}/)
  assert.match(pageSource, /canManageQuality=\{canManageQuality\}/)
  assert.doesNotMatch(pageSource, /canQuality=\{canQuality && canManageQuality\}/)
  assert.match(workflowSource, /\{canViewQuality \? <TabsTrigger value="quality">人工质检<\/TabsTrigger> : null\}/)
  assert.match(workflowSource, /disabled=\{qualityLocked \|\| !canManageQuality\}/)
  assert.match(workflowSource, /\{canManageQuality && !qualityLocked \? <div/)
})
