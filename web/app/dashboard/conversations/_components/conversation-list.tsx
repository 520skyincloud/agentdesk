"use client"

import { CircleAlert, UserIcon } from "lucide-react";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { ScrollArea } from "@/components/ui/scroll-area";
import { IMConversationStatus } from "@/lib/generated/enums";
import { useAgentConversationsStore } from "@/lib/stores/agent-conversations";
import { formatDateTime, repairMojibakeText } from "@/lib/utils";
import { useI18n } from "@/i18n/provider";

type ConversationListProps = {
  onAfterSelect?: () => void
}

export function ConversationList({ onAfterSelect }: ConversationListProps) {
  const t = useI18n()
  const conversations = useAgentConversationsStore((state) => state.conversations)
  const loading = useAgentConversationsStore((state) => state.conversationsLoading)
  const selectedId = useAgentConversationsStore((state) => state.selectedConversationId)
  const selectConversation = useAgentConversationsStore((state) => state.selectConversation)

  return (
    <ScrollArea className="overflow-auto bg-white/95">
      {loading ? (
        <div className="m-3 rounded-lg border border-dashed border-[#d9e2f2] bg-[#f7f9fd] p-6 text-center text-sm text-[#7a8599]">
          {t("conversation.loading")}
        </div>
      ) : conversations.length > 0 ? (
        conversations.map((conversation) => {
          const isSelected = selectedId === conversation.id
          const isHandoffPending =
            conversation.routeStatus === "HQ_AGENTDESK_PENDING" ||
            conversation.needHumanFollowUp
          const isHandoffServing =
            conversation.routeStatus === "HQ_AGENTDESK_SERVING"
          return (
            <div
              key={conversation.id}
              className={`mx-3 mb-1.5 cursor-pointer rounded-xl px-3 py-2.5 transition ${
                isSelected
                  ? "bg-[#eef3ff] shadow-[inset_0_0_0_1px_rgba(79,117,255,0.12)]"
                  : "hover:bg-[#f7f9fd]"
              }`}
              onClick={() => {
                void selectConversation(conversation.id).then(
                  () => {
                    onAfterSelect?.()
                  },
                  () => {},
                )
              }}
            >
              <div className="overflow-hidden">
                <div className="flex items-start gap-2.5">
                  <Avatar className="size-10 shrink-0 rounded-xl">
                    <AvatarImage src={conversation.customerAvatar || ""} />
                    <AvatarFallback className="rounded-xl bg-[#f0f4fb] text-[#526072]">
                      <UserIcon className="size-3.5 text-[#526072]" />
                    </AvatarFallback>
                  </Avatar>
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-1.5">
                      {isHandoffPending ? (
                        <CircleAlert className="size-3.5 shrink-0 text-destructive" />
                      ) : null}
                      <span className="min-w-0 flex-1 truncate text-[13px] font-semibold leading-4 text-[#1f2937]">
                        {repairMojibakeText(conversation.customerName) ||
                          t("conversation.customerFallback", {
                            id: conversation.customerId || conversation.id,
                          })}
                      </span>
                      <span className="shrink-0 text-[11px] text-[#9aa4b2]">
                        {conversation.lastMessageAt
                          ? formatDateTime(conversation.lastMessageAt).slice(5, 16)
                          : ""}
                      </span>
                      {conversation.agentUnreadCount > 0 ? (
                        <div className="flex size-4.5 shrink-0 items-center justify-center rounded-full bg-[#3366ff] text-[10px] text-white">
                          {conversation.agentUnreadCount > 99
                            ? "99+"
                            : conversation.agentUnreadCount}
                        </div>
                      ) : null}
                    </div>
                    <div className="mt-1 truncate text-[12px] leading-4 text-[#7a8599]">
                      {repairMojibakeText(conversation.lastMessageSummary) || t("conversation.noLatestMessage")}
                    </div>
                  </div>
                </div>
                {conversation.status === IMConversationStatus.Pending &&
                conversation.currentTeamName ? (
                  <div className="mt-1 flex items-center gap-1 pl-11 text-[10px] text-[#7a8599]">
                    <span className="rounded-md border border-[#d9e2f2] bg-[#f7f9fd] px-1.5 py-0.5">
                      {t("conversation.teamOnDuty", {
                        name: repairMojibakeText(conversation.currentTeamName),
                      })}
                    </span>
                  </div>
                ) : null}
                {(conversation.storeName || conversation.wxWorkEmployeeName || isHandoffPending || isHandoffServing) ? (
                  <div className="mt-1 flex flex-wrap items-center gap-1 pl-11 text-[10px]">
                    {conversation.storeName || conversation.wxWorkEmployeeName ? (
                      <span className="rounded-md border border-[#d9e2f2] bg-[#f7f9fd] px-1.5 py-0.5 text-[#7a8599]">
                        {repairMojibakeText(conversation.storeName) || t("conversation.storeUnknown")}
                        {conversation.wxWorkEmployeeName
                          ? ` / ${repairMojibakeText(conversation.wxWorkEmployeeName)}`
                          : ""}
                      </span>
                    ) : null}
                    {isHandoffPending ? (
                      <span className="rounded-md border border-destructive/15 bg-destructive/10 px-1.5 py-0.5 text-destructive">
                        {t("conversation.manualHandoffPending")}
                      </span>
                    ) : null}
                    {isHandoffServing ? (
                      <span className="rounded-md border border-emerald-200 bg-emerald-50 px-1.5 py-0.5 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300">
                        {t("conversation.manualHandoffServing")}
                      </span>
                    ) : null}
                  </div>
                ) : null}
              </div>
            </div>
          )
        })
      ) : (
        <div className="m-3 rounded-lg border border-dashed border-[#d9e2f2] bg-[#f7f9fd] p-6 text-center text-sm text-[#7a8599]">
          {t("conversation.empty")}
        </div>
      )}
    </ScrollArea>
  )
}
