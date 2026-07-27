import assert from "node:assert/strict"
import fs from "node:fs"
import test from "node:test"

const componentSource = fs.readFileSync(
  new URL("./store-model-credential.tsx", import.meta.url),
  "utf8",
)
const apiSource = fs.readFileSync(
  new URL("../lib/api/store-model-credential.ts", import.meta.url),
  "utf8",
)
const tenantSource = fs.readFileSync(
  new URL("../app/dashboard/channels/_components/model-access.tsx", import.meta.url),
  "utf8",
)
const usersSource = fs.readFileSync(
  new URL("../app/dashboard/users/page.tsx", import.meta.url),
  "utf8",
)
const workbenchSource = fs.readFileSync(
  new URL("../app/dashboard/store-workbench/page.tsx", import.meta.url),
  "utf8",
)

test("manager and store staff reuse one credential component", () => {
  assert.match(tenantSource, /StoreModelCredentialDialog/)
  assert.match(usersSource, /StoreModelCredentialDialog/)
  assert.match(workbenchSource, /StoreModelCredentialPanel mode="self"/)
  assert.match(componentSource, /mode: CredentialMode/)
})

test("credential API uses manager and self-service contracts", () => {
  for (const endpoint of [
    "/api/dashboard/store-model-credential/get",
    "/api/dashboard/store-model-credential/update",
    "/api/dashboard/store-model-credential/approve",
    "/api/dashboard/store-model-credential/policy",
    "/api/dashboard/store-model-profile/activate_pending",
    "/api/dashboard/store-workbench/model_credential",
    "/api/dashboard/store-workbench/model_credential/update",
  ]) {
    assert.match(apiSource, new RegExp(endpoint.replaceAll("/", "\\/")))
  }
})

test("single and batch policy updates require the current password", () => {
  assert.match(componentSource, /currentPassword,\s*confirmed: true/)
  assert.match(tenantSource, /currentPassword: policyPassword/)
  assert.match(apiSource, /currentPassword: string/)
})

test("browser response contract contains only masked credential metadata", () => {
	const credentialResponseSource = apiSource.slice(
		0,
		apiSource.indexOf("export type StoreModelCredentialAudit"),
	)
  for (const forbidden of [
    "encryptedKey",
    "keyNonce",
    "keyFingerprint:",
    "masterKeyId",
    "gatewayBaseUrl",
    "apiKey: string",
  ]) {
    assert.doesNotMatch(credentialResponseSource, new RegExp(forbidden))
  }
  assert.match(credentialResponseSource, /keyMask: string/)
  assert.match(credentialResponseSource, /fingerprintLast6: string/)
  assert.match(componentSource, /type=\{showKey \? "text" : "password"\}/)
})

test("pending model profile switch reuses the sensitive action contract", () => {
  assert.match(componentSource, /activatePendingStoreModelProfile/)
  assert.match(componentSource, /sensitivePayloadReady\(false\)/)
  assert.match(componentSource, /currentPassword,\s*confirmed/)
  assert.match(componentSource, /验证并切换待选方案/)
})

test("credential scope changes clear the previous store snapshot and sensitive form state", () => {
  for (const reset of [
    "setData(null)",
    "setAudit([])",
    'setNewKey("")',
    'setCurrentPassword("")',
    "setConfirmed(false)",
    "setShowKey(false)",
  ]) {
    assert.match(componentSource, new RegExp(reset.replace(/[()[\]]/g, "\\$&")))
  }
})
