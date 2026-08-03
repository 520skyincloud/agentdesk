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
const deviceLoginActionSource = managerSource.slice(
  managerSource.indexOf('key: "deviceLogin"'),
  managerSource.indexOf('key: "replaceLogin"')
)
const continueReplacementActionSource = managerSource.slice(
  managerSource.indexOf('key: "continueReplacement"'),
  managerSource.indexOf('key: "deviceLogin"')
)

test("valid online and offline instances expose the device login flow", () => {
  assert.match(deviceLoginActionSource, /key: "deviceLogin"/)
  assert.match(deviceLoginActionSource, /label: "扫码重新登录"/)
  assert.doesNotMatch(deviceLoginActionSource, /healthStatus/)
  assert.match(deviceLoginActionSource, /item\.status !== Status\.Deleted/)
  assert.match(deviceLoginActionSource, /!isPendingReplacement\(item\)/)
  assert.match(deviceLoginActionSource, /item\.loginAvailable !== false/)
  assert.match(deviceLoginActionSource, /!item\.protocolExpired/)
  assert.match(deviceLoginActionSource, /Boolean\(item\.guid\.trim\(\)\)/)
  assert.match(managerSource, /<WxWorkProtocolDeviceLoginDialog/)
})

test("pending replacement drafts resume verification instead of ordinary relogin", () => {
  assert.match(continueReplacementActionSource, /label: "继续完成更换"/)
  assert.match(continueReplacementActionSource, /isPendingReplacement\(item\)/)
  assert.match(managerSource, /window\.open\(url, "_blank", "noopener,noreferrer"\)/)
  assert.match(managerSource, /尚未完成邮箱验证，不接管消息/)
  assert.match(componentSource, /扫码登录完成，请继续完成更换验证/)
  assert.match(componentSource, /继续完成更换/)
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

test("device login directly requests the documented qrcode endpoint", () => {
  assert.match(componentSource, /getWxWorkProtocolLoginQrcode\(instanceId\)/)
  assert.match(componentSource, /正在获取企微员工号登录二维码/)
  assert.doesNotMatch(componentSource, /WxWorkProtocolLoginProxyField/)
  assert.doesNotMatch(componentSource, /proxyConfigured/)
  assert.doesNotMatch(componentSource, /recoverWxWorkProtocolInstance/)
  assert.doesNotMatch(componentSource, /loginRuntimeMaxAttempts/)
})

test("device login reuses the selected instance instead of creating an identity", () => {
  assert.match(componentSource, /getWxWorkProtocolLoginQrcode/)
  assert.doesNotMatch(componentSource, /startWxWorkProtocolLogin/)
  assert.doesNotMatch(componentSource, /createWxWorkProtocolRemoteSetup/)
})
