import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const settingsPageUrl = new URL("./page.tsx", import.meta.url)
const editDialogUrl = new URL("./_components/channel-edit.tsx", import.meta.url)
const platformPageUrl = new URL("../channels/page.tsx", import.meta.url)
const navigationUrl = new URL("../../../lib/navigation.tsx", import.meta.url)

test("platform company access and tenant channel settings remain separate pages", async () => {
  const [settingsSource, platformSource] = await Promise.all([
    readFile(settingsPageUrl, "utf8"),
    readFile(platformPageUrl, "utf8"),
  ])

  assert.match(platformSource, /fetchTenants/)
  assert.match(platformSource, /createTenant/)
  assert.doesNotMatch(platformSource, /fetchChannels|createChannel|updateChannel/)
  assert.match(settingsSource, /fetchChannels/)
  assert.match(settingsSource, /createChannel/)
  assert.match(settingsSource, /updateChannel/)
  assert.match(settingsSource, /deleteChannel/)
  assert.doesNotMatch(settingsSource, /fetchTenants|createTenant/)
})

test("channel actions follow the existing visible channel permissions", async () => {
  const source = await readFile(settingsPageUrl, "utf8")

  assert.match(source, /permissions\.has\("channel\.view"\)/)
  assert.match(source, /permissions\.has\("channel\.create"\)/)
  assert.match(source, /permissions\.has\("channel\.update"\)/)
  assert.match(source, /permissions\.has\("channel\.delete"\)/)
  assert.match(source, /showEdit=\{\(item\) => canUpdate/)
  assert.match(source, /deleteItem=\{canDelete/)
  assert.match(source, /toggle: canUpdate/)
  assert.match(source, /disabled: \(item\) => !isEditableChannel\(item\)/)
})

test("tenant navigation exposes channel settings only with tenant context and view permission", async () => {
  const source = await readFile(navigationUrl, "utf8")

  assert.match(
    source,
    /titleKey: "nav\.channelSettings",[\s\S]*?url: "\/dashboard\/settings",[\s\S]*?requiredPermission: "channel\.view"/
  )
  assert.match(
    source,
    /titleKey: "nav\.serviceCapabilities",[\s\S]*?context: "tenant",[\s\S]*?url: "\/dashboard\/settings"/
  )
})

test("channel editor supports current channels without reviving legacy channel forms", async () => {
  const source = await readFile(editDialogUrl, "utf8")

  assert.match(source, /"web" \| "wechat_mp" \| "wxwork_protocol"/)
  assert.match(source, /fetchAIAgentsAll\(\{ status: Status\.Ok \}\)/)
  assert.match(source, /fetchChannel\(itemId\)/)
  assert.doesNotMatch(source, /WxWorkKFAccount|fetchWxWorkKFAccounts/)
  assert.doesNotMatch(source, /wecom-cli|wxwork_cli|bridgeToken/)
})
