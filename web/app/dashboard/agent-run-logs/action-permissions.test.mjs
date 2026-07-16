import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("agent run log strategy filter follows the internal strategy view permission", () => {
  assert.match(pageSource, /permissions\.includes\("aiAgent\.view"\)/)
  assert.match(pageSource, /if \(!canViewRuntimeStrategies\)/)
  assert.match(pageSource, /\.{3}\(canViewRuntimeStrategies/)
  assert.match(pageSource, /const data = await fetchRuntimeStrategyOptions\(\)/)
})
