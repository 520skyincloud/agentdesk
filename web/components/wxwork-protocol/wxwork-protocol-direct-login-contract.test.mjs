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
const remoteSetupSource = readFileSync(
  new URL("../../app/wxwork-remote-setup/page.tsx", import.meta.url),
  "utf8"
)
const apiSource = readFileSync(
  new URL("../../lib/api/admin.ts", import.meta.url),
  "utf8"
)

test("all employee login entry points request qrcodes without proxy input", () => {
  for (const source of [bindingSource, deviceLoginSource, remoteSetupSource]) {
    assert.doesNotMatch(source, /WxWorkProtocolLoginProxyField/)
    assert.doesNotMatch(source, /异地登录代理/)
    assert.doesNotMatch(source, /proxyConfigured/)
  }
  assert.match(bindingSource, /startWxWorkProtocolLogin\(\{\s*channelId:/)
  assert.doesNotMatch(bindingSource, /proxy:\s*loginProxy/)
  assert.match(deviceLoginSource, /getWxWorkProtocolLoginQrcode\(instanceId\)/)
  assert.match(remoteSetupSource, /getWxWorkProtocolRemoteSetupLoginQrcode\(token\)/)
})

test("browser login api contracts only send instance identity or setup token", () => {
  assert.match(
    apiSource,
    /function getWxWorkProtocolLoginQrcode\(id: number\)[\s\S]*?JSON\.stringify\(\{ id \}\)/
  )
  assert.match(
    apiSource,
    /function getWxWorkProtocolRemoteSetupLoginQrcode\(token: string\)[\s\S]*?JSON\.stringify\(\{ token \}\)/
  )
  assert.doesNotMatch(apiSource, /proxyConfigured: boolean/)
})

test("all login entry points retain status-10 verification handling", () => {
  for (const source of [bindingSource, deviceLoginSource, remoteSetupSource]) {
    assert.match(source, /requiresCode/)
    assert.match(source, /verifyWxWorkProtocol/)
  }
  assert.match(remoteSetupSource, /window\.setInterval\(\(\) => void loadLoginStatus\(false\), 3000\)/)
})

test("browser login contracts do not expose provider raw responses or keys", () => {
  assert.doesNotMatch(apiSource, /rawResponse/)
  assert.doesNotMatch(apiSource, /^\s+key: string$/m)
})
