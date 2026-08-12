import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const source = await readFile(new URL("./app-sidebar.tsx", import.meta.url), "utf8")

test("sidebar navigation receives both permissions and roles from the session", () => {
  assert.match(
    source,
    /filterDashboardNavForSession\(session\?\.permissions, navContext, session\?\.roles\)/,
  )
  assert.match(source, /\[navContext, session\?\.permissions, session\?\.roles\]/)
})
