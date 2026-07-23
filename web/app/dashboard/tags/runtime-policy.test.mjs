import assert from "node:assert/strict"
import { access, readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")
const policySource = await readFile(
  new URL("./_components/runtime-policy.tsx", import.meta.url),
  "utf8",
)
const apiSource = await readFile(
  new URL("../../../lib/api/admin.ts", import.meta.url),
  "utf8",
)
const navigationSource = await readFile(
  new URL("../../../lib/navigation.tsx", import.meta.url),
  "utf8",
)

test("customer tag runtime policy reuses the existing tag page and permissions", () => {
  assert.match(pageSource, /permissions\.has\("tag\.update"\)/)
  assert.match(pageSource, /TabsTrigger value="catalog"/)
  assert.match(pageSource, /TabsTrigger value="runtime"/)
  assert.match(pageSource, /CustomerTagRuntimePolicyPanel canUpdate=\{canUpdate\}/)
  assert.match(policySource, /\{canUpdate \? \(/)
  assert.match(policySource, /disabled=\{!canUpdate \|\| actionLoading\}/)
})

test("runtime controls use one API for individual, selected, and all-store updates", () => {
  assert.match(policySource, /runToggle\("evolution", checked, false, \[item\.storeId\]\)/)
  assert.match(policySource, /runToggle\("reply", checked, false, \[item\.storeId\]\)/)
  assert.match(policySource, /Array\.from\(selectedIds\)/)
  assert.match(policySource, /runToggle\(action\.feature, action\.enabled, true, \[\]\)/)
  assert.match(apiSource, /\/api\/dashboard\/customer-tag\/runtime\/batch_toggle/)
  assert.match(apiSource, /\/api\/dashboard\/customer-tag\/policy\/update/)
})

test("runtime batch menus keep Base UI labels inside menu groups", () => {
  assert.match(policySource, /DropdownMenuGroup/)
  assert.doesNotMatch(
    policySource,
    /<DropdownMenuContent[^>]*>\s*<DropdownMenuLabel>/,
  )
})

test("runtime policy does not add a parallel page or navigation entry", async () => {
  assert.doesNotMatch(navigationSource, /\/dashboard\/customer-tag|\/dashboard\/tag-runtime/)
  await assert.rejects(access(new URL("../customer-tag/page.tsx", import.meta.url)))
  await assert.rejects(access(new URL("../tag-runtime/page.tsx", import.meta.url)))
})
