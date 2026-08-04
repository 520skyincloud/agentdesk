import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")
const apiSource = await readFile(
  new URL("../../../lib/api/admin.ts", import.meta.url),
  "utf8",
)

test("model profile writes require a platform account and final update permission", () => {
  assert.match(pageSource, /Boolean\(session\?\.isPlatformAccount\)/)
  assert.match(pageSource, /permissions\.has\("aiConfig\.update"\)/)
  assert.doesNotMatch(pageSource, /aiConfig\.(create|delete)/)
  assert.match(pageSource, /fetchModelProfileCatalog/)
  assert.match(pageSource, /createModelProfile/)
  assert.match(pageSource, /updateModelProfile/)
  assert.match(pageSource, /validateModelProfile/)
  assert.match(pageSource, /publishModelProfile/)
  assert.doesNotMatch(pageSource, /fetchAIConfigs|createAIConfig|updateAIConfig|deleteAIConfig/)
  assert.doesNotMatch(pageSource, /API Key|apiKey/)
})

test("model profile publication uses controlled Store evidence", () => {
  assert.match(pageSource, /catalog\?\.testRequired/)
  assert.match(pageSource, /catalog\?\.testTargets/)
  assert.match(pageSource, /真实启用槽测试/)
  assert.match(pageSource, /已有 active 凭据，但测试门店未就绪/)
  assert.match(apiSource, /\/api\/dashboard\/model-profile-template\/test/)
  assert.match(
    apiSource,
    /JSON\.stringify\(\{ id, tenantId, storeId, storeStaffBindingId \}\)/,
  )
  assert.doesNotMatch(apiSource, /validateModelProfile\([^)]*apiKey/)
})
