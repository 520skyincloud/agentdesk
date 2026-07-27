import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const source = await readFile(new URL("./layout.tsx", import.meta.url), "utf8")

test("dashboard layout blocks direct routes with the shared navigation permission contract", () => {
  assert.match(source, /dashboardPathIsAccessible/)
  assert.match(source, /firstAccessibleDashboardPath/)
  assert.match(source, /router\.replace\(fallbackPath \?\? "\/dashboard"\)/)
  assert.match(source, /missingTenantContext \|\| inaccessibleRoute/)
})

test("dashboard state is remounted when the authenticated tenant context changes", () => {
  assert.match(source, /const tenantContextKey = session/)
  assert.match(source, /`\$\{session\.user\.id\}:\$\{session\.activeTenantId\}`/)
  assert.match(source, /<NotificationProvider key=\{tenantContextKey\}>/)
})
