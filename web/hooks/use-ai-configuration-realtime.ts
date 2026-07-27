"use client"

import { useEffect, useRef } from "react"

import { useAuth } from "@/components/auth-provider"
import { createConfigurationWebSocketUrl } from "@/lib/api/admin"
import { createRealtimeConnectionManager } from "@/lib/realtime-connection"

export const aiConfigurationEventTypes = [
  "store_model_profile.changed",
  "store_model_credential.changed",
  "fastgpt_profile.changed",
] as const

export type AIConfigurationEventType = (typeof aiConfigurationEventTypes)[number]

export type AIConfigurationChangedEvent = {
  type: AIConfigurationEventType
  tenantId: number
  storeId: number
  profileId: number
  revision: number
  status: string
  updatedAt: string
}

type RealtimeEnvelope = {
  eventId?: string
  type?: string
  data?: Partial<Omit<AIConfigurationChangedEvent, "type">>
}

const knownEventTypes = new Set<string>(aiConfigurationEventTypes)

export function useAIConfigurationRealtime(
  onChanged: (event: AIConfigurationChangedEvent) => void,
  enabled = true,
) {
  const { session } = useAuth()
  const onChangedRef = useRef(onChanged)

  useEffect(() => {
    onChangedRef.current = onChanged
  }, [onChanged])

  useEffect(() => {
    if (!enabled || !session?.user.id) return

    const connection = createRealtimeConnectionManager({
      createSocket: () => new WebSocket(createConfigurationWebSocketUrl()),
      canReconnect: () => enabled,
      onMessage: (message, socket) => {
        try {
          const envelope = JSON.parse(message.data) as RealtimeEnvelope
          const eventType = envelope.type ?? ""
          if (!knownEventTypes.has(eventType)) return
          if (envelope.eventId && socket.readyState === WebSocket.OPEN) {
            socket.send(JSON.stringify({ type: "ack", eventId: envelope.eventId }))
          }
          const payload = envelope.data ?? {}
          onChangedRef.current({
            type: eventType as AIConfigurationEventType,
            tenantId: Number(payload.tenantId) || 0,
            storeId: Number(payload.storeId) || 0,
            profileId: Number(payload.profileId) || 0,
            revision: Number(payload.revision) || 0,
            status: String(payload.status ?? ""),
            updatedAt: String(payload.updatedAt ?? ""),
          })
        } catch {
          // Ignore malformed third-party or stale WebSocket payloads.
        }
      },
    })
    connection.connect()
    return () => connection.disconnect()
  }, [enabled, session?.activeTenantId, session?.user.id])
}
