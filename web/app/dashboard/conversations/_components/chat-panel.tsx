"use client";

import { memo, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { toast } from "sonner";

import { ConversationTransferDialog } from "@/components/conversation-actions/transfer-dialog";
import { ImMessageHTML } from "@/components/im-message-html";
import { useImageLightbox } from "@/components/image-lightbox";
import { useI18n } from "@/i18n/provider";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";
import { useIsLgUp } from "@/hooks/use-lg-media";
import {
  assignAgentConversation,
  type AgentMessage,
} from "@/lib/api/agent";
import {
  inviteWxWorkProtocolRoomMember,
  setWxWorkProtocolAIReplyEnabled,
  type WxWorkProtocolInstance,
} from "@/lib/api/admin";
import { readSession } from "@/lib/auth";
import { renderIMMessageHTML } from "@/lib/im-message";
import {
  agentConversationSelectors,
  useAgentConversationsStore,
  type AgentConversationFilterKey,
} from "@/lib/stores/agent-conversations";
import { formatDateTime } from "@/lib/utils";
import { AgentMessageEditor } from "./agent-message-editor";

const EMPTY_AGENT_MESSAGES: AgentMessage[] = [];

type ChatPanelProps = {
  wxWorkInstance?: WxWorkProtocolInstance | null;
  onWxWorkInstanceUpdated?: (instance: WxWorkProtocolInstance) => void;
};

export function ChatPanel({ wxWorkInstance, onWxWorkInstanceUpdated }: ChatPanelProps) {
  const t = useI18n();
  const conversation = useAgentConversationsStore(
    agentConversationSelectors.selectedConversation,
  );
  const messages =
    useAgentConversationsStore((state) => state.messages) ??
    EMPTY_AGENT_MESSAGES;
  const loading = useAgentConversationsStore((state) => state.messagesLoading);
  const sending = useAgentConversationsStore((state) => state.sending);
  const uploadingAsset = useAgentConversationsStore(
    (state) => state.uploadingAsset,
  );
  const sendMessage = useAgentConversationsStore((state) => state.sendMessage);
  const uploadImage = useAgentConversationsStore((state) => state.uploadImage);
  const sendImage = useAgentConversationsStore((state) => state.sendImage);
  const sendAttachment = useAgentConversationsStore((state) => state.sendAttachment);
  const markSelectedConversationRead = useAgentConversationsStore(
    (state) => state.markSelectedConversationRead,
  );
  const recallMessage = useAgentConversationsStore((state) => state.recallMessage);
  const recallingMessageId = useAgentConversationsStore(
    (state) => state.recallingMessageId,
  );
  const loadConversations = useAgentConversationsStore((state) => state.loadConversations);
  const loadMessages = useAgentConversationsStore((state) => state.loadMessages);
  const loadOlderMessages = useAgentConversationsStore(
    (state) => state.loadOlderMessages,
  );
  const messagesHasMore = useAgentConversationsStore(
    (state) => state.messagesHasMore,
  );
  const messagesLoadingMore = useAgentConversationsStore(
    (state) => state.messagesLoadingMore,
  );
  const conversationFilter = useAgentConversationsStore((state) => state.conversationFilter);
  const setConversationFilter = useAgentConversationsStore(
    (state) => state.setConversationFilter,
  );
  const messagesContainerRef = useRef<HTMLDivElement>(null);
  const messagesContentRef = useRef<HTMLDivElement>(null);
  const scrollBottomRafRef = useRef<number | null>(null);
  const shouldStickToBottomRef = useRef(true);
  const prependScrollAnchorRef = useRef<{ height: number; top: number } | null>(
    null,
  );
  const [claiming, setClaiming] = useState(false);
  const [savingAIReply, setSavingAIReply] = useState(false);
  const [claimDialogOpen, setClaimDialogOpen] = useState(false);
  const [transferDialogOpen, setTransferDialogOpen] = useState(false);
  const [groupInviteDialogOpen, setGroupInviteDialogOpen] = useState(false);
  const [groupInviteUsers, setGroupInviteUsers] = useState("");
  const [invitingGroupMembers, setInvitingGroupMembers] = useState(false);
  const isLgUp = useIsLgUp();
  const isClosedConversation = conversation?.status === 4;
  const isPendingConversation = conversation?.status === 2;
  const isHandoffPending =
    conversation?.routeStatus === "HQ_AGENTDESK_PENDING" ||
    conversation?.needHumanFollowUp;
  const isHandoffServing = conversation?.routeStatus === "HQ_AGENTDESK_SERVING";
  const routeStatus = conversation?.routeStatus;
  const isAIServing = !routeStatus || routeStatus === "AI_SERVING" || routeStatus === "AI_FALLBACK";
  const aiReplyEnabled = wxWorkInstance?.aiReplyEnabled !== false;
  const canAgentReply =
    !isClosedConversation &&
    !isPendingConversation &&
    (routeStatus === "STORE_WECOM_MANUAL" ||
      routeStatus === "HQ_AGENTDESK_SERVING" ||
      (isAIServing && !aiReplyEnabled));
  const showBottomEditor = !isClosedConversation && !isPendingConversation;
  const currentUserId = readSession()?.user?.id ?? 0;
  const protocolRoomID = getProtocolRoomID(conversation?.wxWorkExternalUserId);

  const switchToMyActiveIfNeeded = () => {
    if (conversationFilter !== "pending") {
      return;
    }
    setConversationFilter("active" satisfies AgentConversationFilterKey);
  };

  const getViewport = useCallback(
    () => messagesContainerRef.current,
    [],
  );

  const isNearBottom = useCallback(
    (element: HTMLElement, threshold = 80) =>
      element.scrollHeight - element.scrollTop - element.clientHeight <=
      threshold,
    [],
  );

  const scrollToBottom = useCallback(() => {
    const viewport = getViewport();
    if (!viewport) {
      return;
    }
    viewport.scrollTop = viewport.scrollHeight;
  }, [getViewport]);

  /**
   * Match the widget message list: keep scrolling for a few frames until
   * scrollHeight stabilizes, which prevents stacked scroll jumps.
   */
  const scheduleScrollToBottom = useCallback(
    (attempts = 4) => {
      if (scrollBottomRafRef.current !== null) {
        cancelAnimationFrame(scrollBottomRafRef.current);
      }
      const run = (remaining: number, previousHeight = -1) => {
        scrollBottomRafRef.current = requestAnimationFrame(() => {
          const viewport = getViewport();
          if (!viewport) {
            scrollBottomRafRef.current = null;
            return;
          }
          const currentHeight = viewport.scrollHeight;
          scrollToBottom();
          if (remaining > 1 && currentHeight !== previousHeight) {
            run(remaining - 1, currentHeight);
            return;
          }
          scrollBottomRafRef.current = null;
        });
      };
      run(attempts);
    },
    [getViewport, scrollToBottom],
  );

  const handleImageSettled = useCallback(() => {
    if (!shouldStickToBottomRef.current) {
      return;
    }
    scheduleScrollToBottom();
  }, [scheduleScrollToBottom]);

  const maybeMarkConversationRead = useCallback(() => {
    const viewport = getViewport();
    if (!viewport || !conversation || loading) {
      return;
    }
    if (
      typeof document !== "undefined" &&
      document.visibilityState !== "visible"
    ) {
      return;
    }
    if (!isNearBottom(viewport)) {
      return;
    }
    void markSelectedConversationRead().catch((error) => {
      toast.error(error instanceof Error ? error.message : t("conversation.markReadFailed"));
    });
  }, [
    conversation,
    getViewport,
    isNearBottom,
    loading,
    markSelectedConversationRead,
    t,
  ]);

  useEffect(() => {
    const viewport = getViewport();
    if (!viewport) {
      return;
    }

    const handleScroll = () => {
      shouldStickToBottomRef.current = isNearBottom(viewport);
      if (shouldStickToBottomRef.current) {
        maybeMarkConversationRead();
      }
    };

    handleScroll();
    viewport.addEventListener("scroll", handleScroll);
    return () => {
      viewport.removeEventListener("scroll", handleScroll);
    };
  }, [conversation?.id, getViewport, isNearBottom, maybeMarkConversationRead]);

  useLayoutEffect(() => {
    shouldStickToBottomRef.current = true;
    scheduleScrollToBottom();
    return () => {
      if (scrollBottomRafRef.current !== null) {
        cancelAnimationFrame(scrollBottomRafRef.current);
        scrollBottomRafRef.current = null;
      }
    };
  }, [conversation?.id, scheduleScrollToBottom]);

  useLayoutEffect(() => {
    const viewport = getViewport();
    if (!viewport) {
      return;
    }
    const anchor = prependScrollAnchorRef.current;
    if (anchor) {
      prependScrollAnchorRef.current = null;
      const nextHeight = viewport.scrollHeight;
      viewport.scrollTop = nextHeight - anchor.height + anchor.top;
      return;
    }
    if (shouldStickToBottomRef.current) {
      scheduleScrollToBottom();
    }
  }, [messages, getViewport, scheduleScrollToBottom]);

  useEffect(() => {
    const content = messagesContentRef.current;
    if (!content) {
      return;
    }

    const observer = new ResizeObserver(() => {
      if (!shouldStickToBottomRef.current) {
        return;
      }
      scheduleScrollToBottom();
    });

    observer.observe(content);
    return () => {
      observer.disconnect();
    };
  }, [conversation?.id, scheduleScrollToBottom]);

  useEffect(() => {
    maybeMarkConversationRead();
  }, [maybeMarkConversationRead, messages.length]);

  useEffect(() => {
    const handleVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        maybeMarkConversationRead();
      }
    };
    const handleFocus = () => {
      maybeMarkConversationRead();
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);
    window.addEventListener("focus", handleFocus);
    return () => {
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      window.removeEventListener("focus", handleFocus);
    };
  }, [maybeMarkConversationRead]);

  const handleLoadOlder = async () => {
    const viewport = getViewport();
    if (!viewport || messagesLoadingMore || !messagesHasMore) {
      return;
    }
    prependScrollAnchorRef.current = {
      height: viewport.scrollHeight,
      top: viewport.scrollTop,
    };
    try {
      await loadOlderMessages();
    } catch (error) {
      prependScrollAnchorRef.current = null;
      toast.error(error instanceof Error ? error.message : t("conversation.loadHistoryFailed"));
    }
  };

  const handleSend = async (html: string) => {
    if (!conversation || sending || isClosedConversation) return;
    try {
      shouldStickToBottomRef.current = true;
      await sendMessage(html);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("conversation.sendMessageFailed"));
    }
  };

  const handleClaim = async () => {
    if (!conversation || claiming) return;
    const session = readSession();
    if (!session?.user?.id) {
      toast.error(t("conversation.claimRequiresSignIn"));
      return;
    }

    setClaiming(true);
    try {
      await assignAgentConversation(
        conversation.id,
        session.user.id,
        isHandoffPending
          ? t("conversation.manualHandoffClaimReason")
          : t("conversation.claimReason"),
      );

      switchToMyActiveIfNeeded();
      setClaimDialogOpen(false);
      toast.success(t("conversation.claimSuccess"));
      await reloadConversationData(conversation.id);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("conversation.claimFailed"));
    } finally {
      setClaiming(false);
    }
  };

  const reloadConversationData = async (conversationId: number) => {
    await loadConversations();
    await loadMessages(conversationId, { forceLoading: true, reset: true });
  };

  const handleToggleAIReply = async (enabled: boolean) => {
    if (!wxWorkInstance || savingAIReply) {
      return;
    }
    setSavingAIReply(true);
    try {
      await setWxWorkProtocolAIReplyEnabled(wxWorkInstance.id, enabled);
      onWxWorkInstanceUpdated?.({ ...wxWorkInstance, aiReplyEnabled: enabled });
      if (conversation) {
        await reloadConversationData(conversation.id);
      }
      toast.success(enabled ? t("conversation.aiReplyEnabled") : t("conversation.aiReplyDisabled"));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("conversation.aiReplyUpdateFailed"));
    } finally {
      setSavingAIReply(false);
    }
  };

  const handleInviteGroupMembers = async () => {
    if (!wxWorkInstance?.id) {
      toast.error("当前会话未绑定企微员工号");
      return;
    }
    if (!protocolRoomID) {
      toast.error("当前不是群聊会话，不能邀请群成员");
      return;
    }
    const userList = groupInviteUsers
      .split(/[\n,，;；\s]+/)
      .map((item) => item.trim())
      .filter(Boolean);
    if (userList.length === 0) {
      toast.error("请填写要邀请的成员ID");
      return;
    }
    setInvitingGroupMembers(true);
    try {
      await inviteWxWorkProtocolRoomMember({
        id: wxWorkInstance.id,
        roomId: protocolRoomID,
        userList,
      });
      toast.success("群邀请已提交");
      setGroupInviteDialogOpen(false);
      setGroupInviteUsers("");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "群邀请失败");
    } finally {
      setInvitingGroupMembers(false);
    }
  };

  if (!conversation) {
    return (
      <div className="mt-10 flex flex-1 items-center justify-center px-4">
        <div className="text-center text-muted-foreground">
          <p className="text-lg">{t("conversation.empty")}</p>
          <p className="mt-1 text-sm lg:hidden">
            {t("conversation.noConversationMobile")}
          </p>
          <p className="mt-1 hidden text-sm lg:block">
            {t("conversation.selectConversationToChat")}
          </p>
        </div>
      </div>
    );
  }

  const messagesScroll = (
    <div
      ref={messagesContainerRef}
      className="h-full min-h-0 flex-1 overflow-y-auto p-4 agent-desk-scrollbar"
    >
      {isHandoffPending || isHandoffServing ? (
        <div className={`mb-3 rounded-md border px-3 py-2 text-xs ${
          isHandoffPending
            ? "border-destructive/25 bg-destructive/5 text-destructive"
            : "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-500/30 dark:bg-emerald-500/15 dark:text-emerald-300"
        }`}>
          {isHandoffPending
            ? t("conversation.manualHandoffPendingNotice")
            : t("conversation.manualHandoffServingNotice")}
        </div>
      ) : null}
      <div ref={messagesContentRef} className="flex flex-col">
        {!loading && messages.length > 0 && messagesHasMore ? (
          <div className="mb-4 flex justify-center">
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={messagesLoadingMore}
              onClick={() => void handleLoadOlder()}
            >
              {messagesLoadingMore ? t("conversation.loading") : t("conversation.loadOlder")}
            </Button>
          </div>
        ) : null}
        {loading ? (
          <div className="py-8 text-center text-sm text-muted-foreground">
            {t("conversation.loading")}
          </div>
        ) : messages.length > 0 ? (
          messages.map((message) => (
            <MessageItem
              key={message.id}
              message={message}
              onImageSettled={handleImageSettled}
              canRecall={message.senderType === "agent" && message.senderId === currentUserId}
              recalling={recallingMessageId === message.id}
              onRecall={async (messageId) => {
                await recallMessage(messageId);
              }}
            />
          ))
        ) : (
          <div className="py-8 text-center text-sm text-muted-foreground">
            {t("conversation.emptyMessages")}
          </div>
        )}
      </div>
    </div>
  );

  const bottomPanel = (
    <div className="h-full overflow-auto border-t border-border/80 bg-card">
      {isClosedConversation ? (
        <div className="h-full flex justify-center items-center">
          {t("conversation.closedNotice")}
        </div>
      ) : isPendingConversation ? (
        <div className="h-full flex justify-center items-center">
          <div className="flex items-center gap-2 h-full">
            <Button
              onClick={() => setClaimDialogOpen(true)}
              disabled={claiming}
              size="sm"
            >
              {claiming
                ? t("conversation.claiming")
                : isHandoffPending
                  ? t("conversation.claimHandoff")
                  : t("conversation.claim")}
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex h-full min-h-0 flex-col">
          <div className="min-h-0 flex-1">
            <AgentMessageEditor
              disabled={!conversation || sending || !canAgentReply}
              uploadingAsset={uploadingAsset}
              aiReplyEnabled={aiReplyEnabled}
              canAgentReply={canAgentReply}
              disabledReason={
                !conversation
                  ? "请选择一个会话"
                  : isAIServing && aiReplyEnabled
                    ? "AI 回复已开启。关闭后可由网页端直接回复。"
                    : "当前会话暂不可回复"
              }
              aiReplyToggleDisabled={!wxWorkInstance || savingAIReply}
              onToggleAIReply={handleToggleAIReply}
              onSend={handleSend}
              onUploadImage={async (file) => {
                shouldStickToBottomRef.current = true;
                const uploaded = await uploadImage(file);
                return uploaded;
              }}
              onSendImage={async (file) => {
                shouldStickToBottomRef.current = true;
                try {
                  await sendImage(file);
                } catch (error) {
                  toast.error(error instanceof Error ? error.message : t("conversation.sendImageFailed"));
                }
              }}
              onSendAttachment={async (file) => {
                shouldStickToBottomRef.current = true;
                try {
                  await sendAttachment(file);
                } catch (error) {
                  toast.error(error instanceof Error ? error.message : t("conversation.sendAttachmentFailed"));
                }
              }}
              onOpenGroupInvite={() => setGroupInviteDialogOpen(true)}
            />
          </div>
        </div>
      )}
    </div>
  );

  return (
    <div className="flex h-full min-h-0 flex-1 flex-col overflow-hidden">
      {isLgUp ? (
        <ResizablePanelGroup
          orientation="vertical"
          className="flex min-h-0 flex-1 flex-col"
        >
          <ResizablePanel
            defaultSize={showBottomEditor ? "72%" : "82%"}
            minSize="35%"
            className="min-h-0"
          >
            {messagesScroll}
          </ResizablePanel>
          <ResizableHandle withHandle />
          <ResizablePanel
            defaultSize={showBottomEditor ? "28%" : "18%"}
            minSize={showBottomEditor ? "18%" : "12%"}
            maxSize={showBottomEditor ? "55%" : "30%"}
            className="min-h-0"
          >
            {bottomPanel}
          </ResizablePanel>
        </ResizablePanelGroup>
      ) : (
        <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
          <div className="min-h-0 flex-1">{messagesScroll}</div>
          <div className="shrink-0 pb-[env(safe-area-inset-bottom)] lg:pb-0">
            {bottomPanel}
          </div>
        </div>
      )}
      <Dialog
        open={claimDialogOpen}
        onOpenChange={(open) => {
          if (claiming) {
            return;
          }
          setClaimDialogOpen(open);
        }}
      >
        <DialogContent className="max-w-md" showCloseButton={false}>
          <DialogHeader>
            <DialogTitle>
              {isHandoffPending
                ? t("conversation.claimHandoffTitle")
                : t("conversation.claimTitle")}
            </DialogTitle>
            <DialogDescription>
              {conversation
                ? `${t("conversation.claimConfirmPrefix")}${
                    conversation.customerName ||
                    `${t("conversation.customerFallbackPrefix")}${conversation.customerId || conversation.id}`
                  }${t("conversation.claimConfirmSuffix")}`
                : t("conversation.claimCurrent")}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={claiming}
              onClick={() => setClaimDialogOpen(false)}
            >
              {t("conversation.cancel")}
            </Button>
            <Button
              type="button"
              disabled={claiming}
              onClick={() => void handleClaim()}
            >
              {claiming
                ? t("conversation.claiming")
                : isHandoffPending
                  ? t("conversation.confirmClaimHandoff")
                  : t("conversation.confirmClaim")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <ConversationTransferDialog
        open={transferDialogOpen}
        mode="transfer"
        conversationId={conversation.id}
        onOpenChange={setTransferDialogOpen}
        onSuccess={async () => {
          await reloadConversationData(conversation.id);
        }}
      />
      <Dialog open={groupInviteDialogOpen} onOpenChange={setGroupInviteDialogOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>邀请群成员</DialogTitle>
            <DialogDescription>
              按企微协议 SAAS 的 /room/invite_room_member 接口提交，参数为 guid、room_id、user_list。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="rounded-md border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
              群ID：{protocolRoomID || "当前不是群聊会话"}
            </div>
            <Textarea
              value={groupInviteUsers}
              onChange={(event) => setGroupInviteUsers(event.target.value)}
              placeholder="一行一个成员ID，也支持逗号、空格分隔"
              disabled={invitingGroupMembers || !protocolRoomID}
              className="min-h-28"
            />
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={invitingGroupMembers}
              onClick={() => setGroupInviteDialogOpen(false)}
            >
              取消
            </Button>
            <Button
              type="button"
              disabled={invitingGroupMembers || !protocolRoomID}
              onClick={() => void handleInviteGroupMembers()}
            >
              {invitingGroupMembers ? "提交中..." : "邀请"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function getProtocolRoomID(externalUserId?: string) {
  const value = (externalUserId ?? "").trim();
  if (!value) {
    return "";
  }
  if (value.startsWith("R:")) {
    return value.slice(2).trim();
  }
  if (value.includes("@chatroom") || value.includes("@openim")) {
    return value;
  }
  return "";
}

type MessageItemProps = {
  message: AgentMessage;
  onImageSettled: () => void;
  canRecall: boolean;
  recalling: boolean;
  onRecall: (messageId: number) => Promise<void>;
};

const MessageItem = memo(
  function MessageItem({
    message,
    onImageSettled,
    canRecall,
    recalling,
    onRecall,
  }: MessageItemProps) {
    const t = useI18n();
    const { open: openImageLightbox } = useImageLightbox();
    const isCustomer = message.senderType === "customer";
    const isAi = message.senderType === "ai";
    const isAgentSide = message.senderType === "agent" || isAi;
    const isRecalled = Boolean(message.recalledAt) || message.sendStatus === 6;
    const senderName = isCustomer
      ? message.senderName || t("conversation.customerSender")
      : isAi
        ? "AI"
        : message.senderName || t("conversation.agentSender");
    const agentAvatarSrc =
      isAgentSide && !isAi && message.senderAvatar?.trim()
        ? message.senderAvatar.trim()
        : undefined;
    const avatarFallback = isAi ? "AI" : senderName.charAt(0);
    const htmlContent = isRecalled
      ? `<p>${t("conversation.messageRecalledHtml")}</p>`
      : buildMessageHTML(message);
    const bubbleClassName = isAi
      ? "border border-primary/15 bg-primary/5 text-foreground shadow-sm"
      : isAgentSide
        ? "bg-emerald-600 text-white shadow-sm"
        : "border border-border/70 bg-muted/60 text-foreground shadow-sm";
    const htmlClassName = isAi
      ? "[&_a]:text-foreground [&_a]:underline [&_img]:rounded-md"
      : isAgentSide
        ? "[&_p]:text-white [&_a]:text-white [&_a]:underline [&_img]:rounded-md"
        : "[&_a]:text-foreground [&_a]:underline [&_img]:rounded-md";
    const avatarClassName = isAi
      ? "border border-primary/20 bg-primary/10 text-xs text-foreground"
      : isAgentSide
        ? "bg-emerald-600 text-xs text-white"
        : "border border-border/70 bg-muted/60 text-xs text-foreground";
    const recalledBubbleClassName = isAgentSide
      ? "border border-dashed border-emerald-200 bg-emerald-50 text-emerald-800"
      : "border border-dashed border-border/70 bg-muted/40 text-muted-foreground";
    const recalledHtmlClassName = isAgentSide
      ? "[&_p]:text-emerald-800"
      : "[&_p]:text-muted-foreground";
    const showRecallAction = canRecall && !isRecalled;

    return (
      <div
        className={`mb-4 flex items-start gap-2 ${
          isAgentSide ? "justify-end" : "justify-start"
        }`}
      >
        {isAgentSide ? (
          <>
            <div className="flex max-w-[70%] flex-col items-end">
              <div className="mb-1 text-xs text-muted-foreground">
                {senderName}
              </div>
              <div
                className={`w-fit rounded-2xl px-3 py-2 text-left ${
                  isRecalled ? recalledBubbleClassName : bubbleClassName
                }`}
              >
                <ImMessageHTML
                  html={htmlContent}
                  className={isRecalled ? recalledHtmlClassName : htmlClassName}
                  onImageSettled={onImageSettled}
                  onImageClick={isRecalled ? undefined : openImageLightbox}
                />
              </div>
              <div className="mt-1 flex items-center gap-2 text-xs text-muted-foreground">
                <span>{formatDateTime(message.sentAt || "")}</span>
                {isRecalled ? <span>{t("conversation.messageRecalled")}</span> : null}
                {message.sendStatus === 2 && !isRecalled && (
                  <span>
                    {message.customerRead
                      ? t("conversation.customerRead")
                      : t("conversation.customerUnread")}
                  </span>
                )}
                {showRecallAction ? (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="h-auto px-1 py-0 text-xs text-muted-foreground"
                    disabled={recalling}
                    onClick={() => {
                      void onRecall(message.id).catch((error) => {
                        toast.error(error instanceof Error ? error.message : t("conversation.recallFailed"));
                      });
                    }}
                  >
                    {recalling ? t("conversation.recalling") : t("conversation.recall")}
                  </Button>
                ) : null}
              </div>
            </div>
            <Avatar className="size-8 shrink-0">
              <AvatarImage src={agentAvatarSrc ?? ""} />
              <AvatarFallback className={avatarClassName}>
                {avatarFallback}
              </AvatarFallback>
            </Avatar>
          </>
        ) : (
          <>
            <Avatar className="size-8 shrink-0">
              <AvatarImage src="" />
              <AvatarFallback className={avatarClassName}>
                {t("conversation.customerAvatar")}
              </AvatarFallback>
            </Avatar>
            <div className="max-w-[70%]">
              <div className="mb-1 text-xs text-muted-foreground">
                {senderName}
              </div>
              <div
                className={`w-fit rounded-2xl px-3 py-2 ${
                  isRecalled ? recalledBubbleClassName : bubbleClassName
                }`}
              >
                <ImMessageHTML
                  html={htmlContent}
                  className={isRecalled ? recalledHtmlClassName : htmlClassName}
                  onImageSettled={onImageSettled}
                  onImageClick={isRecalled ? undefined : openImageLightbox}
                />
              </div>
              <div className="mt-1 flex items-center gap-2 text-xs text-muted-foreground">
                <span>{formatDateTime(message.sentAt || "")}</span>
                {isRecalled ? <span>{t("conversation.messageRecalled")}</span> : null}
              </div>
            </div>
          </>
        )}
      </div>
    );
  },
  (prevProps, nextProps) =>
    prevProps.message === nextProps.message &&
    prevProps.onImageSettled === nextProps.onImageSettled &&
    prevProps.canRecall === nextProps.canRecall &&
    prevProps.recalling === nextProps.recalling &&
    prevProps.onRecall === nextProps.onRecall,
);

function buildMessageHTML(message: {
  messageType: string;
  content: string;
  payload?: string;
}) {
  return renderIMMessageHTML(message);
}
