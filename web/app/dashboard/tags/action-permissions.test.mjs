import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("fixed industry tags only expose alias and leaf status updates", () => {
  assert.match(pageSource, /permissions\.has\("tag\.update"\)/)
  assert.match(pageSource, /item\.children\.length > 0/)
  assert.match(pageSource, /updateTagStatus\(item\.id, nextStatus\)/)
  assert.match(pageSource, /updateTag\(\{ id: editingItem\.id, displayAlias \}\)/)
  assert.doesNotMatch(pageSource, /tag\.create|tag\.delete/)
  assert.doesNotMatch(pageSource, /createTag|deleteTag|updateTagSort|DndContext|useSortable/)
})
