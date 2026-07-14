import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const registrationSource = await readFile(
  new URL("./_components/registration-form.tsx", import.meta.url),
  "utf8"
)
const userPageSource = await readFile(
  new URL("../dashboard/users/page.tsx", import.meta.url),
  "utf8"
)
const invitationSource = await readFile(
  new URL("../dashboard/users/_components/invitation-dialog.tsx", import.meta.url),
  "utf8"
)
const reviewSource = await readFile(
  new URL("../dashboard/users/_components/registration-review.tsx", import.meta.url),
  "utf8"
)

test("public registration is feature-gated and tenant invitation scoped", () => {
  assert.match(registrationSource, /fetchAuthOptions/)
  assert.match(registrationSource, /tenantRegistrationEnabled/)
  assert.match(registrationSource, /validateTenantInvitation/)
  assert.match(registrationSource, /registerTenantUser\(payload, requestIdFor\(payload\)\)/)
  assert.match(registrationSource, /invitation\.tenantShortName/)
  assert.doesNotMatch(registrationSource, /tenantId|roleIds|agentTeamId|storeId/)
})

test("account management reuses visible tenant permissions for invitation and review", () => {
  assert.match(userPageSource, /permissions\.has\("tenantInvite\.view"\)/)
  assert.match(userPageSource, /permissions\.has\("tenantInvite\.rotate"\)/)
  assert.match(userPageSource, /permissions\.has\("tenantRegistration\.view"\)/)
  assert.match(userPageSource, /permissions\.has\("tenantRegistration\.review"\)/)
  assert.match(userPageSource, /permissions\.has\("user\.assignRole"\)/)
  assert.match(userPageSource, /<RegistrationReviewPanel/)
})

test("invitation rotation warns about invalidating old links", () => {
  assert.match(invitationSource, /rotateCurrentTenantInvitation/)
  assert.match(invitationSource, /rotateDescription/)
  assert.match(invitationSource, /registrationClosedHint/)
})

test("registration approval only offers backend-assignable enabled roles", () => {
  assert.match(reviewSource, /role\.assignable && role\.status === Status\.Ok/)
  assert.match(reviewSource, /TenantRegistrationReviewDecision\.Approve/)
  assert.match(reviewSource, /selectedRoleIds\.length === 0/)
  assert.match(reviewSource, /rejectReasonRequired/)
  assert.match(reviewSource, /reviewTenantRegistration\(payload, requestIdFor\(payload\)\)/)
})

test("tenant registration messages remain aligned across locales", async () => {
  const zh = JSON.parse(
    await readFile(new URL("../../messages/zh-CN.json", import.meta.url), "utf8")
  )
  const en = JSON.parse(
    await readFile(new URL("../../messages/en-US.json", import.meta.url), "utf8")
  )

  assert.deepEqual(
    Object.keys(en.tenantRegistration).sort(),
    Object.keys(zh.tenantRegistration).sort()
  )
  assert.ok(Object.keys(zh.tenantRegistration).length >= 100)
})
