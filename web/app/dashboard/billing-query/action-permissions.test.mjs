import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageUrl = new URL("./page.tsx", import.meta.url)
const apiUrl = new URL("../../../lib/api/billing-query.ts", import.meta.url)

test("billing page uses explicit view and export permissions", async () => {
  const source = await readFile(pageUrl, "utf8")

  assert.match(source, /permissions\.includes\("billing\.view"\)/)
  assert.match(source, /permissions\.includes\("billing\.export"\)/)
  assert.match(source, /options\?\.scopeMode === "store"/)
  assert.match(source, /maximumSelectedStores = 50/)
})

test("billing API uses one scoped backend capability and exposes no model infrastructure fields", async () => {
  const source = await readFile(apiUrl, "utf8")

  assert.match(source, /\/api\/dashboard\/billing-query\/options/)
  assert.match(source, /\/api\/dashboard\/billing-query\/get/)
  assert.match(source, /\/api\/dashboard\/billing-query\/export/)
  assert.doesNotMatch(source, /\b(apiKey|baseUrl|provider|promptTemplate|jsonSchema)\b/i)
})
