import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const [pageSource, pickerSource, apiSource, navigationSource] = await Promise.all([
  readFile(new URL("./page.tsx", import.meta.url), "utf8"),
  readFile(new URL("./_components/store-room-picker.tsx", import.meta.url), "utf8"),
  readFile(new URL("../../../lib/api/store-workbench.ts", import.meta.url), "utf8"),
  readFile(new URL("../../../lib/navigation.tsx", import.meta.url), "utf8"),
])

test("store workbench view and update stay as separate visible permissions", () => {
  assert.match(pageSource, /permissions\.has\("storeWorkbench\.view"\)/)
  assert.match(pageSource, /permissions\.has\("storeWorkbench\.update"\)/)
  assert.match(pageSource, /if \(!canView\)/)
  assert.match(pageSource, /if \(!canUpdate \|\| !data\?\.bound \|\| !form \|\| saving\) return/)
  assert.match(navigationSource, /requiredPermission: "storeWorkbench\.view"/)
})

test("workbench APIs never accept an arbitrary instance or binding id", () => {
  for (const path of ["current", "update", "room_list", "room_member_list"]) {
    assert.match(apiSource, new RegExp(`/api/dashboard/store-workbench/${path}`))
  }
  const updatePayload = apiSource.match(/export type UpdateStoreWorkbenchPayload = \{([\s\S]*?)\n\}/)?.[1] ?? ""
  assert.doesNotMatch(updatePayload, /instanceId|bindingId|userId|storeId/)
  assert.doesNotMatch(apiSource, /fetch\(/)
})

test("notification rooms and members come from protocol-backed selectors", () => {
  assert.match(pickerSource, /fetchStoreWorkbenchRooms\(\)/)
  assert.match(pickerSource, /fetchStoreWorkbenchRoomMembers\(roomConversationId\)/)
  assert.match(pickerSource, /max-h-56[\s\S]*overflow-y-auto/)
  assert.match(pickerSource, /<OptionCombobox/)
  assert.doesNotMatch(pickerSource, /<Input/)
})
