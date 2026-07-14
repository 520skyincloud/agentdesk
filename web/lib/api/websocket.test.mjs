import assert from "node:assert/strict"
import test from "node:test"

import { createAuthenticatedWebSocketUrl } from "./websocket.ts"

test("authenticated dashboard websocket URL carries the active tenant", () => {
  const url = createAuthenticatedWebSocketUrl(
    "/api/ws/dashboard",
    "access token",
    101
  )

  assert.equal(
    url,
    "/api/ws/dashboard?accessToken=access+token&tenantId=101"
  )
})

test("authenticated websocket URL omits an empty tenant context", () => {
  const url = createAuthenticatedWebSocketUrl(
    "/api/ws/dashboard/notification",
    "token"
  )

  assert.equal(
    url,
    "/api/ws/dashboard/notification?accessToken=token"
  )
})
