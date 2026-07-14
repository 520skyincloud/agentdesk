import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")
const detailSource = await readFile(new URL("./_components/detail.tsx", import.meta.url), "utf8")

test("conversation monitor maps actions to backend permissions", () => {
  for (const permission of [
    "conversation.assign",
    "conversation.transfer",
    "conversation.close",
    "tag.view",
    "agent.view",
    "agentTeam.view",
  ]) {
    assert.match(pageSource, new RegExp(`permissions\\.has\\("${permission.replace(".", "\\.")}\\"\\)`))
  }

  assert.match(pageSource, /canViewTags \? fetchTagsAll\(\)/)
  assert.match(pageSource, /canViewAgents \? fetchAgentProfilesAll\(\)/)
  assert.match(pageSource, /canViewTeams \? fetchAgentTeamsAll\(\)/)
  assert.match(pageSource, /open=\{canAssign && assignOpen\}/)
  assert.match(pageSource, /open=\{canTransfer && transferOpen\}/)
  assert.match(pageSource, /open=\{canClose && closeOpen\}/)
  assert.match(pageSource, /canAssign=\{canAssign\}/)
  assert.match(pageSource, /canTransfer=\{canTransfer\}/)
  assert.match(pageSource, /canClose=\{canClose\}/)
})

test("monitor detail keeps read action while gating supervisory writes", () => {
  assert.match(detailSource, /\{canAssign \? \(/)
  assert.match(detailSource, /\{canTransfer \? \(/)
  assert.match(detailSource, /\{canClose && !isClosedConversation \? \(/)
  assert.match(detailSource, /onClick=\{\(\) => void onRead\(\)\}/)
})
