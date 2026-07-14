"use client";

import {
  AlertTriangleIcon,
  ArrowRightLeftIcon,
  BotIcon,
  CircleUserRoundIcon,
  CircleXIcon,
  FilePlus2Icon,
  FilterIcon,
  LinkIcon,
  MessageCircleWarningIcon,
  Menu,
  MoreHorizontalIcon,
  QrCodeIcon,
  SearchIcon,
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { WxWorkProtocolInstanceManager } from "@/components/wxwork-protocol/wxwork-protocol-instance-manager";
import { useAgentConversationRealtime } from "@/hooks/use-agent-conversation-realtime";
import { useI18n } from "@/i18n/provider";
import {
  checkWxWorkProtocolLoginQrcode,
  createWxWorkProtocolRemoteSetup,
  fetchWxWorkProtocolInstances,
  resolveWxWorkProtocolLoginBinding,
  startWxWorkProtocolLogin,
  syncWxWorkProtocolProfile,
  type StartWxWorkProtocolLoginResult,
  type WxWorkProtocolInstance,
} from "@/lib/api/admin";
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
  const [scanLoginLoading, setScanLoginLoading] = useState(false);
  const [remoteSetupLoading, setRemoteSetupLoading] = useState(false);
  const [scanLoginResult, setScanLoginResult] = useState<StartWxWorkProtocolLoginResult | null>(null);
  const [scanLoginStatus, setScanLoginStatus] = useState("等待生成登录二维码");
  const [scanLoginError, setScanLoginError] = useState("");
  const [scanLoginResolving, setScanLoginResolving] = useState(false);
  const scanLoginSucceededRef = useRef(false);
  const scanLoginCheckingRef = useRef(false);
  const instancesRequestSeqRef = useRef(0);
  const [instances, setInstances] = useState<WxWorkProtocolInstance[]>([]);
  const [accountKeyword, setAccountKeyword] = useState("");
  const [handoffToastDismissedId, setHandoffToastDismissedId] = useState<number | null>(null);
  const permissions = useMemo(
    () => new Set(session?.permissions ?? []),
    [session?.permissions],
  );
  const canCreateTicket = permissions.has("ticket.create");
  const canAssignTicket = permissions.has("ticket.assign") && permissions.has("agent.view");
  const canViewTags = permissions.has("tag.view");
  const canViewWxWorkAccounts = permissions.has("channel.view");
  const canCreateWxWorkAccounts = canViewWxWorkAccounts && permissions.has("channel.create");
  const canUpdateWxWorkAccounts = canViewWxWorkAccounts && permissions.has("channel.update");
  const canDeleteWxWorkAccounts = canViewWxWorkAccounts && permissions.has("channel.delete");
  const canManageWxWorkAccounts = canUpdateWxWorkAccounts || canDeleteWxWorkAccounts;
  const canTransferConversation = permissions.has("conversation.transfer");
  const canCloseConversation = permissions.has("conversation.close");
  const canUseConversationActions = canCreateTicket || canTransferConversation || canCloseConversation;
  const isSupportAgent = session?.roles?.includes("cs_user") ?? false;
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

  const cleanupPendingScanLogin = async () => {
    scanLoginSucceededRef.current = false;
  };

  const handleScanLoginOpenChange = (open: boolean) => {
    if (open && !canCreateWxWorkAccounts) {
      return;
    }
    if (!open) {
      void cleanupPendingScanLogin();
    }
    setScanLoginOpen(open);
  };

  const startScanLogin = async () => {
    if (!canCreateWxWorkAccounts) return;
    setScanLoginOpen(true);
    scanLoginSucceededRef.current = false;
    setScanLoginLoading(true);
    setScanLoginResult(null);
    setScanLoginError("");
    setScanLoginStatus("正在从协议平台空闲实例池绑定真实 guid，并生成登录二维码...");
    try {
      const result = await startWxWorkProtocolLogin();
      setScanLoginResult(result);
      setScanLoginStatus("已绑定空闲实例，请用企业微信员工号扫码确认登录");
      await loadWxWorkInstances();
    } catch (error) {
      const message = error instanceof Error ? error.message : "获取登录二维码失败";
      setScanLoginError(message);
      setScanLoginStatus(message);
      toast.error(message);
    } finally {
      setScanLoginLoading(false);
    }
  };

  const resolveScanLoginBinding = async () => {
    if (!canCreateWxWorkAccounts) return;
    setScanLoginResolving(true);
    try {
      await resolveWxWorkProtocolLoginBinding(0);
      toast.success("已处理未登录的临时占用，正在重新生成二维码");
      await startScanLogin();
    } catch (error) {
      const message = error instanceof Error ? error.message : "处理绑定占用失败";
      setScanLoginError(message);
      setScanLoginStatus(message);
      toast.error(message);
    } finally {
      setScanLoginResolving(false);
    }
  };

  const createRemoteSetupLink = async () => {
    if (!canCreateWxWorkAccounts) return;
    setScanLoginOpen(true);
    setRemoteSetupLoading(true);
    try {
      const item = await createWxWorkProtocolRemoteSetup({ channelId: 0, remark: "远程门店开户链接" });
      const url = item.remoteSetupUrl || `${window.location.origin}/wxwork-remote-setup?token=${encodeURIComponent(item.remoteSetupToken || "")}`;
      await navigator.clipboard.writeText(url);
      toast.success("远程开户链接已复制，可以发给门店负责人");
      await loadWxWorkInstances();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "生成远程链接失败");
    } finally {
      setRemoteSetupLoading(false);
    }
  };

  useEffect(() => {
    if (!canCreateWxWorkAccounts || !scanLoginOpen || !scanLoginResult?.instance.id) {
      return;
    }
    let disposed = false;
    const timer = window.setInterval(async () => {
      if (disposed || scanLoginCheckingRef.current) {
        return;
      }
      scanLoginCheckingRef.current = true;
      try {
        const raw = await checkWxWorkProtocolLoginQrcode(scanLoginResult.instance.id);
        const lower = raw.toLowerCase();
        if (lower.includes("success") || lower.includes("login") || lower.includes("已登录") || lower.includes("登录成功")) {
          setScanLoginStatus("登录成功，正在同步员工号资料...");
          if (canUpdateWxWorkAccounts) {
            await syncWxWorkProtocolProfile(scanLoginResult.instance.id).catch(() => "");
          }
          await loadWxWorkInstances();
          scanLoginSucceededRef.current = true;
          toast.success("员工号登录成功，请继续绑定门店和知识库");
          setScanLoginOpen(false);
        } else {
          setScanLoginStatus("等待扫码确认，系统会自动轮询登录状态");
        }
      } catch (error) {
        setScanLoginStatus(error instanceof Error ? error.message : "检查扫码状态失败");
      } finally {
        scanLoginCheckingRef.current = false;
      }
    }, 3000);
    return () => {
      disposed = true;
      window.clearInterval(timer);
    };
  }, [canCreateWxWorkAccounts, canUpdateWxWorkAccounts, loadWxWorkInstances, scanLoginOpen, scanLoginResult?.instance.id]);

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
                  onClick={() => void startScanLogin()}
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
      <Dialog open={canCreateWxWorkAccounts && scanLoginOpen} onOpenChange={handleScanLoginOpenChange}>
        <DialogContent className="agentdesk-surface rounded-2xl sm:max-w-4xl">
          <DialogHeader>
            <DialogTitle>新增企微员工号</DialogTitle>
            <DialogDescription>
              新增账号只从这里进入。现场门店用左侧扫码；异地门店用右侧链接自助完成扫码、门店资料、坐标、服务时间和群通知配置。
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 md:grid-cols-2">
            <div className="rounded-2xl border border-[#dbe7f6] bg-white p-5 shadow-[0_12px_32px_rgba(35,74,122,0.06)]">
              <div className="flex items-start gap-3">
                <div className="flex size-9 shrink-0 items-center justify-center rounded-xl bg-[#eef4ff] text-[#2563eb]">
                  <QrCodeIcon className="size-4" />
                </div>
                <div className="min-w-0 flex-1">
                  <div className="font-semibold text-foreground">总部现场扫码</div>
                  <p className="mt-2 text-sm leading-6 text-muted-foreground">
                    系统先从实例池认领真实空闲 GUID，再生成协议登录二维码。没空闲实例时不会创建占位账号。
                  </p>
                </div>
              </div>
              <div className="mt-4 flex min-h-64 items-center justify-center rounded-2xl border border-[#e6edf7] bg-[#f8fbff] p-4">
                {scanLoginResult?.qrcode ? (
                  <img
                    src={scanLoginResult.qrcode.startsWith("data:") ? scanLoginResult.qrcode : `data:image/png;base64,${scanLoginResult.qrcode}`}
                    alt="企微员工号登录二维码"
                    className="size-56 rounded-xl bg-white object-contain p-2 shadow-[0_12px_30px_rgba(30,64,175,0.12)]"
                  />
                ) : scanLoginLoading ? (
                  <div className="text-sm text-muted-foreground">正在生成二维码...</div>
                ) : (
                  <div className="text-center text-sm text-muted-foreground">
                    <QrCodeIcon className="mx-auto mb-2 size-10" />
                    点击下方按钮生成二维码
                  </div>
                )}
              </div>
              <div className="mt-3 rounded-xl border border-[#dbe7f6] bg-[#f6f9ff] p-3 text-xs text-muted-foreground shadow-inner shadow-blue-100/30">
                <div className="font-medium text-foreground">{scanLoginStatus}</div>
                {scanLoginResult?.qrcodeContent ? <div className="mt-1 break-all">二维码内容：{scanLoginResult.qrcodeContent}</div> : null}
              </div>
              {scanLoginError ? (
                <div className="mt-3 rounded-xl border border-amber-200 bg-amber-50 p-3 text-xs text-amber-900">
                  <div className="flex items-start gap-2">
                    <AlertTriangleIcon className="mt-0.5 size-4 shrink-0" />
                    <div className="min-w-0 flex-1">
                      <div className="font-medium">实例绑定需要处理</div>
                      <div className="mt-1 leading-5">
                        系统只会清理未登录、未接过客户消息的临时占用；已经登录的员工号不会被自动解绑。
                      </div>
                      <Button
                        className="mt-3 h-8 rounded-lg bg-amber-600 px-3 text-xs text-white hover:bg-amber-700"
                        onClick={() => void resolveScanLoginBinding()}
                        disabled={scanLoginResolving || scanLoginLoading || remoteSetupLoading}
                      >
                        {scanLoginResolving ? "处理中..." : "处理占用并重新生成"}
                      </Button>
                    </div>
                  </div>
                </div>
              ) : null}
              <Button className="mt-4 w-full rounded-xl" onClick={() => void startScanLogin()} disabled={scanLoginLoading || remoteSetupLoading || scanLoginResolving}>
                <QrCodeIcon className="size-4" />
                {scanLoginLoading ? "生成中..." : scanLoginResult ? "重新生成现场扫码" : "生成现场扫码"}
              </Button>
            </div>
            <div className="rounded-2xl border border-[#dbe7f6] bg-white p-5 shadow-[0_12px_32px_rgba(35,74,122,0.06)]">
              <div className="flex items-start gap-3">
                <div className="flex size-9 shrink-0 items-center justify-center rounded-xl bg-[#eef4ff] text-[#2563eb]">
                  <LinkIcon className="size-4" />
                </div>
                <div className="min-w-0 flex-1">
                  <div className="font-semibold text-foreground">远程门店自助开户</div>
                  <p className="mt-2 text-sm leading-6 text-muted-foreground">
                    生成链接发给外地门店。对方打开后完成协议扫码，并填写门店名称、坐标、服务时间、托管模式和群通知。
                  </p>
                </div>
              </div>
              <div className="mt-4 rounded-2xl border border-[#e6edf7] bg-[#f8fbff] p-4 text-sm leading-6 text-muted-foreground">
                <div className="font-medium text-foreground">门店负责人会在独立页面完成：</div>
                <div className="mt-2">1. 企微员工号扫码登录协议实例</div>
                <div>2. 一键获取门店坐标并填写门店资料</div>
                <div>3. 选择服务时间、托管模式、门店群和 @ 成员</div>
                <div>4. 绑定门店知识库，模型未覆盖时走全局配置</div>
              </div>
              <Button className="mt-4 w-full rounded-xl" variant="outline" onClick={() => void createRemoteSetupLink()} disabled={scanLoginLoading || remoteSetupLoading}>
                <LinkIcon className="size-4" />
                {remoteSetupLoading ? "生成中..." : "生成并复制链接"}
              </Button>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setScanLoginOpen(false)}>关闭</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
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
