"use client";

import {
  ArrowRightLeftIcon,
  ChevronsUpDown,
  CircleUserRoundIcon,
  CircleXIcon,
  FilePlus2Icon,
  MessageCircleWarningIcon,
  Menu,
  MoreHorizontalIcon,
  PlusIcon,
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
import { Input } from "@/components/ui/input";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { WxWorkProtocolInstanceManager } from "@/components/wxwork-protocol/wxwork-protocol-instance-manager";
import { useAgentConversationRealtime } from "@/hooks/use-agent-conversation-realtime";
import { useI18n } from "@/i18n/provider";
import { fetchWxWorkProtocolInstances, type WxWorkProtocolInstance } from "@/lib/api/admin";
import {
  agentConversationFilterOptions,
  agentConversationSelectors,
  type AgentConversationFilterKey,
  useAgentConversationsStore,
} from "@/lib/stores/agent-conversations";
import { CreateTicketFromConversationDialog } from "../tickets/_components/create-ticket-from-conversation-dialog";
import { ChatPanel } from "./_components/chat-panel";
import { ConversationInfoPanel } from "./_components/conversation-info-panel";
import { ConversationList } from "./_components/conversation-list";

const workbenchIconButtonClassName =
  "size-8 text-muted-foreground hover:bg-muted hover:text-foreground";

function getCustomerOnlineClassName(online?: boolean) {
  return online
    ? "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-500/30 dark:bg-emerald-500/15 dark:text-emerald-300"
    : "border-border bg-muted text-muted-foreground";
}

function getCustomerOnlineDotClassName(online?: boolean) {
  return online ? "bg-emerald-500" : "bg-muted-foreground/70";
}

export default function ConversationsPage() {
  const t = useI18n();
  const conversation = useAgentConversationsStore(
    agentConversationSelectors.selectedConversation,
  );
  const conversationFilter = useAgentConversationsStore(
    (state) => state.conversationFilter,
  );
  const conversations = useAgentConversationsStore((state) => state.conversations);
  const selectedWxWorkInstanceId = useAgentConversationsStore(
    (state) => state.selectedWxWorkInstanceId,
  );
  const setSelectedWxWorkInstanceId = useAgentConversationsStore(
    (state) => state.setSelectedWxWorkInstanceId,
  );
  const setConversationFilter = useAgentConversationsStore(
    (state) => state.setConversationFilter,
  );
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
  const [instances, setInstances] = useState<WxWorkProtocolInstance[]>([]);
  const [accountKeyword, setAccountKeyword] = useState("");
  const [handoffToastDismissedId, setHandoffToastDismissedId] = useState<number | null>(null);
  const filterContainerRef = useRef<HTMLDivElement | null>(null);
  const filterMeasureRef = useRef<HTMLDivElement | null>(null);
  const [showFilterDropdown, setShowFilterDropdown] = useState(false);

  useEffect(() => {
    const container = filterContainerRef.current;
    const measure = filterMeasureRef.current;
    if (!container || !measure) {
      return;
    }

    const updateFilterMode = () => {
      setShowFilterDropdown(measure.scrollWidth > container.clientWidth);
    };

    updateFilterMode();

    const observer = new ResizeObserver(() => {
      updateFilterMode();
    });

    observer.observe(container);
    observer.observe(measure);

    return () => {
      observer.disconnect();
    };
  }, []);

  const currentFilterOption =
    agentConversationFilterOptions.find((opt) => opt.value === conversationFilter) ??
    agentConversationFilterOptions[0];
  const getFilterLabel = (labelKey: string) => t(labelKey);
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

  useEffect(() => {
    void loadConversations().catch((error) => {
      toast.error(error instanceof Error ? error.message : t("conversation.loadListFailed"));
    });
  }, [loadConversations, conversationFilter, selectedWxWorkInstanceId, t]);

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
      <div className="flex w-72 shrink-0 flex-col border-r border-border/80 bg-muted/20 xl:w-80">
        <div className="border-b border-border/80 px-3 py-3">
          <div className="flex items-center justify-between gap-2">
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold">企微员工号</div>
              <div className="mt-0.5 text-xs text-muted-foreground">账号添加、回调、AI 托管都在这里管理</div>
            </div>
            <Button
              variant="ghost"
              size="icon"
              className="size-8 shrink-0 text-muted-foreground"
              onClick={() => setAccountManagerOpen(true)}
              aria-label="管理企微员工号"
              title="管理企微员工号"
            >
              <SettingsIcon className="size-4" />
            </Button>
          </div>
          <div className="mt-3 grid grid-cols-2 gap-2">
            <Button
              variant="default"
              size="sm"
              className="justify-center gap-2"
              onClick={() => setAccountManagerOpen(true)}
            >
              <PlusIcon className="size-4" />
              新增账号
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="justify-center gap-2"
              onClick={() => setAccountManagerOpen(true)}
            >
              <SettingsIcon className="size-4" />
              账号设置
            </Button>
          </div>
          <div className="mt-3 grid grid-cols-2 gap-2 text-xs">
            <div className="rounded-md border bg-card px-2 py-2">
              <div className="text-muted-foreground">当前会话</div>
              <div className="mt-1 text-lg font-semibold leading-none">{conversations.length}</div>
            </div>
            <div className="rounded-md border bg-card px-2 py-2">
              <div className="text-muted-foreground">待人工</div>
              <div className="mt-1 text-lg font-semibold leading-none text-destructive">{pendingHandoffCount}</div>
            </div>
          </div>
          <div className="relative mt-3">
            <SearchIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={accountKeyword}
              onChange={(event) => setAccountKeyword(event.target.value)}
              placeholder="搜索员工号/门店"
              className="h-8 pl-8 text-xs"
            />
          </div>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-2">
          <button
            type="button"
            className={`mb-2 flex w-full items-center justify-between rounded-md border px-3 py-2 text-left text-sm transition-colors ${
              selectedWxWorkInstanceId === null ? "border-primary bg-primary text-primary-foreground" : "bg-card hover:bg-muted"
            }`}
            onClick={() => {
              setSelectedWxWorkInstanceId(null);
              void loadConversations();
            }}
          >
            <span className="truncate">全部账号</span>
            <span className="text-xs opacity-70">{conversations.length}</span>
          </button>
          {filteredInstances.map((item) => (
            <button
              key={item.id}
              type="button"
              className={`mb-2 w-full rounded-md border px-3 py-2 text-left text-sm transition-colors ${
                selectedWxWorkInstanceId === item.id ? "border-primary bg-primary text-primary-foreground" : "bg-card hover:bg-muted"
              }`}
              onClick={() => {
                setSelectedWxWorkInstanceId(item.id);
                void loadConversations();
              }}
            >
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0 flex-1">
                  <span className="block truncate font-medium">{item.employeeName || item.guid}</span>
                  <span className="mt-0.5 block truncate text-xs opacity-75">
                    {item.storeName || item.employeeUserId || "未绑定门店"}
                  </span>
                </div>
                <span
                  className={`mt-1 size-2 shrink-0 rounded-full ${
                    item.healthStatus === "online" ? "bg-emerald-500" : item.healthStatus === "offline" ? "bg-muted-foreground/50" : "bg-amber-500"
                  }`}
                />
              </div>
              <div className="mt-2 flex items-center gap-1">
                <Badge variant={item.aiReplyEnabled === false ? "outline" : "secondary"} className="h-5 px-1.5 text-[10px]">
                  {item.aiReplyEnabled === false ? "AI停用" : "AI托管"}
                </Badge>
                <Badge variant={item.fallbackToHQ === false ? "outline" : "secondary"} className="h-5 px-1.5 text-[10px]">
                  {item.fallbackToHQ === false ? "门店处理" : "总部兜底"}
                </Badge>
              </div>
            </button>
          ))}
          {filteredInstances.length === 0 ? (
            <div className="rounded-md border border-dashed bg-card px-3 py-6 text-center text-xs text-muted-foreground">
              没有匹配的员工号
            </div>
          ) : null}
        </div>
      </div>
      <div className="flex min-w-0 flex-1 flex-col bg-inherit">
        <div className="flex h-12.5 shrink-0 items-start justify-between gap-2 border-b border-border/80 bg-card px-2 py-2">
          <div ref={filterContainerRef} className="relative min-w-0 flex-1">
            <div className="mb-1 flex items-center justify-between gap-2 px-1 text-xs text-muted-foreground">
              <span className="truncate">
                {selectedInstance
                  ? selectedInstance.storeName || selectedInstance.employeeName || selectedInstance.guid
                  : "全部员工号"}
              </span>
              {pendingHandoffCount > 0 ? (
                <span className="text-destructive">{pendingHandoffCount} 个待处理</span>
              ) : null}
            </div>
            {showFilterDropdown ? (
              <DropdownMenu>
                <DropdownMenuTrigger
                  render={
                    <Button
                      variant="outline"
                      className="h-8.5 w-full min-w-0 justify-between gap-2 px-3 text-xs sm:text-sm"
                    />
                  }
                >
                  <span className="truncate">
                    {currentFilterOption
                      ? getFilterLabel(currentFilterOption.labelKey)
                      : t("conversation.filterPlaceholder")}
                  </span>
                  <ChevronsUpDown className="size-4 shrink-0 text-muted-foreground" />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="start" className="w-44 min-w-44">
                  <DropdownMenuRadioGroup
                    value={conversationFilter}
                    onValueChange={(value) =>
                      setConversationFilter(value as AgentConversationFilterKey)
                    }
                  >
                    {agentConversationFilterOptions.map((opt) => (
                      <DropdownMenuRadioItem key={opt.value} value={opt.value}>
                        {getFilterLabel(opt.labelKey)}
                      </DropdownMenuRadioItem>
                    ))}
                  </DropdownMenuRadioGroup>
                </DropdownMenuContent>
              </DropdownMenu>
            ) : (
              <Tabs
                value={conversationFilter}
                onValueChange={(value) =>
                  setConversationFilter(value as AgentConversationFilterKey)
                }
                className="min-w-0 flex-1 gap-0"
              >
                <TabsList className="w-full min-w-0 justify-start">
                  {agentConversationFilterOptions.map((opt) => (
                    <TabsTrigger
                      key={opt.value}
                      value={opt.value}
                      className="shrink-0 px-2.5 text-xs sm:text-sm"
                    >
                      {getFilterLabel(opt.labelKey)}
                    </TabsTrigger>
                  ))}
                </TabsList>
              </Tabs>
            )}
            <div
              ref={filterMeasureRef}
              className="pointer-events-none absolute whitespace-nowrap opacity-0"
              aria-hidden="true"
            >
              <div className="inline-flex">
                {agentConversationFilterOptions.map((opt) => (
                  <span
                    key={opt.value}
                    className="shrink-0 px-2.5 text-xs sm:text-sm"
                  >
                    {getFilterLabel(opt.labelKey)}
                  </span>
                ))}
              </div>
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
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-hidden bg-card text-card-foreground">
      <div className="flex h-12.5 shrink-0 items-center justify-between gap-3 border-b border-border/80 bg-card px-3 py-1">
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
              <Avatar className="size-8 shrink-0 lg:size-9">
                <AvatarImage src="" />
                <AvatarFallback className="bg-primary/10 text-sm text-primary">
                  {t("conversation.customerAvatar")}
                </AvatarFallback>
              </Avatar>
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <p className="min-w-0 truncate text-sm font-medium leading-tight">
                    {conversation.customerName ||
                      t("conversation.customerFallback", {
                        id: conversation.customerId || conversation.id,
                      })}
                  </p>
                  <span
                    className={`inline-flex shrink-0 items-center gap-1 rounded-md border px-1.5 py-0.5 text-[11px] leading-none ${getCustomerOnlineClassName(
                      conversation.customerOnline,
                    )}`}
                  >
                    <span
                      className={`size-1.5 rounded-full ${getCustomerOnlineDotClassName(
                        conversation.customerOnline,
                      )}`}
                    />
                    {conversation.customerOnline
                      ? t("conversation.customerOnline")
                      : t("conversation.customerOffline")}
                  </span>
                </div>
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
              <p className="truncate font-medium text-[14px] leading-tight">
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
        <div className="flex shrink-0 items-center gap-0.5 sm:gap-1">
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
      <div className="flex min-h-0 w-full flex-1 overflow-hidden">
        <ChatPanel
          wxWorkInstance={conversationInstance}
          onWxWorkInstanceUpdated={handleInstanceUpdated}
        />
      </div>
    </div>
  );

  return (
    <div className="flex h-[calc(100dvh-var(--header-height))] min-h-0 w-full min-w-0 flex-col overflow-hidden lg:h-full">
      {mobileMenuOpen && (
        <button
          type="button"
          aria-label={t("conversation.closeConversationList")}
          className="fixed top-12 right-0 bottom-0 left-0 z-30 bg-black/50 lg:hidden"
          onClick={() => setMobileMenuOpen(false)}
        />
      )}
      <div
        className={`fixed top-12 bottom-0 left-0 z-40 flex w-[min(22rem,calc(100vw-0.75rem))] max-w-[min(22rem,calc(100vw-0.75rem))] flex-col overflow-hidden border-r border-border/80 bg-card text-card-foreground shadow-xl transition-transform duration-300 ease-out will-change-transform touch-manipulation overscroll-contain supports-[padding:max(0px)]:pb-[env(safe-area-inset-bottom)] lg:hidden ${
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
      <div className="hidden min-h-0 w-full flex-1 grid-cols-[288px_360px_minmax(0,1fr)] overflow-hidden lg:grid xl:grid-cols-[320px_390px_minmax(0,1fr)]">
        <div className="col-span-2 min-h-0 border-r border-border/80 bg-card">
          {renderConversationSidebar()}
        </div>
        <div className="min-h-0 bg-card">
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
                customerName: conversation.customerName,
                customerId: conversation.customerId ?? 0,
                lastMessageSummary: conversation.lastMessageSummary,
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
      {handoffConversation && handoffToastDismissedId !== handoffConversation.id ? (
        <div className="fixed right-4 bottom-4 z-50 w-[min(22rem,calc(100vw-2rem))] rounded-md border bg-card p-4 text-card-foreground shadow-lg">
          <div className="flex items-start gap-3">
            <div className="mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-md bg-destructive/10 text-destructive">
              <MessageCircleWarningIcon className="size-5" />
            </div>
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium">新的转人工请求</div>
              <div className="mt-1 line-clamp-2 text-xs text-muted-foreground">
                {handoffConversation.customerName || `会话 #${handoffConversation.id}`}：
                {handoffConversation.handoffReason || handoffConversation.lastMessageSummary || "等待同事处理"}
              </div>
              <div className="mt-3 flex items-center gap-2">
                <Button
                  size="sm"
                  className="h-8"
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
                  className="h-8"
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
