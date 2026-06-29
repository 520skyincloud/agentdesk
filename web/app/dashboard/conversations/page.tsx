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
  SettingsIcon,
  X,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";

import { ConversationCloseDialog } from "@/components/conversation-actions/close-dialog";
import { ConversationTransferDialog } from "@/components/conversation-actions/transfer-dialog";
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
  deleteWxWorkProtocolInstance,
  fetchWxWorkProtocolInstances,
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
  "size-8 rounded-lg border border-transparent bg-white/80 text-muted-foreground shadow-none hover:border-[#d9e2f2] hover:bg-white hover:text-[#2563eb]";

export default function ConversationsPage() {
  const t = useI18n();
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
  const [scanLoginResult, setScanLoginResult] = useState<StartWxWorkProtocolLoginResult | null>(null);
  const [scanLoginStatus, setScanLoginStatus] = useState("等待生成登录二维码");
  const scanLoginSucceededRef = useRef(false);
  const scanLoginCheckingRef = useRef(false);
  const [instances, setInstances] = useState<WxWorkProtocolInstance[]>([]);
  const [accountKeyword, setAccountKeyword] = useState("");
  const [handoffToastDismissedId, setHandoffToastDismissedId] = useState<number | null>(null);
  const selectedInstance = instances.find((item) => item.id === selectedWxWorkInstanceId) ?? null;
  const conversationInstance =
    instances.find((item) => item.id === conversation?.wxWorkInstanceId) ?? selectedInstance;
  const pendingHandoffCount = conversations.filter((item) => item.needHumanFollowUp).length;
  const filteredInstances = useMemo(() => {
    const keyword = accountKeyword.trim().toLowerCase();
    if (!keyword) {
      return instances;
    }
    return instances.filter((item) =>
      [item.employeeName, item.employeeUserId, item.guid, item.storeName, item.storeCode]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(keyword)),
    );
  }, [accountKeyword, instances]);

  const handleInstanceUpdated = (updated: WxWorkProtocolInstance) => {
    setInstances((current) =>
      current.map((item) => (item.id === updated.id ? updated : item)),
    );
  };

  const cleanupPendingScanLogin = async () => {
    const instance = scanLoginResult?.instance;
    if (!instance?.id || scanLoginSucceededRef.current || instance.healthStatus !== "login_qrcode") {
      return;
    }
    try {
      await deleteWxWorkProtocolInstance(instance.id);
      await loadWxWorkInstances();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "清理未完成扫码账号失败");
    }
  };

  const handleScanLoginOpenChange = (open: boolean) => {
    if (!open) {
      void cleanupPendingScanLogin();
    }
    setScanLoginOpen(open);
  };

  const startScanLogin = async () => {
    setScanLoginOpen(true);
    scanLoginSucceededRef.current = false;
    setScanLoginLoading(true);
    setScanLoginResult(null);
    setScanLoginStatus("正在向协议服务请求登录二维码...");
    try {
      const result = await startWxWorkProtocolLogin();
      setScanLoginResult(result);
      setScanLoginStatus("请用企业微信员工号扫码确认登录");
      await loadWxWorkInstances();
    } catch (error) {
      setScanLoginStatus(error instanceof Error ? error.message : "获取登录二维码失败");
      toast.error(error instanceof Error ? error.message : "获取登录二维码失败");
    } finally {
      setScanLoginLoading(false);
    }
  };

  useEffect(() => {
    if (!scanLoginOpen || !scanLoginResult?.instance.id) {
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
          await syncWxWorkProtocolProfile(scanLoginResult.instance.id).catch(() => "");
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
  }, [scanLoginOpen, scanLoginResult?.instance.id]);

  useEffect(() => {
    void loadConversations().catch((error) => {
      toast.error(error instanceof Error ? error.message : t("conversation.loadListFailed"));
    });
  }, [loadConversations, selectedWxWorkInstanceId, t]);

  const loadWxWorkInstances = async () => {
    await fetchWxWorkProtocolInstances({ status: 0, limit: 200 })
      .then((page) => setInstances(page.results ?? []))
      .catch((error) => {
        toast.error(error instanceof Error ? error.message : "加载员工号失败");
      });
  };

  useEffect(() => {
    void loadWxWorkInstances();
  }, []);

  async function handleConversationChanged(conversationId: number) {
    await loadConversations();
    await loadMessages(conversationId, {
      forceLoading: false,
      reset: false,
    });
  }

  useAgentConversationRealtime();

  const renderConversationSidebar = (opts?: { onListAfterSelect?: () => void }) => (
    <div className="flex h-full min-h-0 flex-1 bg-inherit">
      <div className="flex w-72 shrink-0 flex-col border-r border-[#e9edf5] bg-white/95 xl:w-80">
        <div className="border-b border-[#eef2f7] bg-white/95 px-4 py-4">
          <div className="flex items-center justify-between gap-2">
            <div className="flex min-w-0 items-center gap-2">
              <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-[#eef4ff] text-[#2563eb]">
                <CircleUserRoundIcon className="size-4" />
              </div>
              <div className="min-w-0">
                <div className="truncate text-sm font-semibold text-[#1f2937]">企微员工号</div>
                <div className="mt-0.5 text-[11px] text-[#6b7280]">账号 / 回调 / AI 托管</div>
              </div>
            </div>
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
          </div>
          <div className="mt-3 grid grid-cols-2 gap-2">
            <Button
              variant="outline"
              size="sm"
              className="h-9 justify-center gap-1.5 rounded-lg border-[#4f75ff] bg-white text-xs font-medium text-[#2855d9] shadow-none hover:bg-[#f4f7ff] hover:text-[#2855d9]"
              onClick={() => void startScanLogin()}
            >
              <QrCodeIcon className="size-4" />
              新增账号
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="h-9 justify-center gap-1.5 rounded-lg border-[#d9e2f2] bg-white text-xs font-medium text-[#344054] shadow-none hover:bg-[#f6f8fc]"
              onClick={() => setAccountManagerOpen(true)}
            >
              <SettingsIcon className="size-4" />
              账号设置
            </Button>
          </div>
          <div className="mt-3 grid grid-cols-2 gap-2 text-xs">
            <div className="rounded-xl border border-[#edf1f7] bg-[#f8fbff] px-3 py-2">
              <div className="text-[#7a8599]">当前会话</div>
              <div className="mt-1 text-lg font-semibold leading-none text-[#1f2937]">{conversations.length}</div>
            </div>
            <div className="rounded-xl border border-[#edf1f7] bg-[#f8fbff] px-3 py-2">
              <div className="text-[#7a8599]">待人工</div>
              <div className="mt-1 text-lg font-semibold leading-none text-destructive">{pendingHandoffCount}</div>
            </div>
          </div>
          <div className="relative mt-3">
            <SearchIcon className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-[#9aa4b2]" />
            <Input
              value={accountKeyword}
              onChange={(event) => setAccountKeyword(event.target.value)}
              placeholder="搜索员工号/门店"
              className="h-9 rounded-lg border-[#d9e2f2] bg-[#f5f7fb] pl-8 pr-8 text-xs shadow-none placeholder:text-[#9aa4b2] focus-visible:bg-white"
            />
            <FilterIcon className="pointer-events-none absolute right-2.5 top-1/2 size-3.5 -translate-y-1/2 text-[#9aa4b2]" />
          </div>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-3.5">
          <button
            type="button"
            className={`mb-2 flex w-full items-center justify-between rounded-xl px-2.5 py-2 text-left text-sm transition ${
              selectedWxWorkInstanceId === null
                ? "bg-[#eef3ff] text-[#2855d9] shadow-[inset_0_0_0_1px_rgba(79,117,255,0.12)]"
                : "text-[#344054] hover:bg-[#f7f9fd]"
            }`}
            onClick={() => {
              setSelectedWxWorkInstanceId(null);
              void loadConversations();
            }}
          >
            <span className="flex min-w-0 items-center gap-2 truncate">
              <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-white text-[11px] shadow-sm">全</span>
              <span className="truncate font-medium">全部账号</span>
            </span>
            <span className="rounded-full bg-white px-1.5 text-[11px] text-[#7a8599] shadow-sm">{conversations.length}</span>
          </button>
          {filteredInstances.map((item) => (
            <button
              key={item.id}
              type="button"
            className={`group mb-2 w-full rounded-xl px-2.5 py-2 text-left text-sm transition ${
                selectedWxWorkInstanceId === item.id
                  ? "bg-[#eef3ff] text-[#2855d9] shadow-[inset_0_0_0_1px_rgba(79,117,255,0.12)]"
                  : "text-[#344054] hover:bg-[#f7f9fd]"
              }`}
              onClick={() => {
                setSelectedWxWorkInstanceId(item.id);
                void loadConversations();
              }}
            >
              <div className="flex items-center gap-2">
                <Avatar className="relative size-9 shrink-0 rounded-lg">
                  <AvatarImage src={item.employeeAvatar || ""} />
                  <AvatarFallback className="rounded-lg bg-[#f0f4fb] text-xs font-semibold text-[#526072]">
                  {(repairMojibakeText(item.employeeName) || item.guid || "企").slice(0, 1)}
                  </AvatarFallback>
                  <span
                    className={`absolute -right-0.5 -bottom-0.5 size-2.5 rounded-full border border-white ${
                      item.healthStatus === "online" ? "bg-emerald-500" : item.healthStatus === "offline" ? "bg-[#aab4c3]" : "bg-amber-500"
                    }`}
                  />
                </Avatar>
                <div className="min-w-0 flex-1">
                  <span className="block truncate font-medium leading-4">{repairMojibakeText(item.employeeName) || item.guid}</span>
                  <span className="mt-0.5 block truncate text-[11px] text-[#7a8599]">
                    {repairMojibakeText(item.storeName) || item.employeeUserId || "未绑定门店"}
                  </span>
                </div>
                <MoreHorizontalIcon className="size-4 shrink-0 text-[#a0a8b7] opacity-0 transition group-hover:opacity-100" />
              </div>
              <div className="mt-2 flex items-center gap-1 pl-11">
                <Badge variant={item.aiReplyEnabled === false ? "outline" : "secondary"} className="h-5 rounded-md border-[#d9e2f2] px-1.5 text-[10px] font-normal">
                  {item.aiReplyEnabled === false ? "AI停用" : "AI托管"}
                </Badge>
                <Badge variant={item.fallbackToHQ === false ? "outline" : "secondary"} className="h-5 rounded-md border-[#d9e2f2] px-1.5 text-[10px] font-normal">
                  {item.fallbackToHQ === false ? "门店处理" : "总部兜底"}
                </Badge>
              </div>
            </button>
          ))}
          {filteredInstances.length === 0 ? (
            <div className="rounded-lg border border-dashed border-[#d9e2f2] bg-[#f7f9fd] px-3 py-6 text-center text-xs text-[#7a8599]">
              没有匹配的员工号
            </div>
          ) : null}
        </div>
      </div>
      <div className="flex min-w-0 flex-1 flex-col bg-white/95">
        <div className="border-b border-[#eef2f7] bg-white/95 px-4 py-4">
          <Button
            variant="outline"
            size="sm"
            className="mb-3 h-9 w-full justify-center gap-1.5 rounded-lg border-[#4f75ff] bg-white text-xs font-medium text-[#2855d9] shadow-none hover:bg-[#f4f7ff] hover:text-[#2855d9]"
            onClick={() => setAccountManagerOpen(true)}
          >
            <FilePlus2Icon className="size-4" />
            管理会话入口
          </Button>
          <div className="relative">
            <SearchIcon className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-[#9aa4b2]" />
            <Input
              value={searchKeyword}
              onChange={(event) => {
                setSearchKeyword(event.target.value);
                void loadConversations();
              }}
              placeholder="搜索"
              className="h-9 rounded-lg border-[#d9e2f2] bg-[#f5f7fb] pl-8 pr-8 text-xs shadow-none placeholder:text-[#9aa4b2] focus-visible:bg-white"
            />
            <FilterIcon className="pointer-events-none absolute right-2.5 top-1/2 size-3.5 -translate-y-1/2 text-[#9aa4b2]" />
          </div>
        </div>
        <div className="flex h-14 shrink-0 items-center justify-between gap-2 border-b border-[#eef2f7] bg-white/95 px-4 py-2">
          <div className="min-w-0 flex-1">
            <div className="truncate text-sm font-semibold text-[#1f2937]">
              {selectedInstance
                ? repairMojibakeText(selectedInstance.storeName) || repairMojibakeText(selectedInstance.employeeName) || selectedInstance.guid
                : "全部员工号"}
            </div>
            <div className="mt-0.5 flex items-center gap-2 text-xs text-[#7a8599]">
              <span>全部未关闭会话</span>
              {pendingHandoffCount > 0 ? (
                <span className="text-destructive">{pendingHandoffCount} 个待处理</span>
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

  const handoffConversation = conversations.find((item) => item.needHumanFollowUp);

  const workspaceContent = (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-hidden bg-[#f0f3f8] text-card-foreground">
      <div className="mx-3 mt-3 flex h-16 shrink-0 items-center justify-between gap-3 rounded-2xl border border-white bg-white/95 px-4 py-2 shadow-[0_10px_28px_rgba(31,41,55,0.05)]">
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
                <AvatarFallback className="rounded-xl bg-[#f0f4fb] text-sm font-semibold text-[#526072]">
                  {t("conversation.customerAvatar")}
                </AvatarFallback>
              </Avatar>
              <div className="min-w-0">
                <p className="min-w-0 truncate text-sm font-semibold leading-tight text-[#1f2937]">
                  {repairMojibakeText(conversation.customerName) ||
                    t("conversation.customerFallback", {
                      id: conversation.customerId || conversation.id,
                    })}
                </p>
                <p className="mt-0.5 truncate text-xs text-[#7a8599]">
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
              <p className="truncate font-semibold text-[14px] leading-tight text-[#1f2937]">
                {t("conversation.workbenchTitle")}
              </p>
              <p className="mt-0.5 truncate text-[14px] text-[#7a8599] sm:text-[14px] lg:hidden">
                {t("conversation.openMenuSelectConversation")}
              </p>
              <p className="mt-0.5 hidden truncate text-[12px] text-[#7a8599] lg:block">
                {t("conversation.selectConversationFromSidebar")}
              </p>
            </div>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-1 sm:gap-1.5">
          {conversation && conversation.needHumanFollowUp ? (
            <Badge className="hidden rounded-md bg-[#fff1f2] px-2 text-xs font-normal text-destructive shadow-none sm:inline-flex">
              待人工
            </Badge>
          ) : conversation ? (
            <Badge className="hidden rounded-md bg-[#eef4ff] px-2 text-xs font-normal text-[#2855d9] shadow-none sm:inline-flex">
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
              <DropdownMenuItem
                onClick={() => setCreateTicketOpen(true)}
                disabled={!conversation}
              >
                <FilePlus2Icon />
                {t("conversation.createTicket")}
              </DropdownMenuItem>
              <DropdownMenuItem
                onClick={() => setTransferOpen(true)}
                disabled={!conversation || conversation.status !== 3}
              >
                <ArrowRightLeftIcon />
                {t("conversation.transferConversation")}
              </DropdownMenuItem>
              <DropdownMenuItem
                onClick={() => setCloseOpen(true)}
                disabled={!conversation || conversation.status === 4}
              >
                <CircleXIcon />
                {t("conversation.closeConversation")}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
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
        <div className="flex h-full min-h-0 overflow-hidden rounded-2xl bg-[#edf1f6] shadow-[inset_0_0_0_1px_rgba(255,255,255,0.78)]">
        <ChatPanel
          wxWorkInstance={conversationInstance}
          onWxWorkInstanceUpdated={handleInstanceUpdated}
        />
        </div>
      </div>
    </div>
  );

  return (
    <div className="flex h-[calc(100dvh-var(--header-height))] min-h-0 w-full min-w-0 flex-col overflow-hidden bg-[#f6f8fb] p-0 lg:h-full lg:p-3">
      {mobileMenuOpen && (
        <button
          type="button"
          aria-label={t("conversation.closeConversationList")}
          className="fixed top-12 right-0 bottom-0 left-0 z-30 bg-black/50 lg:hidden"
          onClick={() => setMobileMenuOpen(false)}
        />
      )}
      <div
        className={`fixed top-12 bottom-0 left-0 z-40 flex w-[min(22rem,calc(100vw-0.75rem))] max-w-[min(22rem,calc(100vw-0.75rem))] flex-col overflow-hidden border-r border-[#dbe7f6] bg-white text-card-foreground shadow-xl transition-transform duration-300 ease-out will-change-transform touch-manipulation overscroll-contain supports-[padding:max(0px)]:pb-[env(safe-area-inset-bottom)] lg:hidden ${
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
      <div className="hidden min-h-0 w-full flex-1 grid-cols-[288px_360px_minmax(0,1fr)] overflow-hidden rounded-[22px] border border-[#e3e9f2] bg-white shadow-[0_18px_48px_rgba(31,41,55,0.08)] lg:grid xl:grid-cols-[320px_390px_minmax(0,1fr)]">
        <div className="col-span-2 min-h-0 border-r border-[#e5e9f2] bg-white">
          {renderConversationSidebar()}
        </div>
        <div className="min-h-0 bg-[#eef2f7]">
          {workspaceContent}
        </div>
      </div>
      <ConversationTransferDialog
        open={transferOpen}
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
        open={closeOpen}
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
        open={createTicketOpen}
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
      <Sheet open={accountManagerOpen} onOpenChange={setAccountManagerOpen}>
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
            <WxWorkProtocolInstanceManager
              layout="fragment"
              tableShellClassName="max-h-[70vh] overflow-auto"
              onChanged={() => void loadWxWorkInstances()}
            />
          </div>
        </SheetContent>
      </Sheet>
      <Dialog open={scanLoginOpen} onOpenChange={handleScanLoginOpenChange}>
        <DialogContent className="agentdesk-surface rounded-2xl sm:max-w-md">
          <DialogHeader>
            <DialogTitle>扫码新增企微员工号</DialogTitle>
            <DialogDescription>
              系统会先向 wework 协议服务创建真实登录二维码；扫码成功后，再到账号设置里绑定门店、知识库和客服组。
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4">
            <div className="agentdesk-subtle-surface flex min-h-64 items-center justify-center rounded-2xl bg-[#f8fbff] p-4">
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
                  暂无二维码，请重新生成
                </div>
              )}
            </div>
            <div className="rounded-xl border border-[#dbe7f6] bg-[#f6f9ff] p-3 text-xs text-muted-foreground shadow-inner shadow-blue-100/30">
              <div className="font-medium text-foreground">{scanLoginStatus}</div>
              {scanLoginResult?.instance.guid ? (
                <div className="mt-1 break-all">实例 GUID：{scanLoginResult.instance.guid}</div>
              ) : null}
              {scanLoginResult?.qrcodeContent ? (
                <div className="mt-1 break-all">二维码内容：{scanLoginResult.qrcodeContent}</div>
              ) : null}
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setAccountManagerOpen(true)}>
              账号设置
            </Button>
            <Button onClick={() => void startScanLogin()} disabled={scanLoginLoading}>
              {scanLoginLoading ? "生成中..." : "重新生成"}
            </Button>
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
              <div className="text-sm font-medium">新的转人工请求</div>
              <div className="mt-1 line-clamp-2 text-xs text-muted-foreground">
                {repairMojibakeText(handoffConversation.customerName) || `会话 #${handoffConversation.id}`}：
                {repairMojibakeText(handoffConversation.handoffReason) || repairMojibakeText(handoffConversation.lastMessageSummary) || "等待同事处理"}
              </div>
              <div className="mt-3 flex items-center gap-2">
                <Button
                  size="sm"
                  className="h-8 rounded-lg"
                  onClick={() => {
                    setSelectedWxWorkInstanceId(handoffConversation.wxWorkInstanceId || null);
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
