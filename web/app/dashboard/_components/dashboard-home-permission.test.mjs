import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const source = await readFile(new URL("./dashboard-home.tsx", import.meta.url), "utf8")

test("dashboard overview requires explicit permission and redirects to an accessible module", () => {
  assert.match(source, /permissions\.includes\("dashboard\.view"\)/)
  assert.match(source, /filterDashboardNavForSession\(session\.permissions/)
  assert.match(source, /item\.url !== "\/dashboard"/)
  assert.match(source, /if \(!canViewOverview\) \{/)
  assert.match(source, /router\.replace\(fallbackPath\)/)
  assert.match(source, /const result = await fetchDashboardOverview\(nextRange\)/)
})
