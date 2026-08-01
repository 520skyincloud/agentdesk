"use client";
import {
  HistoryIcon,
  Link2Icon,
  MailIcon,
  MessagesSquareIcon,
  PencilIcon,
  PhoneIcon,
  UserRoundIcon,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";

import { type CustomerFormSavePayload } from "@/components/customer-form";
import { CustomerFormDialog } from "@/components/customer-form-dialog";
import { CustomerLinkOrCreateDialog } from "@/components/customer-link-or-create-dialog";
import { CustomerTagHistoryDialog } from "@/components/customer-tag-history-dialog";
import { CustomerTagBadges } from "@/components/customer-tag-badges";
import { useAuth } from "@/components/auth-provider";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import {
  fetchCustomerTagOptions,
  setAgentConversationAutoHandoffEnabled,
  type AgentConversation,
} from "@/lib/api/agent";
import { type Tag } from "@/lib/api/admin";
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
import { cn, formatDateTime, repairMojibakeText } from "@/lib/utils";
import { useI18n } from "@/i18n/provider";
import {
  CustomerTagPicker,
} from "./customer-tag-picker";
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

function useCanLinkConversationCustomer() {
  const { session } = useAuth();
  const permissions = new Set(session?.permissions ?? []);
  return permissions.has("conversation.linkCustomer")
    && (permissions.has("customer.view") || permissions.has("customer.create"));
}

type ConversationInfoPermissions = {
  canViewCustomer: boolean;
  canUpdateCustomer: boolean;
  canViewTickets: boolean;
  canViewTags: boolean;
  canManageTags: boolean;
};

function useConversationInfoPermissions(): ConversationInfoPermissions {
  const { session } = useAuth();
  const permissions = new Set(session?.permissions ?? []);
  const canViewCustomer = permissions.has("customer.view");
  const canViewTags = permissions.has("tag.view");

  return {
    canViewCustomer,
    canUpdateCustomer: canViewCustomer && permissions.has("customer.update"),
    canViewTickets: permissions.has("ticket.view"),
    canViewTags,
    canManageTags: canViewTags && permissions.has("conversation.tag"),
  };
}

function UnlinkedCustomerEmpty({ conversation }: { conversation: AgentConversation }) {
  const t = useI18n();
  const canLinkCustomer = useCanLinkConversationCustomer();
  const [linkDialogOpen, setLinkDialogOpen] = useState(false);
  const loadConversations = useAgentConversationsStore((s) => s.loadConversations);

  return (
    <div className="space-y-6 pt-2">
      <div className="flex flex-col items-center justify-center rounded-lg border border-dashed border-border bg-muted/40 px-4 py-8 text-center">
        <UserRoundIcon className="mb-2 size-10 text-primary" aria-hidden />
        <p className="text-sm font-medium text-foreground">
          {t("conversation.unlinkedCustomerTitle")}
        </p>
        <p className="mt-1 max-w-xs text-xs leading-relaxed text-muted-foreground">
          {t("conversation.unlinkedCustomerDescription")}
        </p>
        {canLinkCustomer ? (
          <Button
            type="button"
            className="mt-4 gap-2"
            onClick={() => setLinkDialogOpen(true)}
          >
            <Link2Icon className="size-4" />
            {t("conversation.linkOrCreateCustomer")}
          </Button>
        ) : null}
      </div>
      <CustomerLinkOrCreateDialog
        open={canLinkCustomer && linkDialogOpen}
        onOpenChange={setLinkDialogOpen}
        conversationId={conversation.id}
        onSuccess={() => void loadConversations()}
      />
    </div>
  );
}

function MissingCustomerEmpty({ conversation }: { conversation: AgentConversation }) {
  const t = useI18n();
  const canLinkCustomer = useCanLinkConversationCustomer();
  const [linkDialogOpen, setLinkDialogOpen] = useState(false);
  const loadConversations = useAgentConversationsStore((s) => s.loadConversations);

  return (
    <div className="space-y-6 pt-2">
      <div className="flex flex-col items-center justify-center rounded-lg border border-dashed border-border bg-muted/40 px-4 py-8 text-center">
        <UserRoundIcon className="mb-2 size-10 text-primary" aria-hidden />
        <p className="text-sm font-medium text-foreground">
          {t("conversation.missingCustomerTitle")}
        </p>
        <p className="mt-1 max-w-xs text-xs leading-relaxed text-muted-foreground">
          {t("conversation.missingCustomerDescription")}
        </p>
        {canLinkCustomer ? (
          <Button
            type="button"
            className="mt-4 gap-2"
            onClick={() => setLinkDialogOpen(true)}
          >
            <Link2Icon className="size-4" />
            {t("conversation.relinkOrCreateCustomer")}
          </Button>
        ) : null}
      </div>
      <div className="space-y-2">
        <SectionHeading>{t("conversation.conversationOwner")}</SectionHeading>
        <div className="space-y-2">
          <DetailRow label={t("conversation.channelId")} value={conversation.channelId ? `${conversation.channelId}` : "-"} />
          <DetailRow label={t("conversation.customerId")} value={conversation.customerId ? `${conversation.customerId}` : "-"} />
        </div>
      </div>
      <CustomerLinkOrCreateDialog
        open={canLinkCustomer && linkDialogOpen}
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
  const permissions = useConversationInfoPermissions();
  const embedded = variant === "embedded";

  return (
    <div
      className={cn(
        "flex h-full min-h-0 flex-col overflow-hidden",
        embedded
          ? "bg-card text-card-foreground"
          : "border-border bg-card text-card-foreground",
        className,
      )}
    >
      <div className="flex h-12.5 shrink-0 items-center border-b border-border bg-muted/40 px-3">
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
            <CustomerBody conversation={conversation} permissions={permissions} />
            <ConversationContinuitySection conversation={conversation} />
          </div>
        )}
      </div>
    </div>
  );
}

function ConversationContinuitySection({
  conversation,
}: {
  conversation: AgentConversation;
}) {
  const selectConversation = useAgentConversationsStore(
    (state) => state.selectConversation,
  );
  const sessions = conversation.channelSessions ?? [];
  const related = conversation.relatedConversations ?? [];
  const continuityLinks = conversation.continuityLinks ?? [];

  if (sessions.length === 0 && related.length === 0 && continuityLinks.length === 0) {
    return null;
  }

  return (
    <div className="space-y-4 border-t pt-3">
      {continuityLinks.length > 0 ? (
        <section className="space-y-2">
          <SectionHeading>会话继承</SectionHeading>
          <div className="divide-y rounded-md border border-border">
            {continuityLinks.map((link) => {
              const isPredecessor = link.predecessorConversationId === conversation.id;
              const targetConversationId = isPredecessor
                ? link.successorConversationId
                : link.predecessorConversationId;
              return (
                <button
                  key={`${link.predecessorConversationId}-${link.successorConversationId}`}
                  type="button"
                  className="flex w-full items-start gap-2.5 px-2.5 py-2 text-left hover:bg-muted/50"
                  onClick={() => void selectConversation(targetConversationId)}
                >
                  <Link2Icon className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 flex-1">
                    <span className="block text-xs font-medium text-foreground">
                      {isPredecessor ? "已由后续会话继续接待" : "查看继承的历史会话"}
                    </span>
                    <span className="mt-0.5 block truncate text-[11px] text-muted-foreground">
                      {repairMojibakeText(link.reason) || `会话 #${targetConversationId}`}
                    </span>
                    <span className="mt-0.5 block text-[11px] text-muted-foreground">
                      {formatDateTime(link.createdAt)}
                    </span>
                  </span>
                </button>
              );
            })}
          </div>
        </section>
      ) : null}

      {sessions.length > 0 ? (
        <section className="space-y-2">
          <SectionHeading>服务轮次</SectionHeading>
          <div className="space-y-2">
            {sessions.map((session) => (
              <div
                key={session.sessionNo}
                className="flex items-start gap-2.5 rounded-md border border-border px-2.5 py-2"
              >
                <HistoryIcon className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center justify-between gap-2 text-xs">
                    <span className="font-medium text-foreground">
                      第 {session.sessionNo} 段
                    </span>
                    {session.sessionNo === conversation.currentSessionNo ? (
                      <span className="text-primary">当前</span>
                    ) : null}
                  </div>
                  <p className="mt-0.5 truncate text-xs text-muted-foreground">
                    {[
                      repairMojibakeText(session.storeStaffDisplayName),
                      repairMojibakeText(session.wxWorkEmployeeDisplayName),
                    ]
                      .filter(Boolean)
                      .join(" · ") || "历史服务账号"}
                  </p>
                  <p className="mt-0.5 text-[11px] text-muted-foreground">
                    {formatDateTime(session.startedAt)}
                    {session.endedAt ? ` - ${formatDateTime(session.endedAt)}` : ""}
                  </p>
                </div>
              </div>
            ))}
          </div>
        </section>
      ) : null}

      {related.length > 0 ? (
        <section className="space-y-2">
          <SectionHeading>同门店关联会话</SectionHeading>
          <div className="divide-y rounded-md border border-border">
            {related.map((item) => (
              <button
                key={item.id}
                type="button"
                className="flex w-full items-start gap-2.5 px-2.5 py-2 text-left hover:bg-muted/50"
                onClick={() => void selectConversation(item.id)}
              >
                <MessagesSquareIcon className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-xs font-medium text-foreground">
                    {repairMojibakeText(item.wxWorkEmployeeName) ||
                      `门店员工号会话 #${item.id}`}
                  </span>
                  <span className="mt-0.5 block truncate text-[11px] text-muted-foreground">
                    {repairMojibakeText(item.lastMessageSummary) || "暂无消息摘要"}
                  </span>
                  <span className="mt-0.5 block text-[11px] text-muted-foreground">
                    {formatDateTime(item.lastActiveAt || item.lastMessageAt)}
                  </span>
                </span>
              </button>
            ))}
          </div>
        </section>
      ) : null}
    </div>
  );
}

function CustomerTagSection({
  conversation,
  permissions,
}: {
  conversation: AgentConversation;
  permissions: ConversationInfoPermissions;
}) {
  const t = useI18n();
  const refreshConversation = useAgentConversationsStore(
    (state) => state.refreshConversation,
  );
  const [availableTags, setAvailableTags] = useState<Tag[]>([]);
  const [loading, setLoading] = useState(false);
  const canManageCurrentStoreCustomer = permissions.canManageTags
    && (conversation.customerId ?? 0) > 0
    && (conversation.storeId ?? 0) > 0;

  useEffect(() => {
    if (!canManageCurrentStoreCustomer) {
      setAvailableTags([]);
      setLoading(false);
      return;
    }

    let cancelled = false;

    async function loadTags() {
      setLoading(true);
      try {
        const data = await fetchCustomerTagOptions(conversation.id);
        if (!cancelled) {
          setAvailableTags(Array.isArray(data) ? data : []);
        }
      } catch (error) {
        if (!cancelled) {
          toast.error(error instanceof Error ? error.message : t("conversation.loadTagsFailed"));
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    }

    void loadTags();

    return () => {
      cancelled = true;
    };
  }, [canManageCurrentStoreCustomer, conversation.id, t]);

  return (
    <section className="space-y-2 border-t pt-2">
      <SectionHeading
        action={
          canManageCurrentStoreCustomer ? (
            <div className="flex items-center gap-1">
              <CustomerTagHistoryDialog conversationId={conversation.id} />
              <CustomerTagPicker
                conversation={conversation}
                availableTags={availableTags}
                loading={loading}
                onTagsChanged={() => refreshConversation(conversation.id)}
              />
            </div>
          ) : undefined
        }
      >
        {t("conversation.customerTags")}
      </SectionHeading>
      <CustomerTagBadges tags={conversation.customerTags} />
      {!conversation.customerTags || conversation.customerTags.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("conversation.noCustomerTags")}</p>
      ) : null}
    </section>
  );
}

function CustomerBody({
  conversation,
  permissions,
}: {
  conversation: AgentConversation;
  permissions: ConversationInfoPermissions;
}) {
  const customerId = conversation.customerId ?? 0;

  if (customerId <= 0) {
    return (
      <div className="space-y-4">
        <SmartReplySection conversation={conversation} />
        <UnlinkedCustomerEmpty conversation={conversation} />
        <CustomerTagSection conversation={conversation} permissions={permissions} />
      </div>
    );
  }

  if (!permissions.canViewCustomer) {
    return (
      <div className="space-y-4">
        <SmartReplySection conversation={conversation} />
        <CustomerTagSection conversation={conversation} permissions={permissions} />
      </div>
    );
  }

  return (
    <CustomerLinkedBody
      conversation={conversation}
      customerId={customerId}
      permissions={permissions}
    />
  );
}

type CustomerLinkedBodyProps = {
  conversation: AgentConversation;
  customerId: number;
  permissions: ConversationInfoPermissions;
};

function CustomerLinkedBody({
  conversation,
  customerId,
  permissions,
}: CustomerLinkedBodyProps) {
  const t = useI18n();
  const [loading, setLoading] = useState(true);
  const [customer, setCustomer] = useState<AdminCustomer | null>(null);
  const [contacts, setContacts] = useState<AdminCustomerContact[]>([]);

  const [customerEditOpen, setCustomerEditOpen] = useState(false);
  const [customerEditSaving, setCustomerEditSaving] = useState(false);

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
        <CustomerTagSection conversation={conversation} permissions={permissions} />
      </div>
    );
  }

  const displayName = customer.name.trim() || t("conversation.unnamedCustomer");
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
        <div className="rounded-xl border border-amber-200 bg-amber-50 px-3 py-2.5 text-xs leading-relaxed text-amber-950 shadow-[0_8px_18px_rgba(245,158,11,0.08)] dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-100 dark:shadow-none">
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
          {permissions.canUpdateCustomer ? (
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
          ) : null}
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

      {permissions.canViewTickets ? (
        <RelatedTicketsSection conversation={conversation} />
      ) : null}

      <CustomerTagSection conversation={conversation} permissions={permissions} />

      {permissions.canUpdateCustomer ? (
        <CustomerFormDialog
          open={customerEditOpen}
          onOpenChange={setCustomerEditOpen}
          saving={customerEditSaving}
          itemId={customer.id}
          onSave={async (payload: CustomerFormSavePayload) => {
            if (!permissions.canUpdateCustomer || customerEditSaving) {
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
          <div className="flex items-center justify-between gap-3 border-t border-border pt-2">
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
              className="rounded-lg border border-border bg-card px-3 py-2 shadow-sm"
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
