import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"

const componentSource = readFileSync(
  new URL("./wxwork-protocol-device-login-dialog.tsx", import.meta.url),
  "utf8"
)
const managerSource = readFileSync(
  new URL("./wxwork-protocol-instance-manager.tsx", import.meta.url),
  "utf8"
)

test("existing offline instances expose the device login flow", () => {
  assert.match(managerSource, /key: "deviceLogin"/)
  assert.match(managerSource, /label: "扫码重新登录"/)
  assert.match(managerSource, /item\.healthStatus !== "online"/)
  assert.match(managerSource, /item\.loginAvailable !== false/)
  assert.match(managerSource, /!item\.protocolExpired/)
  assert.match(managerSource, /<WxWorkProtocolDeviceLoginDialog/)
})

test("expired instances cannot start the device login flow", () => {
  assert.match(componentSource, /instance\?\.protocolExpired/)
  assert.match(componentSource, /instance\?\.loginAvailable === false/)
  assert.match(componentSource, /instance\.loginUnavailableReason/)
  assert.match(componentSource, /请先续费或更换有效实例/)
  assert.match(managerSource, /实例已过期/)
})

test("device login polls protocol status and handles verification code", () => {
  assert.match(componentSource, /checkWxWorkProtocolLoginQrcode/)
  assert.match(componentSource, /window\.setInterval\(\(\) => void check\(\), 3000\)/)
  assert.match(componentSource, /status\?\.requiresCode/)
  assert.match(componentSource, /verifyWxWorkProtocolLogin/)
  assert.match(componentSource, /placeholder="请输入验证码"/)
  assert.match(componentSource, /syncWxWorkProtocolProfile/)
})

test("offline device login submits the documented proxy before requesting a qrcode", () => {
  assert.match(componentSource, /WxWorkProtocolLoginProxyField/)
  assert.match(componentSource, /instance\?\.proxyConfigured/)
  assert.match(componentSource, /getWxWorkProtocolLoginQrcode\(\s*instanceId,\s*normalizedProxy \|\| undefined\s*\)/)
  assert.match(componentSource, /正在设置代理并启动登录环境/)
  assert.doesNotMatch(componentSource, /recoverWxWorkProtocolInstance/)
  assert.doesNotMatch(componentSource, /loginRuntimeMaxAttempts/)
})

test("device login reuses the selected instance instead of creating an identity", () => {
  assert.match(componentSource, /getWxWorkProtocolLoginQrcode/)
  assert.doesNotMatch(componentSource, /startWxWorkProtocolLogin/)
  assert.doesNotMatch(componentSource, /createWxWorkProtocolRemoteSetup/)
})
