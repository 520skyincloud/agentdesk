import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")
const managerSource = await readFile(
  new URL("../../../components/wxwork-protocol/wxwork-protocol-instance-manager.tsx", import.meta.url),
  "utf8",
)
const companyDetailSource = await readFile(new URL("../company-detail/page.tsx", import.meta.url), "utf8")

test("conversation workbench preserves all conversations while gating account navigation", () => {
  assert.match(pageSource, /canViewWxWorkAccounts = permissions\.has\("channel\.view"\)/)
  assert.match(pageSource, /if \(!canViewWxWorkAccounts\) \{[\s\S]*setInstances\(\[\]\)/)
  assert.match(pageSource, /setSelectedWxWorkInstanceId\(null\)/)
  assert.match(pageSource, /if \(!canViewWxWorkAccounts\) \{[\s\S]*return conversations\.reduce/)
  assert.match(pageSource, />全部账号</)
  assert.match(pageSource, /canViewWxWorkAccounts && filteredInstances\.length === 0/)
})

test("conversation workbench separates account creation from account management", () => {
  assert.match(pageSource, /canCreateWxWorkAccounts = canViewWxWorkAccounts && permissions\.has\("channel\.create"\)/)
  assert.match(pageSource, /canUpdateWxWorkAccounts = canViewWxWorkAccounts && permissions\.has\("channel\.update"\)/)
  assert.match(pageSource, /canDeleteWxWorkAccounts = canViewWxWorkAccounts && permissions\.has\("channel\.delete"\)/)
  assert.match(pageSource, /if \(!canCreateWxWorkAccounts\) return/)
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
    "company.view",
    "aiConfig.view",
    "aiConfig.update",
  ]) {
    assert.match(managerSource, new RegExp(`permissionSet\\.has\\("${permission.replace(".", "\\.")}\"\\)`))
  }
  assert.match(managerSource, /if \(!canViewChannels\) \{[\s\S]*return null/)
  assert.match(managerSource, /canViewKnowledgeBases \? fetchKnowledgeBasesAll/)
  assert.match(managerSource, /lockCompany \|\| !canViewCompanies/)
  assert.match(managerSource, /showCreate=\{!hideCreateActions && canCreateChannels\}/)
  assert.match(managerSource, /showEdit=\{canUpdateChannels\}/)
  assert.match(managerSource, /deleteItem=\{\s*canDeleteChannels\s*\?\s*async/)
  assert.match(managerSource, /if \(canUpdateChannels\) \{[\s\S]*key: "replaceLogin"/)
  assert.match(managerSource, /open=\{canCreateChannels && createDialogOpen\}/)
})

test("company detail gates its wxwork section and remote setup action", () => {
  assert.match(companyDetailSource, /canViewWxWorkAccounts = permissionSet\.has\("channel\.view"\)/)
  assert.match(companyDetailSource, /canCreateWxWorkAccounts = canViewWxWorkAccounts && permissionSet\.has\("channel\.create"\)/)
  assert.match(companyDetailSource, /if \(!canCreateWxWorkAccounts \|\| !company\) return/)
  assert.match(companyDetailSource, /\{canCreateWxWorkAccounts \? \(/)
  assert.match(companyDetailSource, /\{canViewWxWorkAccounts \? \(/)
  assert.match(companyDetailSource, /open=\{canViewModelSettings && modelSettingsOpen\}/)
})
