import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("device pool update and sync use separate platform permissions", () => {
  assert.match(pageSource, /session\?\.isPlatformAccount === true/)
  assert.match(pageSource, /permissions\.has\("wxworkDevicePool\.update"\)/)
  assert.match(pageSource, /permissions\.has\("wxworkDevicePool\.sync"\)/)
  assert.match(pageSource, /if \(!canUpdate\)/)
  assert.match(pageSource, /if \(!canSync\)/)
  assert.match(pageSource, /\{canUpdate \? \(/)
  assert.match(pageSource, /\{canSync \? \(/)
  assert.match(pageSource, /disabled=\{!canUpdate\}/)
})
