"use client";

import {
  ArrowRightLeftIcon,
  BotIcon,
  CircleUserRoundIcon,
  CircleXIcon,
  FilePlus2Icon,
  FilterIcon,
  MessageCircleWarningIcon,
  Menu,
  MoreHorizontalIcon,
  QrCodeIcon,
  SearchIcon,
  SendIcon,
  SettingsIcon,
  X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";

import { ConversationCloseDialog } from "@/components/conversation-actions/close-dialog";
import { ConversationTransferDialog } from "@/components/conversation-actions/transfer-dialog";
import { useAuth } from "@/components/auth-provider";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { OptionCombobox } from "@/components/option-combobox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { WxWorkProtocolInstanceManager } from "@/components/wxwork-protocol/wxwork-protocol-instance-manager";
import { WxWorkProtocolBindingDialog } from "@/components/wxwork-protocol/wxwork-protocol-binding-dialog";
import { useAgentConversationRealtime } from "@/hooks/use-agent-conversation-realtime";
import { useI18n } from "@/i18n/provider";
import {
  fetchWxWorkProtocolInstances,
  type WxWorkProtocolInstance,
} from "@/lib/api/admin";
import { sendArrivalBindingCard } from "@/lib/api/arrival";
import {
  fetchCurrentAgentPresence,
  updateAgentPresence,
  type AgentPresence,
} from "@/lib/api/service-analytics";
import { AgentPresenceStatus, AgentPresenceStatusLabels } from "@/lib/generated/enums";
import {
  agentConversationSelectors,
  useAgentConversationsStore,
} from "@/lib/stores/agent-conversations";
import { repairMojibakeText } from "@/lib/utils";
import { CreateTicketFromConversationDialog } from "../tickets/_components/create-ticket-from-conversation-dialog";
import { ChatPanel } from "./_components/chat-panel";
import { ConversationInfoPanel } from "./_components/conversation-info-panel";
import { ConversationList } from "./_components/conversation-list";

const workbenchIconButtonClassName =
  "size-8 rounded-lg border border-transparent bg-background/80 text-muted-foreground shadow-none hover:border-border hover:bg-background hover:text-primary";

function hasManualAttention(conversation?: { manualAttention?: { dot?: boolean } } | null) {
  return Boolean(conversation?.manualAttention?.dot);
}

export default function ConversationsPage() {
  const t = useI18n();
  const { session } = useAuth();
  const activeTenantId = session?.activeTenantId ?? session?.user.tenantId ?? 0;
  const conversation = useAgentConversationsStore(
    agentConversationSelectors.selectedConversation,
  );
  const conversations = useAgentConversationsStore((state) => state.conversations);
  const selectedWxWorkInstanceId = useAgentConversationsStore(
    (state) => state.selectedWxWorkInstanceId,
  );
  const setSelectedWxWorkInstanceId = useAgentConversationsStore(
    (state) => state.setSelectedWxWorkInstanceId,
  );
  const searchKeyword = useAgentConversationsStore((state) => state.searchKeyword);
  const setSearchKeyword = useAgentConversationsStore((state) => state.setSearchKeyword);
  const conversationFilter = useAgentConversationsStore((state) => state.conversationFilter);
  const setConversationFilter = useAgentConversationsStore((state) => state.setConversationFilter);
  const setTenantContext = useAgentConversationsStore((state) => state.setTenantContext);
  const loadConversations = useAgentConversationsStore(
    (state) => state.loadConversations,
  );
  const loadMessages = useAgentConversationsStore(
    (state) => state.loadMessages,
  );
  const selectConversation = useAgentConversationsStore(
    (state) => state.selectConversation,
  );
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const [mobileCustomerSheetOpen, setMobileCustomerSheetOpen] = useState(false);
  const [detailSheetOpen, setDetailSheetOpen] = useState(false);
  const [transferOpen, setTransferOpen] = useState(false);
  const [closeOpen, setCloseOpen] = useState(false);
  const [createTicketOpen, setCreateTicketOpen] = useState(false);
  const [accountManagerOpen, setAccountManagerOpen] = useState(false);
  const [scanLoginOpen, setScanLoginOpen] = useState(false);
  const [sendingArrivalBindingCard, setSendingArrivalBindingCard] =
    useState(false);
  const instancesRequestSeqRef = useRef(0);
  const [instances, setInstances] = useState<WxWorkProtocolInstance[]>([]);
  const [accountKeyword, setAccountKeyword] = useState("");
  const [handoffToastDismissedId, setHandoffToastDismissedId] = useState<number | null>(null);
  const [presence, setPresence] = useState<AgentPresence | null>(null);
  const [presenceUpdating, setPresenceUpdating] = useState(false);
  const [breakDialogOpen, setBreakDialogOpen] = useState(false);
  const [breakReason, setBreakReason] = useState("用餐");
  const permissions = useMemo(
    () => new Set(session?.permissions ?? []),
    [session?.permissions],
  );
  const canCreateTicket = permissions.has("ticket.create");
  const canAssignTicket = permissions.has("ticket.assign") && permissions.has("agent.view");
  const canViewTags = permissions.has("tag.view");
  const canViewWxWorkAccounts = permissions.has("channel.view");
  const canCreateWxWorkAccounts = canViewWxWorkAccounts && permissions.has("channel.create") && permissions.has("user.view");
  const canUpdateWxWorkAccounts = canViewWxWorkAccounts && permissions.has("channel.update");
  const canDeleteWxWorkAccounts = canViewWxWorkAccounts && permissions.has("channel.delete");
  const canManageWxWorkAccounts = canUpdateWxWorkAccounts || canDeleteWxWorkAccounts;
  const canTransferConversation = permissions.has("conversation.transfer");
  const canCloseConversation = permissions.has("conversation.close");
  const canSendArrivalBindingCard = permissions.has("conversation.send");
  const canUseConversationActions =
    canCreateTicket ||
    canTransferConversation ||
    canCloseConversation ||
    canSendArrivalBindingCard;
  const canUpdatePresence = permissions.has("agentPresence.update");
  const isSupportAgent = session?.roles?.includes("cs_user") ?? false;

  useEffect(() => {
    if (!canUpdatePresence || activeTenantId <= 0) {
      setPresence(null);
      return;
    }
    void fetchCurrentAgentPresence()
      .then(setPresence)
      .catch((error) => toast.error(error instanceof Error ? error.message : "客服状态加载失败"));
  }, [activeTenantId, canUpdatePresence]);

  const changePresence = async (status: AgentPresenceStatus, reason = "") => {
    if (!canUpdatePresence || presenceUpdating) return;
    if (status === AgentPresenceStatus.Break && !reason.trim()) {
      setBreakDialogOpen(true);
      return;
    }
    setPresenceUpdating(true);
    try {
      setPresence(await updateAgentPresence({ status, breakReason: reason.trim() || undefined }));
      if (status === AgentPresenceStatus.Break) setBreakDialogOpen(false);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "客服状态更新失败");
    } finally {
      setPresenceUpdating(false);
    }
  };
  const showingMyAttention = conversationFilter === "my_attention";
  const selectedInstance = instances.find((item) => item.id === selectedWxWorkInstanceId) ?? null;
  const conversationInstance =
    instances.find((item) => item.id === conversation?.wxWorkInstanceId) ?? selectedInstance;
  const aggregateAccountStats = useMemo(() => {
    if (!canViewWxWorkAccounts) {
      return conversations.reduce(
        (acc, item) => ({
          customerCount: acc.customerCount + 1,
          manualAttentionCount: acc.manualAttentionCount + (item.manualAttention?.dot ? 1 : 0),
          urgentManualAttentionCount:
            acc.urgentManualAttentionCount + (item.manualAttention?.level === "urgent" ? 1 : 0),
        }),
        { customerCount: 0, manualAttentionCount: 0, urgentManualAttentionCount: 0 },
      );
    }
    return instances.reduce(
        (acc, item) => ({
          customerCount: acc.customerCount + Number(item.customerCount || 0),
          manualAttentionCount: acc.manualAttentionCount + Number(item.manualAttentionCount || 0),
          urgentManualAttentionCount: acc.urgentManualAttentionCount + Number(item.urgentManualAttentionCount || 0),
        }),
        { customerCount: 0, manualAttentionCount: 0, urgentManualAttentionCount: 0 },
      );
  }, [canViewWxWorkAccounts, conversations, instances]);
  const myAttentionStats = useMemo(
    () => ({
      customerCount: conversations.length,
      manualAttentionCount: conversations.filter((item) => item.manualAttention?.dot).length,
      urgentManualAttentionCount: conversations.filter(
        (item) => item.manualAttention?.level === "urgent",
      ).length,
    }),
    [conversations],
  );
  const visibleAccountStats = showingMyAttention
    ? myAttentionStats
    : selectedInstance
      ? {
          customerCount: Number(selectedInstance.customerCount || 0),
          manualAttentionCount: Number(selectedInstance.manualAttentionCount || 0),
          urgentManualAttentionCount: Number(selectedInstance.urgentManualAttentionCount || 0),
        }
      : aggregateAccountStats;
  const accountCustomerCount = visibleAccountStats.customerCount;
  const manualAttentionCount = visibleAccountStats.manualAttentionCount;
  const urgentManualAttentionCount = visibleAccountStats.urgentManualAttentionCount;
  const filteredInstances = useMemo(() => {
    if (!canViewWxWorkAccounts) {
      return [];
    }
    const keyword = accountKeyword.trim().toLowerCase();
    if (!keyword) {
      return instances;
    }
    return instances.filter((item) =>
      [item.employeeName, item.employeeUserId, item.guid, item.storeName, item.storeCode]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(keyword)),
    );
  }, [accountKeyword, canViewWxWorkAccounts, instances]);

  const loadWxWorkInstances = useCallback(async () => {
    const requestSeq = ++instancesRequestSeqRef.current;
    if (!canViewWxWorkAccounts) {
      setInstances([]);
      return;
    }
    try {
      const page = await fetchWxWorkProtocolInstances({ status: 0, limit: 200 });
      if (requestSeq === instancesRequestSeqRef.current) {
        setInstances(page.results ?? []);
      }
    } catch (error) {
      if (requestSeq === instancesRequestSeqRef.current) {
        toast.error(error instanceof Error ? error.message : "加载员工号失败");
      }
    }
  }, [canViewWxWorkAccounts]);

  const handleInstanceUpdated = (updated: WxWorkProtocolInstance) => {
    if (!canViewWxWorkAccounts) return;
    setInstances((current) =>
      current.map((item) => (item.id === updated.id ? updated : item)),
    );
  };

  useEffect(() => {
    setTenantContext(activeTenantId);
  }, [activeTenantId, setTenantContext]);

  useEffect(() => {
    setInstances([]);
    if (!canViewWxWorkAccounts) {
      setSelectedWxWorkInstanceId(null);
      return;
    }
    void loadWxWorkInstances();
  }, [activeTenantId, canViewWxWorkAccounts, loadWxWorkInstances, setSelectedWxWorkInstanceId]);

  useEffect(() => {
    void loadConversations().catch((error) => {
      toast.error(error instanceof Error ? error.message : t("conversation.loadListFailed"));
    });
  }, [activeTenantId, loadConversations, selectedWxWorkInstanceId, t]);

  useEffect(() => {
    if (selectedWxWorkInstanceId !== null || !conversation?.wxWorkInstanceId) {
      return;
    }
    const frame = window.requestAnimationFrame(() => {
      const sourceButtons = document.querySelectorAll<HTMLButtonElement>(
        `[data-wxwork-instance-id="${conversation.wxWorkInstanceId}"]`,
      );
      Array.from(sourceButtons)
        .find((button) => button.offsetParent !== null)
        ?.scrollIntoView({ block: "nearest", behavior: "smooth" });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [conversation?.id, conversation?.wxWorkInstanceId, instances.length, selectedWxWorkInstanceId]);

  async function handleConversationChanged(conversationId: number) {
    await loadConversations();
    await loadMessages(conversationId, {
      forceLoading: false,
      reset: false,
    });
  }

  async function handleSendArrivalBindingCard() {
    if (!conversation || sendingArrivalBindingCard) return;
    setSendingArrivalBindingCard(true);
    try {
      await sendArrivalBindingCard(conversation.id);
      toast.success(t("conversation.arrivalBindingCardSent"));
      await handleConversationChanged(conversation.id);
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("conversation.arrivalBindingCardFailed"),
      );
    } finally {
      setSendingArrivalBindingCard(false);
    }
  }

  useAgentConversationRealtime();

  const selectConversationMode = (mode: "all_open" | "my_attention") => {
    setConversationFilter(mode);
    void loadConversations();
  };

  const selectWxWorkAccount = (instanceId: number | null) => {
    if (instanceId !== null && !canViewWxWorkAccounts) return;
    setSelectedWxWorkInstanceId(instanceId);
    if (showingMyAttention) {
      setConversationFilter("all_open");
    }
    void loadConversations();
  };

  const renderConversationSidebar = (opts?: { onListAfterSelect?: () => void }) => (
    <div className="flex h-full min-h-0 flex-1 bg-inherit">
      <div className="flex w-72 shrink-0 flex-col border-r border-border bg-background/95 xl:w-80">
        <div className="border-b border-border bg-background/95 px-4 py-4">
          <div className="flex items-center justify-between gap-2">
            <div className="flex min-w-0 items-center gap-2">
              <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
                <CircleUserRoundIcon className="size-4" />
              </div>
              <div className="min-w-0">
                <div className="truncate text-sm font-semibold text-foreground">企微员工号</div>
                <div className="mt-0.5 text-[11px] text-muted-foreground">账号 / 回调 / AI 托管</div>
              </div>
            </div>
            {canManageWxWorkAccounts ? (
              <Button
                variant="ghost"
                size="icon"
                className={`${workbenchIconButtonClassName} shrink-0`}
                onClick={() => setAccountManagerOpen(true)}
                aria-label="管理企微员工号"
                title="管理企微员工号"
              >
                <SettingsIcon className="size-4" />
              </Button>
            ) : null}
          </div>
          {canCreateWxWorkAccounts || canManageWxWorkAccounts ? (
            <div className={`mt-3 grid gap-2 ${canCreateWxWorkAccounts && canManageWxWorkAccounts ? "grid-cols-2" : "grid-cols-1"}`}>
              {canCreateWxWorkAccounts ? (
                <Button
                  variant="outline"
                  size="sm"
                  className="h-9 justify-center gap-1.5 rounded-lg border-primary/40 bg-background text-xs font-medium text-primary shadow-none hover:bg-primary/5 hover:text-primary"
                  onClick={() => setScanLoginOpen(true)}
                >
                  <QrCodeIcon className="size-4" />
                  新增账号
                </Button>
              ) : null}
              {canManageWxWorkAccounts ? (
                <Button
                  variant="outline"
                  size="sm"
                  className="h-9 justify-center gap-1.5 rounded-lg border-border bg-background text-xs font-medium text-foreground shadow-none hover:bg-muted"
                  onClick={() => setAccountManagerOpen(true)}
                >
                  <SettingsIcon className="size-4" />
                  账号设置
                </Button>
              ) : null}
            </div>
          ) : null}
          <div className="mt-3 grid grid-cols-2 gap-2 text-xs">
            <div className="rounded-lg border border-border bg-muted/40 px-3 py-2">
              <div className="text-muted-foreground">客户</div>
              <div className="mt-1 text-lg font-semibold leading-none text-foreground">{accountCustomerCount}</div>
            </div>
            <div className="rounded-lg border border-border bg-muted/40 px-3 py-2">
              <div className="text-muted-foreground">待人工</div>
              <div className="mt-1 text-lg font-semibold leading-none text-destructive">{manualAttentionCount}</div>
            </div>
          </div>
          {canViewWxWorkAccounts ? (
            <div className="relative mt-3">
              <SearchIcon className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={accountKeyword}
                onChange={(event) => setAccountKeyword(event.target.value)}
                placeholder="搜索员工号/门店"
                className="h-9 rounded-lg border-border bg-muted/50 pl-8 pr-8 text-xs shadow-none placeholder:text-muted-foreground focus-visible:bg-background"
              />
              <FilterIcon className="pointer-events-none absolute right-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            </div>
          ) : null}
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-3.5">
          <button
            type="button"
            className={`sticky top-0 z-10 mb-2 flex w-full items-center justify-between rounded-xl px-2.5 py-2 text-left text-sm transition ${
              selectedWxWorkInstanceId === null
                ? "bg-primary/10 text-primary ring-1 ring-inset ring-primary/15"
                : "bg-background text-foreground hover:bg-muted"
            }`}
            onClick={() => {
              selectWxWorkAccount(null);
            }}
          >
            <span className="flex min-w-0 items-center gap-2 truncate">
              <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-background text-[11px] shadow-sm">全</span>
              <span className="truncate font-medium">全部账号</span>
            </span>
            <span className="rounded-full bg-background px-1.5 text-[11px] text-muted-foreground shadow-sm">{aggregateAccountStats.customerCount}</span>
          </button>
          {filteredInstances.map((item) => {
            const isAccountFilter = selectedWxWorkInstanceId === item.id;
            const isCurrentConversationSource = conversation?.wxWorkInstanceId === item.id;
            return (
              <button
                key={item.id}
                type="button"
                data-wxwork-instance-id={item.id}
                aria-current={isCurrentConversationSource ? "true" : undefined}
                className={`group mb-2 w-full rounded-xl px-2.5 py-2 text-left text-sm transition ${
                  isAccountFilter
                    ? "bg-primary/10 text-primary ring-1 ring-inset ring-primary/15"
                    : selectedWxWorkInstanceId === null && isCurrentConversationSource
                      ? "bg-primary/5 text-primary ring-2 ring-inset ring-primary/35"
                      : "text-foreground hover:bg-muted"
                }`}
                onClick={() => {
                  selectWxWorkAccount(item.id);
                }}
              >
              <div className="flex items-center gap-2">
                <Avatar className="relative size-9 shrink-0 rounded-lg">
                  <AvatarImage src={item.employeeAvatar || ""} />
                  <AvatarFallback className="rounded-lg bg-muted text-xs font-semibold text-muted-foreground">
                  {(repairMojibakeText(item.employeeName) || item.guid || "企").slice(0, 1)}
                  </AvatarFallback>
                  {item.manualAttentionCount > 0 ? (
                    <span
                      className={`absolute -right-1 -top-1 size-3 rounded-full border-2 border-white ${
                        item.urgentManualAttentionCount > 0 ? "bg-rose-600" : "bg-red-500"
                      }`}
                    />
                  ) : null}
                  <span
                    className={`absolute -right-0.5 -bottom-0.5 size-2.5 rounded-full border border-white ${
                      item.healthStatus === "online" ? "bg-emerald-500" : item.healthStatus === "offline" ? "bg-[#aab4c3]" : "bg-amber-500"
                    }`}
                  />
                </Avatar>
                <div className="min-w-0 flex-1">
                  <span className="block truncate font-medium leading-4">{repairMojibakeText(item.employeeName) || item.guid}</span>
                  <span className="mt-0.5 block truncate text-[11px] text-muted-foreground">
                    {repairMojibakeText(item.storeName) || item.employeeUserId || "未绑定门店"}
                  </span>
                </div>
                {item.manualAttentionCount > 0 ? (
                  <span className="rounded-full bg-red-50 px-1.5 text-[11px] font-medium text-red-600">{item.manualAttentionCount}</span>
                ) : (
                  <span className="rounded-full bg-background px-1.5 text-[11px] text-muted-foreground shadow-sm">{item.customerCount || 0}</span>
                )}
              </div>
              <div className="mt-2 flex items-center gap-1 pl-11">
                <Badge variant={item.aiReplyEnabled === false ? "outline" : "secondary"} className="h-5 rounded-md border-border px-1.5 text-[10px] font-normal">
                  {item.aiReplyEnabled === false ? "AI停用" : "AI托管"}
                </Badge>
                <Badge variant={item.fallbackToHQ === false ? "outline" : "secondary"} className="h-5 rounded-md border-border px-1.5 text-[10px] font-normal">
                  {item.fallbackToHQ === false ? "门店处理" : "总部兜底"}
                </Badge>
              </div>
              </button>
            );
          })}
          {canViewWxWorkAccounts && filteredInstances.length === 0 ? (
            <div className="rounded-lg border border-dashed border-border bg-muted/40 px-3 py-6 text-center text-xs text-muted-foreground">
              没有匹配的员工号
            </div>
          ) : null}
        </div>
      </div>
      <div className="flex min-w-0 flex-1 flex-col bg-background/95">
        <div className="border-b border-border bg-background/95 px-4 py-4">
          {canManageWxWorkAccounts ? (
            <Button
              variant="outline"
              size="sm"
              className="mb-3 h-9 w-full justify-center gap-1.5 rounded-lg border-primary/40 bg-background text-xs font-medium text-primary shadow-none hover:bg-primary/5 hover:text-primary"
              onClick={() => setAccountManagerOpen(true)}
            >
              <FilePlus2Icon className="size-4" />
              管理会话入口
            </Button>
          ) : null}
          {isSupportAgent ? (
            <div className="mb-3 grid grid-cols-2 rounded-md bg-muted p-1" role="group" aria-label={t("conversation.viewMode")}>
              <button
                type="button"
                className={`h-8 rounded-sm px-2 text-xs font-medium transition-colors ${
                  !showingMyAttention
                    ? "bg-background text-foreground shadow-sm"
                    : "text-muted-foreground hover:text-foreground"
                }`}
                aria-pressed={!showingMyAttention}
                onClick={() => selectConversationMode("all_open")}
              >
                {t("conversation.currentAccount")}
              </button>
              <button
                type="button"
                className={`h-8 rounded-sm px-2 text-xs font-medium transition-colors ${
                  showingMyAttention
                    ? "bg-background text-foreground shadow-sm"
                    : "text-muted-foreground hover:text-foreground"
                }`}
                aria-pressed={showingMyAttention}
                onClick={() => selectConversationMode("my_attention")}
              >
                {t("conversation.myPendingReplies")}
              </button>
            </div>
          ) : null}
          <div className="relative">
            <SearchIcon className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={searchKeyword}
              onChange={(event) => {
                setSearchKeyword(event.target.value);
                void loadConversations();
              }}
              placeholder="搜索"
              className="h-9 rounded-lg border-border bg-muted/50 pl-8 pr-8 text-xs shadow-none placeholder:text-muted-foreground focus-visible:bg-background"
            />
            <FilterIcon className="pointer-events-none absolute right-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          </div>
        </div>
        <div className="flex h-14 shrink-0 items-center justify-between gap-2 border-b border-border bg-background/95 px-4 py-2">
          <div className="min-w-0 flex-1">
            <div className="truncate text-sm font-semibold text-foreground">
              {showingMyAttention
                ? t("conversation.myPendingReplies")
                : selectedInstance
                ? repairMojibakeText(selectedInstance.storeName) || repairMojibakeText(selectedInstance.employeeName) || selectedInstance.guid
                : "全部员工号"}
            </div>
            <div className="mt-0.5 flex items-center gap-2 text-xs text-muted-foreground">
              <span>客户</span>
              <span>{accountCustomerCount} 个</span>
              {manualAttentionCount > 0 ? (
                <span className="text-destructive">{manualAttentionCount} 个待人工</span>
              ) : null}
              {urgentManualAttentionCount > 0 ? (
                <span className="text-rose-700">{urgentManualAttentionCount} 个紧急</span>
              ) : null}
            </div>
          </div>
          <Button
            variant="ghost"
            size="icon"
            className={`${workbenchIconButtonClassName} mt-0.5 shrink-0 lg:hidden`}
            onClick={() => setMobileMenuOpen(false)}
          >
            <X className="size-4" />
          </Button>
        </div>
        <ConversationList onAfterSelect={opts?.onListAfterSelect} />
      </div>
    </div>
  );

  const handoffConversation = conversations.find((item) => hasManualAttention(item));

  const workspaceContent = (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-hidden bg-muted/40 text-card-foreground">
      <div className="mx-3 mt-3 flex h-16 shrink-0 items-center justify-between gap-3 rounded-lg border border-border bg-card px-4 py-2 shadow-sm">
        <div className="flex min-w-0 items-center gap-2 sm:gap-3">
          <Button
            variant="ghost"
            size="icon"
            className={`${workbenchIconButtonClassName} lg:hidden`}
            onClick={() => setMobileMenuOpen(true)}
          >
            <Menu className="size-4" />
          </Button>
          {conversation ? (
            <>
              <Avatar className="size-10 shrink-0 rounded-xl lg:size-11">
                <AvatarImage src={conversation.customerAvatar || ""} />
                <AvatarFallback className="rounded-xl bg-muted text-sm font-semibold text-muted-foreground">
                  {t("conversation.customerAvatar")}
                </AvatarFallback>
              </Avatar>
              <div className="min-w-0">
                <p className="min-w-0 truncate text-sm font-semibold leading-tight text-foreground">
                  {repairMojibakeText(conversation.customerName) ||
                    t("conversation.customerFallback", {
                      id: conversation.customerId || conversation.id,
                    })}
                </p>
                <p className="mt-0.5 truncate text-xs text-muted-foreground">
                  <span>{t("conversation.channelNumber", { id: conversation.channelId || "-" })}</span>
                  {conversation.customerId ? (
                    <>
                      <span className="text-muted-foreground/60"> / </span>
                      <span>{t("conversation.linkedCustomer")}</span>
                    </>
                  ) : null}
                </p>
              </div>
            </>
          ) : (
            <div className="min-w-0">
              <p className="truncate font-semibold text-[14px] leading-tight text-foreground">
                {t("conversation.workbenchTitle")}
              </p>
              <p className="mt-0.5 truncate text-[14px] text-muted-foreground sm:text-[14px] lg:hidden">
                {t("conversation.openMenuSelectConversation")}
              </p>
              <p className="mt-0.5 hidden truncate text-[12px] text-muted-foreground lg:block">
                {t("conversation.selectConversationFromSidebar")}
              </p>
            </div>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-1 sm:gap-1.5">
          {canUpdatePresence ? (
            <div className="w-20 sm:w-24" title={presence?.breakReason || "客服在线状态"}>
              <OptionCombobox
                value={presence?.status === "offline" ? "" : presence?.status ?? ""}
                options={Object.values(AgentPresenceStatus).map((status) => ({ value: status, label: AgentPresenceStatusLabels[status] }))}
                placeholder="离线"
                searchPlaceholder="选择状态"
                triggerClassName="h-8 w-20 rounded-md px-2 text-xs sm:w-24"
                disabled={presenceUpdating}
                onChange={(value) => {
                  const status = value as AgentPresenceStatus;
                  if (status === AgentPresenceStatus.Break) setBreakDialogOpen(true);
                  else void changePresence(status);
                }}
              />
            </div>
          ) : null}
          {conversation && conversation.manualAttention?.dot ? (
            <Badge className={`hidden rounded-md px-2 text-xs font-normal shadow-none sm:inline-flex ${
              conversation.manualAttention.level === "urgent"
                ? "bg-[#fff1f2] text-destructive"
                : "bg-amber-50 text-amber-700"
            }`}>
              {conversation.manualAttention.label || "待人工"}
            </Badge>
          ) : conversation ? (
            <Badge className="hidden rounded-md bg-primary/10 px-2 text-xs font-normal text-primary shadow-none sm:inline-flex">
              <BotIcon className="mr-1 size-3" />
              AI/人工协同
            </Badge>
          ) : null}
          <Button
            variant="ghost"
            size="icon"
            className={`${workbenchIconButtonClassName} lg:hidden`}
            disabled={!conversation}
            aria-label={t("conversation.conversationInfo")}
            onClick={() => setMobileCustomerSheetOpen(true)}
          >
            <CircleUserRoundIcon className="size-4" />
          </Button>
          {canUseConversationActions ? (
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <Button
                    variant="ghost"
                    size="icon"
                    className={workbenchIconButtonClassName}
                    disabled={!conversation}
                  />
                }
              >
                <MoreHorizontalIcon className="size-4" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-44 min-w-44">
                {canSendArrivalBindingCard ? (
                  <DropdownMenuItem
                    onClick={() => void handleSendArrivalBindingCard()}
                    disabled={!conversation || sendingArrivalBindingCard}
                  >
                    <SendIcon />
                    {sendingArrivalBindingCard
                      ? t("conversation.sendingArrivalBindingCard")
                      : t("conversation.sendArrivalBindingCard")}
                  </DropdownMenuItem>
                ) : null}
                {canCreateTicket ? (
                  <DropdownMenuItem
                    onClick={() => setCreateTicketOpen(true)}
                    disabled={!conversation}
                  >
                    <FilePlus2Icon />
                    {t("conversation.createTicket")}
                  </DropdownMenuItem>
                ) : null}
                {canTransferConversation ? (
                  <DropdownMenuItem
                    onClick={() => setTransferOpen(true)}
                    disabled={!conversation || conversation.status !== 3}
                  >
                    <ArrowRightLeftIcon />
                    {t("conversation.transferConversation")}
                  </DropdownMenuItem>
                ) : null}
                {canCloseConversation ? (
                  <DropdownMenuItem
                    onClick={() => setCloseOpen(true)}
                    disabled={!conversation || conversation.status === 4}
                  >
                    <CircleXIcon />
                    {t("conversation.closeConversation")}
                  </DropdownMenuItem>
                ) : null}
              </DropdownMenuContent>
            </DropdownMenu>
          ) : null}
          <Button
            variant="ghost"
            size="icon"
            className={`${workbenchIconButtonClassName} hidden lg:flex`}
            disabled={!conversation}
            onClick={() => setDetailSheetOpen(true)}
            aria-label={t("conversation.conversationInfo")}
          >
            <CircleUserRoundIcon className="size-4" />
          </Button>
        </div>
      </div>
      <div className="min-h-0 w-full flex-1 overflow-hidden px-3 pb-3 pt-3">
        <div className="flex h-full min-h-0 overflow-hidden rounded-lg bg-muted/40 ring-1 ring-inset ring-border">
        <ChatPanel
          wxWorkInstance={conversationInstance}
          onWxWorkInstanceUpdated={handleInstanceUpdated}
        />
        </div>
      </div>
    </div>
  );

  return (
    <div className="flex h-[calc(100dvh-var(--header-height))] min-h-0 w-full min-w-0 flex-col overflow-hidden bg-muted/30 p-0 lg:h-full lg:p-3">
      {mobileMenuOpen && (
        <button
          type="button"
          aria-label={t("conversation.closeConversationList")}
          className="fixed top-12 right-0 bottom-0 left-0 z-30 bg-black/50 lg:hidden"
          onClick={() => setMobileMenuOpen(false)}
        />
      )}
      <div
        className={`fixed top-12 bottom-0 left-0 z-40 flex w-[min(22rem,calc(100vw-0.75rem))] max-w-[min(22rem,calc(100vw-0.75rem))] flex-col overflow-hidden border-r border-border bg-background text-card-foreground shadow-xl transition-transform duration-300 ease-out will-change-transform touch-manipulation overscroll-contain supports-[padding:max(0px)]:pb-[env(safe-area-inset-bottom)] lg:hidden ${
          mobileMenuOpen ? "translate-x-0" : "-translate-x-full pointer-events-none"
        }`}
        aria-hidden={!mobileMenuOpen}
      >
        {renderConversationSidebar({
          onListAfterSelect: () => setMobileMenuOpen(false),
        })}
      </div>

      <div className="flex min-h-0 min-w-0 w-full flex-1 flex-col overflow-hidden lg:hidden">
        {workspaceContent}
      </div>
      <div className="hidden min-h-0 w-full flex-1 grid-cols-[288px_360px_minmax(0,1fr)] overflow-hidden rounded-lg border border-border bg-background shadow-sm lg:grid xl:grid-cols-[320px_390px_minmax(0,1fr)]">
        <div className="col-span-2 min-h-0 border-r border-border bg-background">
          {renderConversationSidebar()}
        </div>
        <div className="min-h-0 bg-muted/40">
          {workspaceContent}
        </div>
      </div>
      <ConversationTransferDialog
        open={canTransferConversation && transferOpen}
        mode="transfer"
        conversationId={conversation?.id ?? null}
        onOpenChange={setTransferOpen}
        onSuccess={async () => {
          setTransferOpen(false);
          if (conversation?.id) {
            await handleConversationChanged(conversation.id);
          }
        }}
      />
      <ConversationCloseDialog
        open={canCloseConversation && closeOpen}
        conversationId={conversation?.id ?? null}
        onOpenChange={setCloseOpen}
        onSuccess={async () => {
          setCloseOpen(false);
          if (conversation?.id) {
            await handleConversationChanged(conversation.id);
          }
        }}
      />
      <Dialog open={breakDialogOpen} onOpenChange={setBreakDialogOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>进入休息状态</DialogTitle>
            <DialogDescription>休息原因会进入客服出勤记录。</DialogDescription>
          </DialogHeader>
          <label className="space-y-2 text-sm">
            休息原因
            <Input value={breakReason} maxLength={100} onChange={(event) => setBreakReason(event.target.value)} placeholder="如：用餐、培训、会议" />
          </label>
          <DialogFooter>
            <Button variant="outline" onClick={() => setBreakDialogOpen(false)}>取消</Button>
            <Button disabled={presenceUpdating || !breakReason.trim()} onClick={() => void changePresence(AgentPresenceStatus.Break, breakReason)}>{presenceUpdating ? "更新中" : "确认休息"}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <CreateTicketFromConversationDialog
        open={canCreateTicket && createTicketOpen}
        onOpenChange={setCreateTicketOpen}
        conversation={
          conversation
            ? {
                id: conversation.id,
                customerName: repairMojibakeText(conversation.customerName),
                customerId: conversation.customerId ?? 0,
                lastMessageSummary: repairMojibakeText(conversation.lastMessageSummary),
                currentAssigneeId: conversation.currentAssigneeId,
              }
            : null
        }
        canAssign={canAssignTicket}
        canManageTags={canViewTags}
        onSuccess={() => {
          setCreateTicketOpen(false);
        }}
      />

      <Sheet open={mobileCustomerSheetOpen} onOpenChange={setMobileCustomerSheetOpen}>
        <SheetContent
          side="right"
          className="flex w-full flex-col gap-0 border-l p-0 sm:max-w-md"
          showCloseButton
        >
          <ConversationInfoPanel
            conversation={conversation}
            variant="embedded"
            className="min-h-0 flex-1"
          />
        </SheetContent>
      </Sheet>
      <Sheet open={detailSheetOpen} onOpenChange={setDetailSheetOpen}>
        <SheetContent
          side="right"
          className="flex w-full flex-col gap-0 border-l p-0 sm:max-w-md"
          showCloseButton
        >
          <ConversationInfoPanel
            conversation={conversation}
            variant="embedded"
            className="min-h-0 flex-1"
          />
        </SheetContent>
      </Sheet>
      <Sheet open={canManageWxWorkAccounts && accountManagerOpen} onOpenChange={setAccountManagerOpen}>
        <SheetContent
          side="left"
          className="flex w-full flex-col gap-0 overflow-y-auto border-r p-0 sm:max-w-6xl"
          showCloseButton
        >
          <div className="flex min-h-full flex-col gap-4 p-6">
            <SheetHeader className="text-left">
              <SheetTitle>企微员工号账号管理</SheetTitle>
              <SheetDescription>
                在会话工作台内新增、编辑、删除员工号实例，并设置协议回调地址。企业微信员工号协议只按 wework.apifox.cn 文档字段接入。
              </SheetDescription>
            </SheetHeader>
            {canManageWxWorkAccounts ? (
              <WxWorkProtocolInstanceManager
                layout="fragment"
                hideCreateActions
                tableShellClassName="max-h-[70vh] overflow-auto"
                onChanged={() => void loadWxWorkInstances()}
              />
            ) : null}
          </div>
        </SheetContent>
      </Sheet>
      <WxWorkProtocolBindingDialog
        open={canCreateWxWorkAccounts && scanLoginOpen}
        onOpenChange={setScanLoginOpen}
        onChanged={async () => {
          await loadWxWorkInstances()
        }}
      />
      {handoffConversation && handoffToastDismissedId !== handoffConversation.id ? (
        <div className="agentdesk-surface fixed right-4 bottom-4 z-50 w-[min(22rem,calc(100vw-2rem))] rounded-2xl p-4 text-card-foreground">
          <div className="flex items-start gap-3">
            <div className="mt-0.5 flex size-10 shrink-0 items-center justify-center rounded-xl border border-destructive/15 bg-destructive/10 text-destructive">
              <MessageCircleWarningIcon className="size-5" />
            </div>
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium">{handoffConversation.manualAttention?.label || "新的转人工请求"}</div>
              <div className="mt-1 line-clamp-2 text-xs text-muted-foreground">
                {repairMojibakeText(handoffConversation.customerName) || `会话 #${handoffConversation.id}`}：
                {repairMojibakeText(handoffConversation.handoffReason) || repairMojibakeText(handoffConversation.lastMessageSummary) || "等待同事处理"}
              </div>
              <div className="mt-3 flex items-center gap-2">
                <Button
                  size="sm"
                  className="h-8 rounded-lg"
                  onClick={() => {
                    void selectConversation(handoffConversation.id);
                  }}
                >
                  查看会话
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-8 rounded-lg"
                  onClick={() => setHandoffToastDismissedId(handoffConversation.id)}
                >
                  稍后
                </Button>
              </div>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}
