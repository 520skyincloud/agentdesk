import assert from "node:assert/strict"
import test from "node:test"
import ts from "typescript"
import { readFile } from "node:fs/promises"
import vm from "node:vm"

async function loadConfig() {
  const source = await readFile(new URL("./config.ts", import.meta.url), "utf8")
  const compiled = ts.transpileModule(source, {
    compilerOptions: {
      target: ts.ScriptTarget.ES2017,
      module: ts.ModuleKind.CommonJS,
    },
    fileName: "config.ts",
  })
  const sandbox = {
    exports: {},
    module: { exports: {} },
  }
  sandbox.exports = sandbox.module.exports
  vm.runInNewContext(compiled.outputText, sandbox)
  return sandbox.module.exports
}

test("normalizes supported locale aliases", async () => {
  const { DEFAULT_LOCALE, normalizeLocale } = await loadConfig()

  assert.equal(normalizeLocale("zh-CN"), "zh-CN")
  assert.equal(normalizeLocale("zh_CN"), "zh-CN")
  assert.equal(normalizeLocale("zh"), "zh-CN")
  assert.equal(normalizeLocale("en-US"), "en-US")
  assert.equal(normalizeLocale("en_US"), "en-US")
  assert.equal(normalizeLocale("en"), "en-US")
  assert.equal(normalizeLocale("fr-FR"), DEFAULT_LOCALE)
})

test("resolves browser locale from stored value before navigator languages", async () => {
  const { resolveBrowserLocale } = await loadConfig()

  assert.equal(
    resolveBrowserLocale({
      storedLocale: "en-US",
      navigatorLanguages: ["zh-CN"],
    }),
    "en-US"
  )
})

test("falls back through navigator languages", async () => {
  const { resolveBrowserLocale } = await loadConfig()

  assert.equal(
    resolveBrowserLocale({
      storedLocale: "",
      navigatorLanguages: ["fr-FR", "en"],
    }),
    "en-US"
  )
})
