export type OrderedMessageIdentity = {
  id: number
  conversationId: number
}

export type ReadableCurrentMessage = OrderedMessageIdentity & {
  historicalOnly?: boolean
}

export function mergeMessagesPreservingOrder<T extends OrderedMessageIdentity>(
  existing: T[],
  incoming: T[],
  placement: "prepend" | "append",
  merge: (current: T, incoming: T) => T,
): T[] {
  const keyOf = (item: OrderedMessageIdentity) => `${item.conversationId}:${item.id}`
  const byKey = new Map<string, T>()
  const existingKeys: string[] = []
  const incomingNewKeys: string[] = []

  for (const item of existing) {
    const key = keyOf(item)
    if (!byKey.has(key)) {
      existingKeys.push(key)
      byKey.set(key, item)
      continue
    }
    byKey.set(key, merge(byKey.get(key)!, item))
  }
  for (const item of incoming) {
    const key = keyOf(item)
    const current = byKey.get(key)
    if (current) {
      byKey.set(key, merge(current, item))
      continue
    }
    incomingNewKeys.push(key)
    byKey.set(key, item)
  }

  const keys = placement === "prepend"
    ? [...incomingNewKeys, ...existingKeys]
    : [...existingKeys, ...incomingNewKeys]
  return keys.map((key) => byKey.get(key)!)
}

export function findCurrentConversationReadTarget<T extends ReadableCurrentMessage>(
  messages: T[],
  conversationId: number,
): T | undefined {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const item = messages[index]
    if (item.conversationId === conversationId && !item.historicalOnly) {
      return item
    }
  }
  return undefined
}
