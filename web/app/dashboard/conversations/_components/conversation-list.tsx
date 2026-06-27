"use client"

import { CircleAlert, UserIcon } from "lucide-react";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { ScrollArea } from "@/components/ui/scroll-area";
import { IMConversationStatus } from "@/lib/generated/enums";
import { useAgentConversationsStore } from "@/lib/stores/agent-conversations";
import { formatDateTime } from "@/lib/utils";
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
    <ScrollArea className="overflow-auto">
      {loading ? (
        <div className="p-6 text-center text-sm text-muted-foreground">
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
              className={`cursor-pointer border-b border-border/80 px-3 py-2 transition-colors hover:bg-muted/40 ${
                isSelected ? "bg-accent/70" : ""
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
                <div className="flex items-center gap-2">
                  <Avatar className="size-7 shrink-0">
                    <AvatarImage src="" />
                    <AvatarFallback className="bg-primary/10 text-primary">
                      <UserIcon className="size-3.5 text-primary" />
                    </AvatarFallback>
                  </Avatar>
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-1.5">
                      {isHandoffPending ? (
                        <CircleAlert className="size-3.5 shrink-0 text-destructive" />
                      ) : null}
                      <span className="min-w-0 flex-1 truncate font-medium text-sm leading-4">
                        {conversation.customerName ||
                          t("conversation.customerFallback", {
                            id: conversation.customerId || conversation.id,
                          })}
                      </span>
                      {conversation.agentUnreadCount > 0 ? (
                        <div className="flex size-4.5 shrink-0 items-center justify-center rounded-full bg-primary text-[10px] text-primary-foreground">
                          {conversation.agentUnreadCount > 99
                            ? "99+"
                            : conversation.agentUnreadCount}
                        </div>
                      ) : null}
                    </div>
                    <div className="mt-0.5 text-[11px] text-muted-foreground">
                      {conversation.lastMessageAt
                        ? formatDateTime(conversation.lastMessageAt)
                        : t("conversation.noTime")}
                    </div>
                  </div>
                </div>
                <div className="mt-0.5 truncate text-xs leading-4 text-muted-foreground">
                  {conversation.lastMessageSummary || t("conversation.noLatestMessage")}
                </div>
                {conversation.status === IMConversationStatus.Pending &&
                conversation.currentTeamName ? (
                  <div className="mt-1 flex items-center gap-1 text-[10px] text-muted-foreground">
                    <span className="rounded-md bg-muted px-1.5 py-0.5">
                      {t("conversation.teamOnDuty", {
                        name: conversation.currentTeamName,
                      })}
                    </span>
                  </div>
                ) : null}
                {(conversation.storeName || conversation.wxWorkEmployeeName || isHandoffPending || isHandoffServing) ? (
                  <div className="mt-1 flex flex-wrap items-center gap-1 text-[10px]">
                    {conversation.storeName || conversation.wxWorkEmployeeName ? (
                      <span className="rounded-md bg-muted px-1.5 py-0.5 text-muted-foreground">
                        {conversation.storeName || t("conversation.storeUnknown")}
                        {conversation.wxWorkEmployeeName
                          ? ` / ${conversation.wxWorkEmployeeName}`
                          : ""}
                      </span>
                    ) : null}
                    {isHandoffPending ? (
                      <span className="rounded-md bg-destructive/10 px-1.5 py-0.5 text-destructive">
                        {t("conversation.manualHandoffPending")}
                      </span>
                    ) : null}
                    {isHandoffServing ? (
                      <span className="rounded-md bg-emerald-50 px-1.5 py-0.5 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300">
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
        <div className="p-6 text-center text-sm text-muted-foreground">
          {t("conversation.empty")}
        </div>
      )}
    </ScrollArea>
  )
}
