import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("dispatch wait column uses queue and first-response SLA instead of route manual expiry", () => {
  assert.match(pageSource, /formatDuration\(task\.waitingSeconds\)/)
	assert.match(pageSource, /conversationDispatch\.queueSlaUntil/)
	assert.match(pageSource, /conversationDispatch\.firstResponseSlaUntil/)
	assert.doesNotMatch(pageSource, /task\.manualExpireAt/)
})
