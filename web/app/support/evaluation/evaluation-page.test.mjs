import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const formSource = await readFile(new URL("./_components/evaluation-form.tsx", import.meta.url), "utf8")

test("public evaluation renders its bundled logo without the unavailable image optimizer", () => {
  assert.match(formSource, /src="\/images\/zhixi-weibao-logo\.png"/)
  assert.match(formSource, /className="size-9 object-contain" unoptimized/)
})
