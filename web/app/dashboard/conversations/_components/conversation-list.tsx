"use client"

import { UserIcon } from "lucide-react";

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
    <ScrollArea className="overflow-auto bg-background/95">
      {loading ? (
        <div className="m-3 rounded-lg border border-dashed border-border bg-muted/40 p-6 text-center text-sm text-muted-foreground">
          {t("conversation.loading")}
        </div>
      ) : conversations.length > 0 ? (
        conversations.map((conversation) => {
          const isSelected = selectedId === conversation.id
          const manualAttention = conversation.manualAttention
          const showManualDot = Boolean(manualAttention?.dot)
          return (
            <div
              key={conversation.id}
              className={`mx-3 mb-1.5 cursor-pointer rounded-lg border px-3 py-2.5 transition-colors ${
                isSelected
                  ? "border-primary/20 bg-primary/10"
                  : showManualDot
                    ? "border-amber-200/80 bg-amber-50/80 hover:bg-amber-100/70 dark:border-amber-800/50 dark:bg-amber-950/25 dark:hover:bg-amber-950/35"
                    : "border-transparent hover:bg-muted/70"
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
                  <div className="relative size-10 shrink-0">
                    <Avatar className="size-10 rounded-xl">
                      <AvatarImage src={conversation.customerAvatar || ""} />
                      <AvatarFallback className="rounded-xl bg-muted text-muted-foreground">
                        <UserIcon className="size-3.5 text-muted-foreground" />
                      </AvatarFallback>
                    </Avatar>
                    {showManualDot ? (
                      <span
                        className={`absolute -right-0.5 -top-0.5 size-3 rounded-full border-2 border-background ${
                          manualAttention?.level === "urgent"
                            ? "bg-destructive shadow-[0_0_0_3px_rgba(239,68,68,0.16)]"
                            : "bg-rose-500"
                        }`}
                      />
                    ) : null}
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-1.5">
                      <span className="min-w-0 flex-1 truncate text-[13px] font-semibold leading-4 text-foreground">
                        {repairMojibakeText(conversation.customerName) ||
                          t("conversation.customerFallback", {
                            id: conversation.customerId || conversation.id,
                          })}
                      </span>
                      <span className="shrink-0 text-[11px] text-muted-foreground">
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
                    <div className="mt-1 truncate text-[12px] leading-4 text-muted-foreground">
                      {repairMojibakeText(conversation.lastMessageSummary) || t("conversation.noLatestMessage")}
                    </div>
                  </div>
                </div>
                {conversation.status === IMConversationStatus.Pending &&
                conversation.currentTeamName ? (
                  <div className="mt-1 flex items-center gap-1 pl-11 text-[10px] text-muted-foreground">
                    <span className="rounded border border-border bg-muted px-1.5 py-0.5">
                      {t("conversation.teamOnDuty", {
                        name: repairMojibakeText(conversation.currentTeamName),
                      })}
                    </span>
                  </div>
                ) : null}
                {(conversation.storeName || conversation.wxWorkEmployeeName || (manualAttention && manualAttention.level !== "none")) ? (
                  <div className="mt-1 flex flex-wrap items-center gap-1 pl-11 text-[10px]">
                    {conversation.storeName || conversation.wxWorkEmployeeName ? (
                      <span className="rounded border border-border bg-muted px-1.5 py-0.5 text-muted-foreground">
                        {repairMojibakeText(conversation.storeName) || t("conversation.storeUnknown")}
                        {conversation.wxWorkEmployeeName
                          ? ` / ${repairMojibakeText(conversation.wxWorkEmployeeName)}`
                          : ""}
                      </span>
                    ) : null}
                    {manualAttention && manualAttention.level !== "none" ? (
                      <span
                        className={`rounded-md border px-1.5 py-0.5 ${
                          manualAttention.level === "urgent"
                            ? "border-destructive/15 bg-destructive/10 text-destructive"
                            : manualAttention.dot
                              ? "border-amber-200 bg-amber-50 text-amber-700"
                              : "border-emerald-200 bg-emerald-50 text-emerald-700"
                        }`}
                      >
                        {manualAttention.label}
                      </span>
                    ) : null}
                  </div>
                ) : null}
              </div>
            </div>
          )
        })
      ) : (
        <div className="m-3 rounded-lg border border-dashed border-border bg-muted/40 p-6 text-center text-sm text-muted-foreground">
          {t("conversation.empty")}
        </div>
      )}
    </ScrollArea>
  )
}
