import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const hookSource = await readFile(
  new URL("./use-ai-configuration-realtime.ts", import.meta.url),
  "utf8",
)
const adminAPISource = await readFile(
  new URL("../lib/api/admin.ts", import.meta.url),
  "utf8",
)

test("configuration realtime uses its isolated authenticated endpoint", () => {
  assert.match(adminAPISource, /"\/api\/ws\/configuration"/)
  assert.match(hookSource, /createConfigurationWebSocketUrl/)
  assert.match(hookSource, /createRealtimeConnectionManager/)
})

test("configuration realtime accepts only the three redacted refresh events", () => {
  for (const eventType of [
    "store_model_profile.changed",
    "store_model_credential.changed",
    "fastgpt_profile.changed",
  ]) {
    assert.match(hookSource, new RegExp(eventType.replace(".", "\\.")))
  }
  for (const forbidden of [
    "apiKey",
    "prompt",
    "schema",
    "cipher",
    "nonce",
    "fingerprint",
  ]) {
    assert.doesNotMatch(hookSource, new RegExp(`${forbidden}\\??:`))
  }
})

test("configuration pages subscribe through the shared hook", async () => {
  for (const path of [
    "../app/dashboard/model-profiles/page.tsx",
    "../app/dashboard/channels/_components/model-access.tsx",
    "../components/store-model-credential.tsx",
    "../app/dashboard/knowledge/_components/fastgpt-knowledge-workspace.tsx",
    "../app/dashboard/billing-query/page.tsx",
  ]) {
    const source = await readFile(new URL(path, import.meta.url), "utf8")
    assert.match(source, /useAIConfigurationRealtime/)
  }
})
