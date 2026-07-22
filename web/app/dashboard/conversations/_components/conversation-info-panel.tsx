"use client";
import {
  Building2Icon,
  HistoryIcon,
  Link2Icon,
  Loader2Icon,
  MailIcon,
  PencilIcon,
  PhoneIcon,
  UserRoundIcon,
  XIcon,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";

import { type CustomerFormSavePayload } from "@/components/customer-form";
import { CustomerFormDialog } from "@/components/customer-form-dialog";
import { CustomerLinkOrCreateDialog } from "@/components/customer-link-or-create-dialog";
import { TagSelector } from "@/components/tag-selector";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Field,
  FieldContent,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Textarea } from "@/components/ui/textarea";
import {
  addCustomerTag,
  fetchCustomerTagChangeLogs,
  fetchCustomerTagOptions,
  removeCustomerTag,
  setAgentConversationAutoHandoffEnabled,
  type AgentConversation,
  type CustomerTagChangeLog,
} from "@/lib/api/agent";
import { type TagTree } from "@/lib/api/admin";
import { updateCompany, type AdminCompany } from "@/lib/api/company";
import { fetchTickets, type TicketItem } from "@/lib/api/ticket";
import {
  fetchCustomer,
  saveCustomerProfile,
  type AdminCustomer,
} from "@/lib/api/customer";
import {
  fetchCustomerContacts,
  type AdminCustomerContact,
} from "@/lib/api/customer-contact";
import {
  ContactType,
  Gender,
} from "@/lib/generated/enums";
import { useAgentConversationsStore } from "@/lib/stores/agent-conversations";
import { flattenTagTree } from "@/lib/tag-tree";
import { cn, formatDateTime, repairMojibakeText } from "@/lib/utils";
import { useI18n } from "@/i18n/provider";
import { TicketStatusBadge } from "../../tickets/_components/ticket-status-badge";

function contactTypeLabel(
  contactType: ContactType | string,
  t: (key: string, values?: Record<string, string | number>) => string
) {
  switch (contactType) {
    case ContactType.Mobile:
      return t("conversation.contactMobile");
    case ContactType.Email:
      return t("conversation.contactEmail");
    case ContactType.Other:
      return t("conversation.contactOther");
    default:
      return String(contactType);
  }
}

function ContactTypeIcon({ contactType }: { contactType: ContactType | string }) {
  const cls = "size-3.5 shrink-0 text-muted-foreground";
  switch (contactType) {
    case ContactType.Mobile:
      return <PhoneIcon className={cls} aria-hidden />;
    case ContactType.Email:
      return <MailIcon className={cls} aria-hidden />;
    default:
      return <Link2Icon className={cls} aria-hidden />;
  }
}

function DetailRow({
  label,
  value,
  valueClassName,
}: {
  label: string;
  value: string;
  valueClassName?: string;
}) {
  const empty = !value.trim();
  return (
    <div className="flex gap-2.5 text-sm leading-snug">
      <span className="w-17 shrink-0 pt-px text-xs text-muted-foreground">{label}</span>
      <span
        className={cn(
          "min-w-0 flex-1 break-all text-foreground",
          empty && "text-muted-foreground",
          valueClassName,
        )}
      >
        {empty ? "—" : value}
      </span>
    </div>
  );
}

function SectionHeading({
  children,
  action,
}: {
  children: React.ReactNode;
  action?: React.ReactNode;
}) {
  return (
    <div className="flex items-center justify-between gap-2">
      <h3 className="text-xs font-medium text-muted-foreground">{children}</h3>
      {action}
    </div>
  );
}

function UnlinkedCustomerEmpty({ conversation }: { conversation: AgentConversation }) {
  const t = useI18n();
  const [linkDialogOpen, setLinkDialogOpen] = useState(false);
  const loadConversations = useAgentConversationsStore((s) => s.loadConversations);

  return (
    <div className="space-y-6 pt-2">
      <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-[#dbe7f6] bg-[#f6f9ff] px-4 py-8 text-center shadow-inner shadow-blue-100/30">
        <UserRoundIcon className="mb-2 size-10 text-primary" aria-hidden />
        <p className="text-sm font-medium text-foreground">
          {t("conversation.unlinkedCustomerTitle")}
        </p>
        <p className="mt-1 max-w-xs text-xs leading-relaxed text-muted-foreground">
          {t("conversation.unlinkedCustomerDescription")}
        </p>
        <Button
          type="button"
          className="mt-4 gap-2"
          onClick={() => setLinkDialogOpen(true)}
        >
          <Link2Icon className="size-4" />
          {t("conversation.linkOrCreateCustomer")}
        </Button>
      </div>
      <CustomerLinkOrCreateDialog
        open={linkDialogOpen}
        onOpenChange={setLinkDialogOpen}
        conversationId={conversation.id}
        onSuccess={() => void loadConversations()}
      />
    </div>
  );
}

function MissingCustomerEmpty({ conversation }: { conversation: AgentConversation }) {
  const t = useI18n();
  const [linkDialogOpen, setLinkDialogOpen] = useState(false);
  const loadConversations = useAgentConversationsStore((s) => s.loadConversations);

  return (
    <div className="space-y-6 pt-2">
      <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-[#dbe7f6] bg-[#f6f9ff] px-4 py-8 text-center shadow-inner shadow-blue-100/30">
        <UserRoundIcon className="mb-2 size-10 text-primary" aria-hidden />
        <p className="text-sm font-medium text-foreground">
          {t("conversation.missingCustomerTitle")}
        </p>
        <p className="mt-1 max-w-xs text-xs leading-relaxed text-muted-foreground">
          {t("conversation.missingCustomerDescription")}
        </p>
        <Button
          type="button"
          className="mt-4 gap-2"
          onClick={() => setLinkDialogOpen(true)}
        >
          <Link2Icon className="size-4" />
          {t("conversation.relinkOrCreateCustomer")}
        </Button>
      </div>
      <div className="space-y-2">
        <SectionHeading>{t("conversation.conversationOwner")}</SectionHeading>
        <div className="space-y-2">
          <DetailRow label={t("conversation.channelId")} value={conversation.channelId ? `${conversation.channelId}` : "-"} />
          <DetailRow label={t("conversation.customerId")} value={conversation.customerId ? `${conversation.customerId}` : "-"} />
        </div>
      </div>
      <CustomerLinkOrCreateDialog
        open={linkDialogOpen}
        onOpenChange={setLinkDialogOpen}
        conversationId={conversation.id}
        onSuccess={() => void loadConversations()}
      />
    </div>
  );
}

type ConversationInfoPanelProps = {
  conversation: AgentConversation | null;
  className?: string;
  variant?: "default" | "embedded";
};

export function ConversationInfoPanel({
  conversation,
  className,
  variant = "default",
}: ConversationInfoPanelProps) {
  const t = useI18n();
  const embedded = variant === "embedded";

  return (
    <div
      className={cn(
        "flex h-full min-h-0 flex-col overflow-hidden",
        embedded
          ? "bg-card text-card-foreground"
          : "border-[#dbe7f6] bg-white text-card-foreground",
        className,
      )}
    >
      <div className="flex h-12.5 shrink-0 items-center border-b border-[#dbe7f6] bg-[#f8fbff] px-3">
        <h2 className="text-sm font-medium text-foreground">
          {t("conversation.conversationInfo")}
        </h2>
      </div>

      <div
        className={cn(
          "min-h-0 flex-1 overflow-y-auto px-3 pb-4",
          embedded && "pb-[max(1rem,env(safe-area-inset-bottom))] pt-1",
        )}
      >
        {!conversation ? (
          <p className="pt-4 text-sm text-muted-foreground">
            {embedded
              ? t("conversation.selectConversationForInfo")
              : t("conversation.selectSidebarConversationForInfo")}
          </p>
        ) : (
          <div className="space-y-4 py-3">
            <CustomerBody conversation={conversation} />
          </div>
        )}
      </div>
    </div>
  );
}

function CustomerTagSection({
  conversation,
}: {
  conversation: AgentConversation;
}) {
  const refreshConversationDetail = useAgentConversationsStore(
    (state) => state.refreshConversationDetail,
  );
  const [availableTags, setAvailableTags] = useState<TagTree[]>([]);
  const [pendingTagId, setPendingTagId] = useState<number | null>(null);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyLoadingMore, setHistoryLoadingMore] = useState(false);
  const [history, setHistory] = useState<CustomerTagChangeLog[]>([]);
  const [historyPage, setHistoryPage] = useState(1);
  const [historyTotal, setHistoryTotal] = useState(0);
  const [locatingMessageId, setLocatingMessageId] = useState<number | null>(null);
  const currentTags = conversation.customerTags ?? [];
  const selectedTagIds = currentTags.map((item) => item.tagId);

  useEffect(() => {
    let cancelled = false;
    void fetchCustomerTagOptions(conversation.id)
      .then((data) => {
        if (!cancelled) {
          setAvailableTags(Array.isArray(data) ? data : []);
        }
      })
      .catch((error) => {
        if (!cancelled) {
          toast.error(error instanceof Error ? error.message : "加载客户标签失败");
        }
      });
    return () => {
      cancelled = true;
    };
  }, [conversation.id]);

  useEffect(() => {
    if (!historyOpen) {
      return;
    }
    let cancelled = false;
    setHistoryLoading(true);
    setHistoryLoadingMore(false);
    setHistory([]);
    setHistoryPage(1);
    setHistoryTotal(0);
    void fetchCustomerTagChangeLogs(conversation.id)
      .then((data) => {
        if (!cancelled) {
          setHistory(Array.isArray(data.results) ? data.results : []);
          setHistoryPage(data.page?.page ?? 1);
          setHistoryTotal(data.page?.total ?? data.results.length);
        }
      })
      .catch((error) => {
        if (!cancelled) {
          toast.error(error instanceof Error ? error.message : "加载标签历史失败");
        }
      })
      .finally(() => {
        if (!cancelled) {
          setHistoryLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [conversation.id, historyOpen]);

  async function loadMoreHistory() {
    if (historyLoadingMore || history.length >= historyTotal) {
      return;
    }
    setHistoryLoadingMore(true);
    try {
      const data = await fetchCustomerTagChangeLogs(conversation.id, historyPage + 1);
      const incoming = Array.isArray(data.results) ? data.results : [];
      setHistory((current) => {
        const existing = new Set(current.map((item) => item.id));
        return [...current, ...incoming.filter((item) => !existing.has(item.id))];
      });
      setHistoryPage(data.page?.page ?? historyPage + 1);
      setHistoryTotal(data.page?.total ?? historyTotal);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载更多标签历史失败");
    } finally {
      setHistoryLoadingMore(false);
    }
  }

  async function locateEvidenceMessage(messageId: number) {
    if (locatingMessageId !== null) {
      return;
    }
    setLocatingMessageId(messageId);
    try {
      let target = document.getElementById(`message-${messageId}`);
      while (!target) {
        const state = useAgentConversationsStore.getState();
        if (state.selectedConversationId !== conversation.id) {
          toast.error("当前会话已切换，无法定位证据消息");
          return;
        }
        if (state.messages.some((message) => message.id === messageId)) {
          for (let attempt = 0; attempt < 3 && !target; attempt += 1) {
            await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
            target = document.getElementById(`message-${messageId}`);
          }
          break;
        }
        if (!state.messagesHasMore) {
          break;
        }
        const previousCursor = state.messagesCursor;
        const previousCount = state.messages.length;
        await state.loadOlderMessages();
        await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
        const next = useAgentConversationsStore.getState();
        if (next.messagesCursor === previousCursor && next.messages.length === previousCount) {
          break;
        }
        target = document.getElementById(`message-${messageId}`);
      }
      if (!target) {
        toast.error("未在当前会话历史中找到该证据消息");
        return;
      }
      setHistoryOpen(false);
      requestAnimationFrame(() => {
        target?.scrollIntoView({ behavior: "smooth", block: "center" });
        target?.classList.add("ring-2", "ring-primary", "ring-offset-2");
        window.setTimeout(() => {
          target?.classList.remove("ring-2", "ring-primary", "ring-offset-2");
        }, 1800);
      });
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载证据消息失败");
    } finally {
      setLocatingMessageId(null);
    }
  }

  async function updateTags(nextTagIds: number[]) {
    if (pendingTagId !== null) {
      return;
    }
    const current = new Set(selectedTagIds);
    const tagId =
      nextTagIds.find((id) => !current.has(id)) ??
      selectedTagIds.find((id) => !nextTagIds.includes(id));
    if (!tagId) {
      return;
    }
    const removing = current.has(tagId);
    const flatOptions = flattenTagTree(availableTags);
    const nextTag = flatOptions.find((item) => item.id === tagId);
    const replacesConflict = Boolean(
      nextTag?.conflictGroup &&
      currentTags.some((item) => {
        const currentOption = flatOptions.find((option) => option.id === item.tagId);
        return currentOption?.conflictGroup === nextTag.conflictGroup;
      })
    );
    if (!removing && selectedTagIds.length >= 20 && !replacesConflict) {
      toast.error("每位客户最多保留 20 个有效标签");
      return;
    }
    setPendingTagId(tagId);
    try {
      if (removing) {
        await removeCustomerTag({ conversationId: conversation.id, tagId });
      } else {
        await addCustomerTag({ conversationId: conversation.id, tagId });
      }
      await refreshConversationDetail(conversation.id);
      toast.success(removing ? "已移除客户标签" : "已添加客户标签");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "更新客户标签失败");
    } finally {
      setPendingTagId(null);
    }
  }

  return (
    <section className="space-y-2 border-t pt-2">
      <SectionHeading
        action={
          <div className="flex items-center gap-1">
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              title="来源历史"
              onClick={() => setHistoryOpen(true)}
            >
              <HistoryIcon />
            </Button>
            <TagSelector
              mode="multiple"
              value={selectedTagIds}
              onChange={(value) => void updateTags(value)}
              tags={availableTags}
              pendingTagId={pendingTagId}
              selectLeavesOnly
              placeholder="编辑"
              triggerText="编辑"
              searchPlaceholder="搜索客户标签"
              loadingText="加载标签中..."
              emptyText="暂无可用标签"
              align="end"
              showSelectedBadges={false}
              triggerVariant="ghost"
              triggerSize="sm"
              triggerClassName="h-7 w-auto shrink-0 justify-start gap-1 px-2 text-xs"
              contentClassName="w-72"
            />
          </div>
        }
      >
        客户标签
      </SectionHeading>
      {currentTags.length > 0 ? (
        <div className="flex flex-wrap gap-1.5">
          {currentTags.map((item) => (
            <span
              key={item.tagId}
              className="inline-flex h-7 items-center gap-1 rounded-md border border-[#dbe7f6] bg-[#f6f9ff] px-2 text-xs text-foreground"
              title={item.source === "manual" ? "人工确认" : "24 小时知识进化"}
            >
              {item.name}
              <span className="text-[10px] text-muted-foreground">
                {item.source === "manual" ? "人工" : "AI"}
              </span>
              <button
                type="button"
                className="flex size-4 items-center justify-center text-muted-foreground hover:text-destructive"
                disabled={pendingTagId !== null}
                aria-label={`移除客户标签 ${item.name}`}
                onClick={() =>
                  void updateTags(selectedTagIds.filter((id) => id !== item.tagId))
                }
              >
                <XIcon className="size-3" />
              </button>
            </span>
          ))}
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">暂无客户标签</p>
      )}
      <Sheet open={historyOpen} onOpenChange={setHistoryOpen}>
        <SheetContent className="w-full sm:max-w-md">
          <SheetHeader className="border-b">
            <SheetTitle>客户标签来源历史</SheetTitle>
          </SheetHeader>
          <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
            {historyLoading ? (
              <p className="py-10 text-center text-muted-foreground">加载中...</p>
            ) : history.length === 0 ? (
              <p className="py-10 text-center text-muted-foreground">暂无标签历史</p>
            ) : (
              <div className="divide-y">
                {history.map((item) => (
                  <div key={item.id} className="space-y-2 py-3">
                    <div className="flex items-center justify-between gap-2">
                      <span className="font-medium">{customerTagChangeLabel(item)}</span>
                      <span className="text-xs text-muted-foreground">{formatDateTime(item.createdAt)}</span>
                    </div>
                    <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                      <span>{item.source === "manual" ? "人工" : item.source === "ai" ? "AI" : "系统"}</span>
                      {item.operatorName ? <span>· {item.operatorName}</span> : null}
                      {item.confidence > 0 ? <span>· 置信度 {Math.round(item.confidence * 100)}%</span> : null}
                    </div>
                    {(item.evidenceMessageIds ?? []).length > 0 ? (
                      <div className="flex flex-wrap gap-1.5">
                        {(item.evidenceMessageIds ?? []).map((messageId) => (
                          <Button
                            key={messageId}
                            type="button"
                            variant="outline"
                            size="xs"
                            disabled={locatingMessageId !== null}
                            onClick={() => void locateEvidenceMessage(messageId)}
                          >
                            {locatingMessageId === messageId ? (
                              <Loader2Icon className="animate-spin" />
                            ) : null}
                            消息 #{messageId}
                          </Button>
                        ))}
                      </div>
                    ) : null}
                  </div>
                ))}
                {history.length < historyTotal ? (
                  <div className="flex justify-center py-4">
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={historyLoadingMore}
                      onClick={() => void loadMoreHistory()}
                    >
                      {historyLoadingMore ? <Loader2Icon className="animate-spin" /> : null}
                      加载更多
                    </Button>
                  </div>
                ) : null}
              </div>
            )}
          </div>
        </SheetContent>
      </Sheet>
    </section>
  );
}

function customerTagChangeLabel(item: CustomerTagChangeLog) {
  switch (item.action) {
    case "add":
      return `新增 ${item.newTagName || `标签 ${item.newTagId}`}`;
    case "refresh":
      return `刷新 ${item.newTagName || item.oldTagName || `标签 ${item.newTagId || item.oldTagId}`}`;
    case "replace":
      return `${item.oldTagName || `标签 ${item.oldTagId}`} → ${item.newTagName || `标签 ${item.newTagId}`}`;
    case "remove":
      return `移除 ${item.oldTagName || `标签 ${item.oldTagId}`}`;
    default:
      return item.action;
  }
}

function CustomerBody({ conversation }: { conversation: AgentConversation }) {
  const customerId = conversation.customerId ?? 0;

  if (customerId <= 0) {
    return (
      <div className="space-y-4">
        <SmartReplySection conversation={conversation} />
        <UnlinkedCustomerEmpty conversation={conversation} />
      </div>
    );
  }

  return <CustomerLinkedBody conversation={conversation} customerId={customerId} />;
}

type CustomerLinkedBodyProps = {
  conversation: AgentConversation;
  customerId: number;
};

function CustomerLinkedBody({ conversation, customerId }: CustomerLinkedBodyProps) {
  const t = useI18n();
  const [loading, setLoading] = useState(true);
  const [customer, setCustomer] = useState<AdminCustomer | null>(null);
  const [contacts, setContacts] = useState<AdminCustomerContact[]>([]);

  const [customerEditOpen, setCustomerEditOpen] = useState(false);
  const [customerEditSaving, setCustomerEditSaving] = useState(false);
  const [companyEditOpen, setCompanyEditOpen] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const c = await fetchCustomer(customerId);
      setCustomer(c);
      if (!c) {
        setContacts([]);
        return;
      }
      const list = await fetchCustomerContacts(customerId);
      setContacts(Array.isArray(list) ? list : []);
    } catch (e) {
      const msg = e instanceof Error ? e.message : t("conversation.loadCustomerFailed");
      toast.error(msg);
      setCustomer(null);
      setContacts([]);
    } finally {
      setLoading(false);
    }
  }, [customerId, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const isProfileEmpty =
    customer &&
    !customer.name.trim() &&
    !customer.primaryMobile.trim() &&
    !customer.primaryEmail.trim() &&
    customer.companyId === 0 &&
    !customer.remark.trim();

  if (loading && !customer) {
    return (
      <p className="pt-4 text-sm text-muted-foreground">{t("conversation.loadingCustomer")}</p>
    );
  }

  if (!customer) {
    return (
      <div className="space-y-4">
        <MissingCustomerEmpty conversation={conversation} />
      </div>
    );
  }

  const displayName = customer.name.trim() || t("conversation.unnamedCustomer");
  const company = customer.company ?? null;
  const genderLabel =
    customer.gender === Gender.Male
      ? t("conversation.genderMale")
      : customer.gender === Gender.Female
        ? t("conversation.genderFemale")
      : null;

  return (
    <div className="space-y-4">
      <SmartReplySection conversation={conversation} />
      {isProfileEmpty ? (
        <div className="rounded-xl border border-amber-200 bg-amber-50 px-3 py-2.5 text-xs leading-relaxed text-amber-950 shadow-[0_8px_18px_rgba(245,158,11,0.08)] dark:text-amber-100">
          {t("conversation.customerProfileEmpty")}
        </div>
      ) : null}

      <section className="space-y-2">
        <div className="flex items-start justify-between gap-2">
          <div className="flex min-w-0 flex-1 items-start gap-2 text-sm">
            <UserRoundIcon
              className="mt-0.5 size-4 shrink-0 text-muted-foreground"
              aria-hidden
            />
            <div className="min-w-0 flex-1 space-y-0.5">
              <p className="line-clamp-2 leading-snug text-foreground">
                <span className="font-medium">{displayName}</span>
                {genderLabel ? (
                  <span className="font-normal text-muted-foreground">
                    {" "}
                    · {genderLabel}
                  </span>
                ) : null}
              </p>
            </div>
          </div>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-7 shrink-0 gap-1 px-2 text-xs"
            onClick={() => setCustomerEditOpen(true)}
          >
            <PencilIcon className="size-3.5" />
            {t("conversation.edit")}
          </Button>
        </div>

        <div className="space-y-2">
          <DetailRow
            label={t("conversation.lastActive")}
            value={
              customer.lastActiveAt ? formatDateTime(customer.lastActiveAt) : ""
            }
          />
          <DetailRow
            label={t("conversation.remark")}
            value={customer.remark.trim() ? customer.remark : ""}
            valueClassName="whitespace-pre-wrap"
          />
          <DetailRow
            label={t("conversation.createdAt")}
            value={formatDateTime(customer.createdAt)}
            valueClassName="whitespace-pre-wrap"
          />
          <DetailRow
            label={t("conversation.updatedAt")}
            value={formatDateTime(customer.updatedAt)}
            valueClassName="whitespace-pre-wrap"
          />
        </div>
      </section>

      <section className="space-y-2">
        {contacts.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("conversation.noContacts")}</p>
        ) : (
          <ul className="space-y-3">
            {contacts.map((row) => {
              const tags: string[] = [];
              if (row.isPrimary) {
                tags.push(t("conversation.primaryContactBadge"));
              }
              if (row.isVerified) {
                tags.push(t("conversation.verifiedContactBadge"));
              }
              return (
                <li key={row.id} className="text-sm">
                  <div className="flex items-center gap-2">
                    <ContactTypeIcon contactType={row.contactType} />
                    <div className="min-w-0 flex-1">
                      <p className="break-all font-medium leading-snug text-foreground">
                        {row.contactValue}
                        <span className="ml-2 text-xs font-normal text-muted-foreground">
                          {contactTypeLabel(row.contactType, t)}
                        </span>
                        {tags.length > 0 ? (
                          <span className="ml-2 text-xs text-muted-foreground">
                            {tags.join(" · ")}
                          </span>
                        ) : null}
                      </p>
                      {row.remark ? (
                        <p className="mt-1 line-clamp-3 break-all text-xs leading-relaxed text-muted-foreground">
                          {row.remark}
                        </p>
                      ) : null}
                    </div>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </section>

      {customer.companyId > 0 ? (
        <section className="border-t pt-2">
          {company ? (
            <div className="space-y-2">
              <div className="flex items-start justify-between gap-2">
                <div className="flex min-w-0 flex-1 items-start gap-2 text-sm">
                  <Building2Icon
                    className="mt-0.5 size-4 shrink-0 text-muted-foreground"
                    aria-hidden
                  />
                  <div className="min-w-0 flex-1 space-y-0.5">
                    <p className="line-clamp-2 font-medium leading-snug text-foreground">
                      {company.name}
                    </p>
                    {company.code ? (
                      <p className="font-mono text-xs text-muted-foreground">
                        {company.code}
                      </p>
                    ) : null}
                  </div>
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-7 shrink-0 gap-1 px-2 text-xs"
                  onClick={() => setCompanyEditOpen(true)}
                >
                  <PencilIcon className="size-3.5" />
                  {t("conversation.edit")}
                </Button>
              </div>
              <div className="space-y-2 pt-1">
                <DetailRow
                  label={t("conversation.createdAt")}
                  value={formatDateTime(company.createdAt)}
                />
                <DetailRow
                  label={t("conversation.updatedAt")}
                  value={formatDateTime(company.updatedAt)}
                />
              </div>
              <DetailRow
                label={t("conversation.remark")}
                value={company.remark.trim() ? company.remark : ""}
                valueClassName="whitespace-pre-wrap"
              />
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">
              {t("conversation.companyUnavailable")}
            </p>
          )}
        </section>
      ) : null}

      <RelatedTicketsSection conversation={conversation} />

      <CustomerTagSection conversation={conversation} />


      <CustomerFormDialog
        open={customerEditOpen}
        onOpenChange={setCustomerEditOpen}
        saving={customerEditSaving}
        itemId={customer.id}
        onSave={async (payload: CustomerFormSavePayload) => {
          if (customerEditSaving) {
            return;
          }
          setCustomerEditSaving(true);
          try {
            await saveCustomerProfile({ ...payload, id: customer.id });
            toast.success(t("conversation.saved"));
            void load();
            setCustomerEditOpen(false);
          } catch (e) {
            toast.error(e instanceof Error ? e.message : t("conversation.saveFailed"));
          } finally {
            setCustomerEditSaving(false);
          }
        }}
      />
      {company ? (
        <CompanyEditDialog
          open={companyEditOpen}
          onOpenChange={setCompanyEditOpen}
          company={company}
          onSaved={() => {
            void load();
          }}
        />
      ) : null}
    </div>
  );
}

function SmartReplySection({ conversation }: { conversation: AgentConversation }) {
  const loadConversations = useAgentConversationsStore((state) => state.loadConversations);
  const [savingAutoHandoff, setSavingAutoHandoff] = useState(false);
  const aiServing = !conversation.routeStatus || conversation.routeStatus === "AI_SERVING";
  const hasAccountScopedCustomer = Boolean(conversation.customerId && conversation.wxWorkInstanceId);
  const autoHandoffEnabled = conversation.autoHandoffEnabled !== false;
  const manualAttention = conversation.manualAttention;
  const manualStatus =
    manualAttention && manualAttention.level !== "none"
      ? manualAttention.label
      : conversation.needHumanFollowUp
        ? "待人工跟进"
        : "无待处理请求";
  const manualExpireAt = manualAttention?.expiresAt || conversation.manualExpireAt || "";
  const manualExpireText =
    manualExpireAt
      ? `${formatDateTime(manualExpireAt)} 前无新人工动作将恢复AI`
      : manualAttention?.level === "serving"
        ? "默认10分钟无新客户消息恢复AI"
        : "-";

  const toggleAutoHandoff = async (enabled: boolean) => {
    if (!hasAccountScopedCustomer || savingAutoHandoff) {
      return;
    }
    setSavingAutoHandoff(true);
    try {
      await setAgentConversationAutoHandoffEnabled(conversation.id, enabled);
      await loadConversations();
      toast.success(enabled ? "已允许该客户自动转人工" : "已禁止该客户自动转人工");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "自动转人工设置失败");
    } finally {
      setSavingAutoHandoff(false);
    }
  };

  return (
    <section className="agentdesk-subtle-surface space-y-2 rounded-xl p-3">
      <SectionHeading>智能回复设置</SectionHeading>
      <div className="space-y-2">
        <DetailRow label="当前状态" value={conversation.routeStatusLabel || conversation.routeStatus || "AI接待中"} />
        <DetailRow label="员工号" value={repairMojibakeText(conversation.wxWorkEmployeeName) || conversation.wxWorkEmployeeUserId || ""} />
        <DetailRow label="门店" value={repairMojibakeText(conversation.storeName) || (conversation.storeId ? `${conversation.storeId}` : "")} />
        <DetailRow label="AI托管" value={aiServing ? "已开启" : "人工接待中，AI停答"} />
        <DetailRow label="转人工" value={manualStatus} />
        <DetailRow label="人工超时" value={manualExpireText} />
        {hasAccountScopedCustomer ? (
          <div className="flex items-center justify-between gap-3 border-t border-[#e5edf7] pt-2">
            <div className="min-w-0">
              <p className="text-sm text-foreground">自动转人工</p>
              <p className="mt-0.5 text-xs text-muted-foreground">仅当前企微员工号</p>
            </div>
            <Switch
              checked={autoHandoffEnabled}
              disabled={savingAutoHandoff}
              onCheckedChange={toggleAutoHandoff}
              aria-label="切换该客户自动转人工"
            />
          </div>
        ) : null}
      </div>
    </section>
  );
}

function RelatedTicketsSection({ conversation }: { conversation: AgentConversation }) {
  const t = useI18n();
  const [tickets, setTickets] = useState<TicketItem[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let cancelled = false;
    async function loadTickets() {
      setLoading(true);
      try {
        const data = await fetchTickets({
          conversationId: conversation.id,
          page: 1,
          limit: 5,
        });
        if (!cancelled) {
          setTickets(Array.isArray(data.results) ? data.results : []);
        }
      } catch (error) {
        if (!cancelled) {
          toast.error(error instanceof Error ? error.message : t("conversation.loadTicketsFailed"));
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    }
    void loadTickets();
    return () => {
      cancelled = true;
    };
  }, [conversation.id, t]);

  return (
    <section className="space-y-2 border-t pt-2">
      <SectionHeading>{t("conversation.relatedTickets")}</SectionHeading>
      {loading ? (
        <p className="text-sm text-muted-foreground">{t("conversation.loadingTickets")}</p>
      ) : tickets.length > 0 ? (
        <div className="space-y-2">
          {tickets.map((ticket) => (
            <div
              key={ticket.id}
              className="rounded-xl border border-[#dbe7f6] bg-white px-3 py-2 shadow-[0_8px_18px_rgba(37,99,235,0.06)]"
            >
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-medium text-foreground">
                    {ticket.title}
                  </div>
                  <div className="mt-0.5 text-xs text-muted-foreground">
                    {ticket.ticketNo}
                  </div>
                </div>
                <TicketStatusBadge status={ticket.status} />
              </div>
              <div className="mt-2 flex items-center justify-between gap-3">
                <span className="text-xs text-muted-foreground">
                  {ticket.updatedAt ? formatDateTime(ticket.updatedAt) : "—"}
                </span>
              </div>
            </div>
          ))}
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">{t("conversation.noRelatedTickets")}</p>
      )}
    </section>
  );
}

type CompanyEditDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  company: AdminCompany;
  onSaved: () => void;
};

function CompanyEditDialog({
  open,
  onOpenChange,
  company,
  onSaved,
}: CompanyEditDialogProps) {
  const t = useI18n();
  const [name, setName] = useState("");
  const [code, setCode] = useState("");
  const [remark, setRemark] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) {
      return;
    }
    setName(company.name);
    setCode(company.code);
    setRemark(company.remark);
  }, [open, company]);

  const handleSubmit = async () => {
    const trimmedName = name.trim();
    if (!trimmedName) {
      toast.error(t("conversation.companyNameRequired"));
      return;
    }
    setSaving(true);
    try {
      await updateCompany({
        id: company.id,
        name: trimmedName,
        code: code.trim(),
        intentProfileId: company.intentProfileId || 0,
        remark: remark.trim(),
      });
      toast.success(t("conversation.saved"));
      onSaved();
      onOpenChange(false);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t("conversation.saveFailed"));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md" showCloseButton>
        <DialogHeader>
          <DialogTitle>{t("conversation.editCompany")}</DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-4 py-1">
          <Field orientation="vertical">
            <FieldLabel htmlFor="co-name">{t("conversation.companyName")}</FieldLabel>
            <FieldContent>
              <Input id="co-name" value={name} onChange={(e) => setName(e.target.value)} />
            </FieldContent>
          </Field>
          <Field orientation="vertical">
            <FieldLabel htmlFor="co-code">{t("conversation.companyCode")}</FieldLabel>
            <FieldContent>
              <Input id="co-code" value={code} onChange={(e) => setCode(e.target.value)} />
            </FieldContent>
          </Field>
          <Field orientation="vertical">
            <FieldLabel htmlFor="co-remark">{t("conversation.remark")}</FieldLabel>
            <FieldContent>
              <Textarea
                id="co-remark"
                value={remark}
                onChange={(e) => setRemark(e.target.value)}
                rows={3}
              />
            </FieldContent>
          </Field>
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            {t("conversation.cancel")}
          </Button>
          <Button type="button" disabled={saving} onClick={() => void handleSubmit()}>
            {saving ? t("conversation.saving") : t("conversation.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
