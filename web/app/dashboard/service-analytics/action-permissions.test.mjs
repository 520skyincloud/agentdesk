import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")
const apiSource = await readFile(new URL("../../../lib/api/service-analytics.ts", import.meta.url), "utf8")

test("service analytics actions use global permission codes", () => {
  assert.match(pageSource, /permissions\.includes\("serviceAnalytics\.view"\)/)
  assert.match(pageSource, /permissions\.includes\("serviceAnalytics\.export"\)/)
  assert.match(pageSource, /permissions\.includes\("serviceAnalytics\.managePolicy"\)/)
  assert.doesNotMatch(pageSource, /permissions\.includes\("serviceAnalytics\.manage"\)/)
  assert.match(pageSource, /canExport \? <Button/)
  assert.match(pageSource, /canManagePolicy \? <Button/)
})

test("declared analytics workflow permissions have callable API routes", () => {
  for (const path of [
    "/api/dashboard/service-analytics/export",
    "/api/dashboard/quality-sampling/list",
    "/api/dashboard/conversation-evaluation/list",
  ]) {
    assert.match(apiSource, new RegExp(path.replaceAll("/", "\\/")))
  }
})
