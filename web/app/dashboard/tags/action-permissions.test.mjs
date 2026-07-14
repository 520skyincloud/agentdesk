import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("tag mutations and drag sorting follow explicit action permissions", () => {
  assert.match(pageSource, /permissions\.has\("tag\.create"\)/)
  assert.match(pageSource, /permissions\.has\("tag\.update"\)/)
  assert.match(pageSource, /permissions\.has\("tag\.delete"\)/)
  assert.match(pageSource, /const showActions = canUpdate \|\| canDelete/)
  assert.match(pageSource, /\{canCreate \? \(/)
  assert.match(pageSource, /disabled=\{!canUpdate \|\| loading \|\| sorting\}/)
  assert.match(pageSource, /if \(!canUpdate\)/)
  assert.match(pageSource, /if \(!canDelete\)/)
  assert.match(
    pageSource,
    /colSpan=\{4 \+ \(canUpdate \? 1 : 0\) \+ \(showActions \? 1 : 0\)\}/,
  )
})
