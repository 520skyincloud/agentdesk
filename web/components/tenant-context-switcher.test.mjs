import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const source = await readFile(
  new URL("./tenant-context-switcher.tsx", import.meta.url),
  "utf8"
)
const authProviderSource = await readFile(
  new URL("./auth-provider.tsx", import.meta.url),
  "utf8"
)

test("tenant context switcher uses the authenticated company identity", () => {
  assert.match(source, /session\?\.activeTenantName/)
  assert.match(source, /session\?\.activeTenantId/)
  assert.match(source, /session\?\.isPlatformAccount/)
})

test("platform switching reuses the tenant list and shared auth context", () => {
  assert.match(source, /fetchTenants\(\{ page: 1, limit: 200, status: Status\.Ok \}\)/)
  assert.match(source, /setActiveTenantId\(tenantId, tenantName\)/)
  assert.match(source, /await refreshProfile\(\{ preserveSessionOnError: true \}\)/)
  assert.match(source, /router\.push\("\/dashboard\/channels"\)/)
})

test("failed switching restores the previous company selection", () => {
  assert.match(source, /const previousTenantId = activeTenantId/)
  assert.match(source, /setActiveTenantId\(previousTenantId, previousTenantName\)/)
})

test("interactive profile refresh can preserve the current session while normal expiry still clears it", () => {
  assert.match(authProviderSource, /if \(options\.preserveSessionOnError\)/)
  assert.match(authProviderSource, /setSession\(stored\)[\s\S]*throw error/)
  assert.match(authProviderSource, /clearSession\(\)/)
})
