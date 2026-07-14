import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")
const calendarSource = await readFile(
  new URL("./_components/calendar.tsx", import.meta.url),
  "utf8",
)

test("schedule actions follow create, update, delete and batch permissions", () => {
  assert.match(pageSource, /permissions\.has\("agentTeamSchedule\.create"\)/)
  assert.match(pageSource, /permissions\.has\("agentTeamSchedule\.update"\)/)
  assert.match(pageSource, /permissions\.has\("agentTeamSchedule\.delete"\)/)
  assert.match(pageSource, /permissions\.has\("agentTeamSchedule\.batchGenerate"\)/)
  assert.match(pageSource, /showCreate=\{canCreate\}/)
  assert.match(pageSource, /showEdit=\{canUpdate\}/)
  assert.match(pageSource, /showActionsColumn=\{canUpdate \|\| canDelete\}/)
  assert.match(pageSource, /\{canBatchGenerate \? \(/)
})

test("calendar create and drag interactions use their matching permissions", () => {
  assert.match(calendarSource, /canCreate: boolean/)
  assert.match(calendarSource, /canUpdate: boolean/)
  assert.match(calendarSource, /if \(!canCreate\)/)
  assert.match(calendarSource, /if \(!canUpdate\)/)
  assert.match(calendarSource, /const readonly = !canUpdate/)
  assert.match(calendarSource, /!historical && canCreate/)
})
