import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"

const bindingSource = readFileSync(
  new URL("./wxwork-protocol-binding-dialog.tsx", import.meta.url),
  "utf8"
)
const deviceLoginSource = readFileSync(
  new URL("./wxwork-protocol-device-login-dialog.tsx", import.meta.url),
  "utf8"
)
const proxyFieldSource = readFileSync(
  new URL("./wxwork-protocol-login-proxy-field.tsx", import.meta.url),
  "utf8"
)
const remoteSetupSource = readFileSync(
  new URL("../../app/wxwork-remote-setup/page.tsx", import.meta.url),
  "utf8"
)
const apiSource = readFileSync(
  new URL("../../lib/api/admin.ts", import.meta.url),
  "utf8"
)

test("all three employee login entry points collect the documented remote proxy", () => {
  for (const source of [bindingSource, deviceLoginSource, remoteSetupSource]) {
    assert.match(source, /WxWorkProtocolLoginProxyField/)
  }
  assert.match(bindingSource, /proxy: loginProxy\.trim\(\)/)
  assert.match(deviceLoginSource, /normalizedProxy \|\| undefined/)
  assert.match(remoteSetupSource, /loginProxy\.trim\(\) \|\| undefined/)
})

test("proxy field only advertises provider-documented proxy schemes", () => {
  assert.match(proxyFieldSource, /http:\/\/host:port \/ socks4:\/\/\.\.\. \/ socks5:\/\/\.\.\./)
  assert.match(proxyFieldSource, /type="password"/)
  assert.doesNotMatch(proxyFieldSource, /https:\/\//)
})

test("browser login contracts do not expose provider raw responses or keys", () => {
  assert.doesNotMatch(apiSource, /rawResponse/)
  assert.doesNotMatch(apiSource, /^\s+key: string$/m)
  assert.match(apiSource, /proxyConfigured: boolean/)
})
