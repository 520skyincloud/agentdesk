import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const pageSource = await readFile(new URL("./page.tsx", import.meta.url), "utf8")
const providerSource = await readFile(
  new URL("../../../components/notification-provider.tsx", import.meta.url),
  "utf8",
)

test("notification read state uses update permission without blocking navigation", () => {
  assert.match(pageSource, /session\?\.permissions\.includes\("notification\.update"\)/)
  assert.match(pageSource, /!item\.readAt && canUpdate/)
  assert.match(pageSource, /renderToolbarActions=.*canUpdate \? \(/s)
  assert.match(pageSource, /router\.push\(item\.actionUrl\)/)

  assert.match(providerSource, /permissions\.includes\("notification\.view"\)/)
  assert.match(providerSource, /permissions\.includes\("notification\.update"\)/)
  assert.match(providerSource, /if \(!canView\)/)
  assert.match(providerSource, /!notification\.readAt && canUpdate/)
  assert.match(providerSource, /finally \{/)
  assert.match(providerSource, /router\.push\(notification\.actionUrl\)/)
})
