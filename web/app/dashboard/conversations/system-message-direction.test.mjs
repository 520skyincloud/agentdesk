import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const source = await readFile(
  new URL("./_components/chat-panel.tsx", import.meta.url),
  "utf8",
)

test("system outbound messages render on the agent side instead of as customer messages", () => {
  assert.match(source, /const isSystem = message\.senderType === "system";/)
  assert.match(
    source,
    /const isAgentSide = message\.senderType === "agent" \|\| isAi \|\| isSystem;/,
  )
  assert.match(source, /isSystem\s*\?\s*t\("conversation\.systemSender"\)/)
  assert.match(
    source,
    /isSystem\s*\?\s*t\("conversation\.systemBadge"\)\s*:\s*isAgentSide\s*\?\s*"人工"\s*:\s*"客户"/,
  )
})
