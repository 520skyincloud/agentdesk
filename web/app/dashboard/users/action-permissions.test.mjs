import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const [pageSource, createSource, invitationSource, reviewSource, apiSource, zhSource, enSource] =
  await Promise.all([
    readFile(new URL("./page.tsx", import.meta.url), "utf8"),
    readFile(new URL("./_components/create.tsx", import.meta.url), "utf8"),
    readFile(new URL("./_components/invitation-dialog.tsx", import.meta.url), "utf8"),
    readFile(new URL("./_components/registration-review.tsx", import.meta.url), "utf8"),
    readFile(new URL("../../../lib/api/admin.ts", import.meta.url), "utf8"),
    readFile(new URL("../../../messages/zh-CN.json", import.meta.url), "utf8"),
    readFile(new URL("../../../messages/en-US.json", import.meta.url), "utf8"),
  ])

test("account actions use existing user permissions and function guards", () => {
  for (const code of [
    "user.create",
    "user.update",
    "user.delete",
    "user.assignRole",
    "role.view",
  ]) {
    assert.match(pageSource, new RegExp(`permissions\\.has\\("${code.replace(".", "\\.")}"\\)`))
  }
  assert.match(pageSource, /if \(\s*!canCreateUsers[\s\S]*?savingCreate/)
  assert.match(pageSource, /!canEditUser\(editingUser\)/)
  assert.match(pageSource, /!canAssignRoles[\s\S]*?!assigningRolesUser\?\.manageable/)
  assert.match(pageSource, /!canUpdateUsers \|\| !resettingUser\?\.manageable/)
  assert.match(pageSource, /!canUpdateUsers \|\| !user\.manageable \|\| actionLoadingId != null/)
  assert.match(pageSource, /!canDeleteUsers \|\| !user\.manageable \|\| actionLoadingId != null/)
  assert.match(pageSource, /\{canDeleteUsers \? \(/)
  assert.match(apiSource, /export function deleteUser\(id: number\)/)
  assert.match(apiSource, /"\/api\/dashboard\/user\/delete"/)
})

test("accounts receive roles only and never direct permissions", () => {
  assert.match(pageSource, /assignUserRoles\(assigningRolesUser\.id, roleIds, storeName\)/)
  assert.match(createSource, /roleIds: canAssignRoles \? payload\.roleIds : \[\]/)
  assert.doesNotMatch(pageSource, /assignRolePermissions|assignUserPermissions|permissionIds/)
  assert.doesNotMatch(createSource, /assignRolePermissions|assignUserPermissions|permissionIds/)
})

test("store staff team filtering and binding require team read and update permissions", () => {
  assert.match(pageSource, /permissions\.has\("agentTeam\.view"\)/)
  assert.match(pageSource, /permissions\.has\("agentTeam\.update"\)/)
  assert.match(pageSource, /if \(!canViewAgentTeams\)/)
  assert.match(
    pageSource,
    /!canViewAgentTeams \|\|[\s\S]*?!canUpdateAgentTeams \|\|[\s\S]*?!user\.storeStaff\?\.bindingId/,
  )
  assert.match(pageSource, /canViewAgentTeams &&[\s\S]*?query\.agentTeamId !== "all"/)
})

test("invitation and registration review writes keep explicit guards", () => {
  assert.match(pageSource, /permissions\.has\("tenantInvite\.view"\)/)
  assert.match(pageSource, /permissions\.has\("tenantInvite\.rotate"\)/)
  assert.match(pageSource, /permissions\.has\("tenantRegistration\.view"\)/)
  assert.match(pageSource, /permissions\.has\("tenantRegistration\.review"\)/)
  assert.match(pageSource, /open=\{invitationOpen && canViewInvitation\}/)
  assert.match(invitationSource, /if \(!canRotate \|\| rotating\)/)
  assert.match(reviewSource, /const canSubmit = Boolean\(/)
  assert.match(reviewSource, /if \(!canSubmit \|\| saving\)/)
  assert.match(reviewSource, /open=\{canSubmit\}/)
})

test("user deletion warning distinguishes permanent removal from disabling", () => {
  const zh = JSON.parse(zhSource).user
  const en = JSON.parse(enSource).user
  assert.match(zh.confirmDeleteDescription, /临时停用.*禁用/)
  assert.match(en.confirmDeleteDescription, /Disable.*temporary/i)
})

test("create user drawer keeps its actions reachable on bounded viewports", () => {
  assert.match(createSource, /DrawerContent className="overflow-hidden md:min-w-2xl"/)
  assert.doesNotMatch(createSource, /className="min-w-2xl overflow-hidden"/)
  assert.match(createSource, /className="flex min-h-0 flex-1 flex-col"/)
  assert.match(
    createSource,
    /className="min-h-0 flex-1 space-y-4 overflow-y-auto px-4 pb-4"/,
  )
  assert.match(createSource, /DrawerFooter className="shrink-0 border-t"/)
  assert.doesNotMatch(createSource, /className="flex h-full flex-col"/)
})
