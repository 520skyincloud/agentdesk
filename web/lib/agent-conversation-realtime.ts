import type { RealtimeConversationPatch } from "@/lib/im-realtime-state"

const listMembershipFields = new Set<string>([
  "status",
  "currentAssigneeId",
  "currentTeamId",
  "routeStatus",
  "needHumanFollowUp",
])

export function shouldReloadConversationListForRealtimePatch(
  patch: RealtimeConversationPatch | null | undefined
) {
  if (!patch) {
    return false
  }
  return Object.keys(patch).some((key) => listMembershipFields.has(key))
}
