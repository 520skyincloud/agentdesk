import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("industry profile writes require platform AI config permissions", () => {
  assert.match(pageSource, /Boolean\(session\?\.isPlatformAccount\)/)
  assert.match(pageSource, /permissions\.has\("aiConfig\.create"\)/)
  assert.match(pageSource, /permissions\.has\("aiConfig\.update"\)/)
  assert.match(pageSource, /permissions\.has\("aiConfig\.delete"\)/)
  assert.match(pageSource, /showCreate=\{canCreate\}/)
  assert.match(pageSource, /showEdit=\{canUpdate\}/)
  assert.match(pageSource, /deleteItem=\{canDelete \? \(item\) => deleteReplyIntentProfile\(item\.id\) : undefined\}/)
})

test("industry profile list omits the all-status option from the API query", () => {
  assert.match(
    pageSource,
    /name: "status"[^\n]*defaultValue: "all"[^\n]*allValue: "all"/
  )
})
