type LocalizableNotification = {
  title: string
  content: string
  notificationType: string
}

const TICKET_ASSIGNED_TITLE = "\u5de5\u5355\u6307\u6d3e\u63d0\u9192"
const CONVERSATION_TRANSFERRED_TITLE = "\u4f1a\u8bdd\u8f6c\u63a5\u63d0\u9192"
const CONVERSATION_AUTO_ASSIGNED_TITLE = "\u4f1a\u8bdd\u81ea\u52a8\u5206\u914d\u63d0\u9192"
const CONVERSATION_ASSIGNED_TITLE = "\u4f1a\u8bdd\u5206\u914d\u63d0\u9192"

const TICKET_ASSIGNED_PATTERN = /^\u5de5\u5355 (.+) \u5df2\u6307\u6d3e\u7ed9\u4f60$/
const CONVERSATION_ASSIGNED_PATTERN = /^\u4f1a\u8bdd #([0-9]+) \u5df2\u5206\u914d\u7ed9\u4f60$/
const ASSIGNMENT_REASON_PREFIX = "\u6307\u6d3e\u539f\u56e0: "
const CONVERSATION_ASSIGNMENT_REASON_PREFIX = "\u5206\u914d\u539f\u56e0: "
const TRANSFER_REASON_PREFIX = "\u8f6c\u63a5\u539f\u56e0: "

export function localizeNotificationItem<T extends LocalizableNotification>(
  notification: T,
  locale: string
): T {
  if (locale !== "en-US") {
    return notification
  }
  if (notification.notificationType === "ticket_assigned") {
    return {
      ...notification,
      title: localizeNotificationTitle(notification.title),
      content: localizeTicketAssignedContent(notification.content),
    }
  }
  if (notification.notificationType === "conversation_assigned") {
    return {
      ...notification,
      title: localizeNotificationTitle(notification.title),
      content: localizeConversationAssignedContent(notification.content),
    }
  }
  return notification
}

function localizeTicketAssignedContent(content: string) {
  const lines = splitNotificationLines(content)
  if (lines.length === 0) {
    return content
  }
  const match = lines[0].match(TICKET_ASSIGNED_PATTERN)
  if (match?.[1]) {
    lines[0] = `Ticket ${match[1]} has been assigned to you.`
  }
  return lines
    .map((line) =>
      line.startsWith(ASSIGNMENT_REASON_PREFIX)
        ? `Assignment reason: ${line.slice(ASSIGNMENT_REASON_PREFIX.length)}`
        : line
    )
    .join("\n")
}

function localizeConversationAssignedContent(content: string) {
  const lines = splitNotificationLines(content)
  if (lines.length === 0) {
    return content
  }
  const match = lines[0].match(CONVERSATION_ASSIGNED_PATTERN)
  if (match?.[1]) {
    lines[0] = `Conversation #${match[1]} has been assigned to you.`
  }
  return lines
    .map((line) => {
      if (line.startsWith(CONVERSATION_ASSIGNMENT_REASON_PREFIX)) {
        return `Assignment reason: ${line.slice(CONVERSATION_ASSIGNMENT_REASON_PREFIX.length)}`
      }
      if (line.startsWith(TRANSFER_REASON_PREFIX)) {
        return `Transfer reason: ${line.slice(TRANSFER_REASON_PREFIX.length)}`
      }
      return line
    })
    .join("\n")
}

function localizeNotificationTitle(title: string) {
  switch (title.trim()) {
    case TICKET_ASSIGNED_TITLE:
      return "Ticket assigned"
    case CONVERSATION_TRANSFERRED_TITLE:
      return "Conversation transferred"
    case CONVERSATION_AUTO_ASSIGNED_TITLE:
      return "Conversation auto-assigned"
    case CONVERSATION_ASSIGNED_TITLE:
      return "Conversation assigned"
    default:
      return title
  }
}

function splitNotificationLines(content: string) {
  const normalized = content.trim()
  if (!normalized) {
    return []
  }
  return normalized.split("\n")
}
