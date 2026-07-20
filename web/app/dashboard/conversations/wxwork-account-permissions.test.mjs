import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")
const managerSource = await readFile(
  new URL("../../../components/wxwork-protocol/wxwork-protocol-instance-manager.tsx", import.meta.url),
  "utf8",
)
const modelAssignmentSource = await readFile(
  new URL("../../../components/wxwork-protocol/wxwork-model-assignment-dialog.tsx", import.meta.url),
  "utf8",
)
const bindingDialogSource = await readFile(
  new URL("../../../components/wxwork-protocol/wxwork-protocol-binding-dialog.tsx", import.meta.url),
  "utf8",
)
const remoteBindingSource = await readFile(
  new URL("../../wxwork-remote-setup/page.tsx", import.meta.url),
  "utf8",
)

test("conversation workbench preserves all conversations while gating account navigation", () => {
  assert.match(pageSource, /canViewWxWorkAccounts = permissions\.has\("channel\.view"\)/)
  assert.match(pageSource, /if \(!canViewWxWorkAccounts\) \{[\s\S]*setInstances\(\[\]\)/)
  assert.match(pageSource, /setSelectedWxWorkInstanceId\(null\)/)
  assert.match(pageSource, /if \(!canViewWxWorkAccounts\) \{[\s\S]*return conversations\.reduce/)
  assert.match(pageSource, />全部账号</)
  assert.match(pageSource, /canViewWxWorkAccounts && filteredInstances\.length === 0/)
})

test("conversation workbench separates account creation from account management", () => {
  assert.match(pageSource, /canCreateWxWorkAccounts = canViewWxWorkAccounts && permissions\.has\("channel\.create"\) && permissions\.has\("user\.view"\)/)
  assert.match(pageSource, /canUpdateWxWorkAccounts = canViewWxWorkAccounts && permissions\.has\("channel\.update"\)/)
  assert.match(pageSource, /canDeleteWxWorkAccounts = canViewWxWorkAccounts && permissions\.has\("channel\.delete"\)/)
  assert.match(pageSource, /open=\{canCreateWxWorkAccounts && scanLoginOpen\}/)
  assert.match(pageSource, /open=\{canManageWxWorkAccounts && accountManagerOpen\}/)
  assert.match(pageSource, /\{canCreateWxWorkAccounts \? \(/)
  assert.match(pageSource, /\{canManageWxWorkAccounts \? \(/)
})

test("wxwork instance manager owns its CRUD and auxiliary read permissions", () => {
  for (const permission of [
    "channel.view",
    "channel.create",
    "channel.update",
    "channel.delete",
    "knowledgeBase.view",
    "user.view",
    "tenantModelAssignment.view",
    "tenantModelAssignment.update",
  ]) {
    assert.match(managerSource, new RegExp(`permissionSet\\.has\\("${permission.replace(".", "\\.")}\"\\)`))
  }
  assert.match(managerSource, /if \(!canViewChannels\) \{[\s\S]*return null/)
  assert.match(managerSource, /canViewKnowledgeBases \? fetchKnowledgeBasesAll/)
  assert.match(managerSource, /!hideCreateActions && canCreateChannels && canViewUsers/)
  assert.match(managerSource, /showEdit=\{canUpdateChannels\}/)
  assert.match(managerSource, /deleteItem=\{\s*canDeleteChannels\s*\?\s*async/)
  assert.match(managerSource, /if \(canUpdateChannels\) \{[\s\S]*key: "replaceLogin"/)
  assert.match(managerSource, /key: "modelAssignments"/)
  assert.match(managerSource, /<WxWorkModelAssignmentDialog/)
  assert.match(managerSource, /<WxWorkProtocolBindingDialog/)
  assert.match(managerSource, /open=\{canCreateChannels && canViewUsers && bindingDialogOpen\}/)
})

test("wxwork model assignment only selects from tenant grants", () => {
  assert.match(modelAssignmentSource, /fetchWxWorkModelAssignments\(tenantId, instance\.id\)/)
  assert.match(modelAssignmentSource, /access\.grants/)
  assert.match(modelAssignmentSource, /updateWxWorkModelAssignments/)
  assert.match(modelAssignmentSource, /label: "使用租户默认"/)
  assert.doesNotMatch(modelAssignmentSource, /API Key|Base URL|config\.provider|grant\.provider/)
})

test("binding dialog only links an existing store staff role account", () => {
  assert.match(bindingDialogSource, /permissionSet\.has\("channel\.create"\) && permissionSet\.has\("user\.view"\)/)
  assert.match(bindingDialogSource, /fetchUsersAll\(\{ roleCode: "store_staff", status: Status\.Ok \}\)/)
  assert.match(bindingDialogSource, /storeStaffUserId: Number\(userId\)/)
  assert.match(bindingDialogSource, /该账号代表一家门店/)
  assert.doesNotMatch(bindingDialogSource, /邀请开户|远程开户|createUser|assignUserRoles/)
})

test("remote binding page remains an existing-account binding flow", () => {
  assert.match(remoteBindingSource, /企微员工号绑定/)
  assert.match(remoteBindingSource, /本页不会注册新账号或分配角色/)
  assert.doesNotMatch(remoteBindingSource, /邀请开户|远程开户|门店开户注册|远程配置/)
})
