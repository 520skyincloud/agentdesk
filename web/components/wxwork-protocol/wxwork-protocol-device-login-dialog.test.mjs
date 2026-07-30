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

test("offline device login starts the documented runtime before requesting a qrcode", () => {
  assert.match(componentSource, /recoverWxWorkProtocolInstance\(instanceId\)/)
  assert.match(componentSource, /getWxWorkProtocolLoginQrcode\(instanceId\)/)
  assert.match(componentSource, /loginRuntimeMaxAttempts = 10/)
  assert.match(componentSource, /loginRuntimeRetryIntervalMs = 3000/)
  assert.match(componentSource, /登录环境正在启动，稍后自动重试/)
  assert.match(componentSource, /异地登录器在线后重试/)
})

test("device login reuses the selected instance instead of creating an identity", () => {
  assert.match(componentSource, /getWxWorkProtocolLoginQrcode\(instanceId\)/)
  assert.doesNotMatch(componentSource, /startWxWorkProtocolLogin/)
  assert.doesNotMatch(componentSource, /createWxWorkProtocolRemoteSetup/)
})
