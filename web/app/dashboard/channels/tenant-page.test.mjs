import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")
const navigationSource = await readFile(
  new URL("../../../lib/navigation.tsx", import.meta.url),
  "utf8"
)
const modelAccessSource = await readFile(
  new URL("./_components/model-access.tsx", import.meta.url),
  "utf8"
)

test("legacy channels page is replaced by tenant company management", () => {
  assert.match(pageSource, /fetchTenants/)
  assert.match(pageSource, /createTenant/)
  assert.match(pageSource, /updateTenantStatus/)
  assert.doesNotMatch(pageSource, /fetchChannels|createChannel|updateChannel/)
})

test("tenant company actions are independently permission-gated", () => {
  assert.match(pageSource, /permissions\.has\("tenant\.create"\)/)
  assert.match(pageSource, /permissions\.has\("tenant\.update"\)/)
  assert.match(pageSource, /permissions\.has\("tenant\.updateStatus"\)/)
  assert.match(pageSource, /permissions\.has\("tenant\.switch"\)/)
  assert.match(pageSource, /showEdit=\{canUpdate\}/)
  assert.match(pageSource, /showActionsColumn=\{showActionsColumn\}/)
})

test("channels route is exposed as tenant management through tenant.view", () => {
  assert.match(
    navigationSource,
    /titleKey: "nav\.channels",[\s\S]*?url: "\/dashboard\/channels",[\s\S]*?requiredPermission: "tenant\.view"/
  )
})

test("tenant switching preserves the authenticated session and restores context on failure", () => {
  assert.match(pageSource, /const previousTenantId = session\?\.activeTenantId \?\? 0/)
  assert.match(pageSource, /refreshProfile\(\{ preserveSessionOnError: true \}\)/)
  assert.match(pageSource, /setActiveTenantId\(previousTenantId, previousTenantName\)/)
})

test("tenant rows show isolated resource counts and latest activity", () => {
  assert.match(pageSource, /item\.agentCount/)
  assert.match(pageSource, /item\.storeCount/)
  assert.match(pageSource, /item\.agentTeamCount/)
  assert.match(pageSource, /formatDateTime\(item\.lastActiveAt\)/)
  assert.match(pageSource, /tenant\.columnResources/)
})

test("store profile assignment reuses the tenant action menu with final permissions", () => {
  assert.match(pageSource, /permissions\.has\("aiConfig\.view"\)/)
  assert.match(pageSource, /permissions\.has\("aiConfig\.update"\)/)
  assert.doesNotMatch(pageSource, /tenantModelGrant\./)
  assert.match(pageSource, /key: "model-access"/)
  assert.match(pageSource, /label: "门店模型指派"/)
  assert.match(pageSource, /<TenantModelAccessDialog/)
  assert.match(modelAccessSource, /fetchStoreModelProfileAssignments\(tenant\.id\)/)
  assert.match(modelAccessSource, /batchAssignStoreModelProfile/)
  assert.match(modelAccessSource, /storeIds: \[\.\.\.selectedStoreIds\]/)
  assert.match(modelAccessSource, /confirmRevision: selectedProfile\.revision/)
  assert.doesNotMatch(modelAccessSource, /fetchTenantAIModelAccess|grantedAiConfigIds|defaults: access\.usages/)
  assert.doesNotMatch(modelAccessSource, /API Key|Base URL|tenantModelGrant/)
})
