import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("dispatch wait column labels the manual response window explicitly", () => {
  assert.match(pageSource, /formatDuration\(task\.waitingSeconds\)/)
  assert.match(pageSource, /conversationDispatch\.manualWindowUntil/)
  assert.doesNotMatch(pageSource, /task\.manualExpireAt \? formatDateTime\(task\.manualExpireAt\)/)
})
