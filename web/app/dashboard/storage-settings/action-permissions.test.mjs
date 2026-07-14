import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("storage settings remain read only without platform update permission", () => {
  assert.match(pageSource, /session\?\.isPlatformAccount/)
  assert.match(pageSource, /session\.permissions\.includes\("storageSetting\.update"\)/)
  assert.match(pageSource, /if \(!canUpdate\)/)
  assert.match(pageSource, /\{canUpdate \? \(/)
  assert.match(pageSource, /disabled=\{!canUpdate\}/)
})
