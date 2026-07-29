import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

test("completed provider state resumes binding after the callback URL is cleaned", async () => {
  const source = await readFile(new URL("./page.tsx", import.meta.url), "utf8")
  const failedIndex = source.indexOf('authorizationResult === "failed"')
  const stateIndex = source.indexOf("if (storedState) {")
  const invitationIndex = source.indexOf("if (storedInvitation) {")

  assert.notEqual(failedIndex, -1)
  assert.notEqual(stateIndex, -1)
  assert.notEqual(invitationIndex, -1)
  assert.ok(failedIndex < stateIndex)
  assert.ok(stateIndex < invitationIndex)
  assert.match(
    source.slice(stateIndex, invitationIndex),
    /void loadOptions\(storedState\)/,
  )
})
