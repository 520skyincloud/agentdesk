"use client"

import { useEffect, useRef, useState } from "react"

import { createAdminWebSocketUrl } from "@/lib/api/admin"
import {
  createRealtimeConnectionManager,
  type RealtimeConnectionStatus,
} from "@/lib/realtime-connection"

type StoreCredentialChangedPayload = {
  storeId: number
  credentialRevision: number
  status: string
  changedAt: string
}

type StoreProfileChangedPayload = {
  storeId: number
  profileRevision: number
  status: string
  changedAt: string
}

type RealtimeEnvelope = {
  eventId?: string
  type?: string
  data?: StoreCredentialChangedPayload & StoreProfileChangedPayload
}

export function useStoreModelCredentialRealtime(
  storeId: number,
  onChanged: (payload: StoreCredentialChangedPayload) => void,
  onConnected?: () => void,
  onProfileChanged?: (payload: StoreProfileChangedPayload) => void,
) {
  const [status, setStatus] = useState<RealtimeConnectionStatus>("disconnected")
  const onChangedRef = useRef(onChanged)
  const onConnectedRef = useRef(onConnected)
  const onProfileChangedRef = useRef(onProfileChanged)

  useEffect(() => {
    onChangedRef.current = onChanged
  }, [onChanged])

  useEffect(() => {
    onConnectedRef.current = onConnected
  }, [onConnected])

  useEffect(() => {
    onProfileChangedRef.current = onProfileChanged
  }, [onProfileChanged])

  useEffect(() => {
    if (storeId <= 0) {
      return
    }
    const realtime = createRealtimeConnectionManager({
      createSocket: () => new WebSocket(createAdminWebSocketUrl()),
      onStatusChange: setStatus,
      onOpen: (socket) => {
        socket.send(JSON.stringify({
          type: "subscribe",
          topics: [`store:${storeId}`],
        }))
        onConnectedRef.current?.()
      },
      onMessage: (event, socket) => {
        try {
          const envelope = JSON.parse(event.data) as RealtimeEnvelope
          const eventType = envelope.type || ""
          if (
            eventType === "" ||
            eventType === "connected" ||
            eventType === "pong" ||
            eventType === "subscribed" ||
            eventType === "unsubscribed"
          ) {
            return
          }
          const eventId = envelope.eventId?.trim()
          if (eventId && socket.readyState === WebSocket.OPEN) {
            socket.send(JSON.stringify({ type: "ack", eventId }))
          }
          if (
            eventType === "store_model_credential.changed" &&
            envelope.data?.storeId === storeId
          ) {
            onChangedRef.current(envelope.data)
          } else if (
            eventType === "store_model_profile.changed" &&
            envelope.data?.storeId === storeId
          ) {
            onProfileChangedRef.current?.(envelope.data)
          }
        } catch {
          // Ignore malformed realtime envelopes.
        }
      },
    })
    realtime.connect()
    return () => realtime.disconnect()
  }, [storeId])

  return status
}
