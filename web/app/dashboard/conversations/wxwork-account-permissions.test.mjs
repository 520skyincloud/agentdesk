import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")
const storeStaffIdentitySource = await readFile(
  new URL("./_components/store-staff-conversation-identity.tsx", import.meta.url),
  "utf8",
)
const chatPanelSource = await readFile(
  new URL("./_components/chat-panel.tsx", import.meta.url),
  "utf8",
)
const managerSource = await readFile(
  new URL("../../../components/wxwork-protocol/wxwork-protocol-instance-manager.tsx", import.meta.url),
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
  assert.match(pageSource, /roles\.includes\("store_staff"\)/)
  assert.match(pageSource, /\["super_admin", "admin", "tenant_admin", "cs_team_leader", "cs_user"\]\.includes\(role\)/)
  assert.match(pageSource, /canViewWxWorkAccounts = !isStoreStaff && permissions\.has\("channel\.view"\)/)
  assert.match(pageSource, /if \(!canViewWxWorkAccounts\) \{[\s\S]*setInstances\(\[\]\)/)
  assert.match(pageSource, /setSelectedWxWorkInstanceId\(null\)/)
  assert.match(pageSource, /if \(!canViewWxWorkAccounts\) \{[\s\S]*return conversations\.reduce/)
  assert.match(pageSource, />全部账号</)
  assert.match(pageSource, /canViewWxWorkAccounts && filteredInstances\.length === 0/)
})

test("store staff conversation mode uses the current binding workbench instead of the tenant account pool", () => {
  assert.match(pageSource, /fetchStoreWorkbench\(\)/)
  assert.match(pageSource, /<StoreStaffConversationIdentity/)
  assert.match(pageSource, /data=\{storeStaffWorkspace\}/)
  assert.match(pageSource, /variant="compact"/)
  assert.match(pageSource, /conversationFilter === "my_attention"[\s\S]*setConversationFilter\("all_open"\)/)
  assert.match(pageSource, /agentConversationFilterOptions[\s\S]*item\.value !== "my_attention"/)
  assert.match(pageSource, /aiReplyEnabled=\{isStoreStaff \? storeStaffWorkspace\?\.aiReplyEnabled/)
  assert.match(storeStaffIdentitySource, /我的企微账号/)
  assert.match(storeStaffIdentitySource, /wxWorkHealthStatus/)
  assert.match(storeStaffIdentitySource, /storeCode/)
  assert.match(storeStaffIdentitySource, /variant = "sidebar"/)
  assert.match(storeStaffIdentitySource, /lg:hidden/)
  assert.match(chatPanelSource, /canToggleAIReply = true/)
  assert.match(chatPanelSource, /aiReplyToggleDisabled=\{!canToggleAIReply \|\| !wxWorkInstance \|\| savingAIReply\}/)
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
  ]) {
    assert.match(managerSource, new RegExp(`permissionSet\\.has\\("${permission.replace(".", "\\.")}\"\\)`))
  }
  assert.match(managerSource, /if \(!canViewChannels\) \{[\s\S]*return null/)
  assert.match(managerSource, /canViewKnowledgeBases \? fetchKnowledgeBasesAll/)
  assert.match(managerSource, /!hideCreateActions && canCreateChannels && canViewUsers/)
  assert.match(managerSource, /showEdit=\{canUpdateChannels\}/)
  assert.match(managerSource, /deleteItem=\{\s*canDeleteChannels\s*\?\s*async/)
  assert.match(managerSource, /if \(canUpdateChannels\) \{[\s\S]*key: "replaceLogin"/)
  assert.match(managerSource, /<WxWorkProtocolBindingDialog/)
  assert.match(managerSource, /open=\{canCreateChannels && canViewUsers && bindingDialogOpen\}/)
})

test("wxwork manager does not expose the retired model grant chain", () => {
  assert.match(managerSource, /模型与凭据由绑定门店的生效配置统一解析/)
  assert.doesNotMatch(managerSource, /tenantModelAssignment|modelAssignments|WxWorkModelAssignmentDialog|员工号覆盖租户默认|授权池兜底/)
})

test("binding dialog only links an existing store staff role account", () => {
  assert.match(bindingDialogSource, /permissionSet\.has\("channel\.create"\) && permissionSet\.has\("user\.view"\)/)
  assert.match(bindingDialogSource, /fetchUsersAll\(\{ roleCode: "store_staff", status: Status\.Ok \}\)/)
  assert.match(bindingDialogSource, /storeStaffUserId: Number\(userId\)/)
  assert.match(bindingDialogSource, /门店归属以系统员工号的 Store ID 为准/)
  assert.doesNotMatch(bindingDialogSource, /邀请开户|远程开户|createUser|assignUserRoles/)
})

test("remote binding page remains an existing-account binding flow", () => {
  assert.match(remoteBindingSource, /企微员工号绑定/)
  assert.match(remoteBindingSource, /本页不会注册新账号或分配角色/)
  assert.doesNotMatch(remoteBindingSource, /邀请开户|远程开户|门店开户注册|远程配置/)
})
