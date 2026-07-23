import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")

test("model profile writes require a platform account and final update permission", () => {
  assert.match(pageSource, /Boolean\(session\?\.isPlatformAccount\)/)
  assert.match(pageSource, /permissions\.has\("aiConfig\.update"\)/)
  assert.doesNotMatch(pageSource, /aiConfig\.(create|delete)/)
  assert.match(pageSource, /fetchModelProfileCatalog/)
  assert.match(pageSource, /createModelProfile/)
  assert.match(pageSource, /updateModelProfile/)
  assert.match(pageSource, /validateModelProfile/)
  assert.match(pageSource, /publishModelProfile/)
  assert.doesNotMatch(pageSource, /fetchAIConfigs|createAIConfig|updateAIConfig|deleteAIConfig/)
  assert.doesNotMatch(pageSource, /API Key|apiKey/)
})
