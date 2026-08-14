"use client";

import { Fragment, memo, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  LoaderCircleIcon,
} from "lucide-react";
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
  directTakeoverAgentConversation,
  activateAgentConversationTakeover,
  requestAgentConversationTakeover,
  resumeAgentConversationAI,
  reviewAgentConversationTakeover,
  type AgentMessage,
  type ConversationHistorySegment,
} from "@/lib/api/agent";
import {
  inviteWxWorkProtocolRoomMember,
  type WxWorkProtocolInstance,
} from "@/lib/api/admin";
import { readSession } from "@/lib/auth";
import { renderIMMessageHTML } from "@/lib/im-message";
import { IMMessageStatusLabels } from "@/lib/generated/enums";
import {
  agentConversationSelectors,
  useAgentConversationsStore,
} from "@/lib/stores/agent-conversations";
import { formatDateTime, repairMojibakeText } from "@/lib/utils";
import { AgentMessageEditor } from "./agent-message-editor";

const EMPTY_AGENT_MESSAGES: AgentMessage[] = [];
type TakeoverDialogMode = "request" | "direct" | "activate" | "review" | "resume";

type ChatPanelProps = {
  wxWorkInstance?: WxWorkProtocolInstance | null;
};

export function ChatPanel({
  wxWorkInstance,
}: ChatPanelProps) {
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
  const refreshConversation = useAgentConversationsStore((state) => state.refreshConversation);
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
  const messagesContainerRef = useRef<HTMLDivElement>(null);
  const messagesContentRef = useRef<HTMLDivElement>(null);
  const scrollBottomRafRef = useRef<number | null>(null);
  const shouldStickToBottomRef = useRef(true);
  const prependScrollAnchorRef = useRef<{ height: number; top: number } | null>(
    null,
  );
  const [takeoverDialogMode, setTakeoverDialogMode] = useState<TakeoverDialogMode | null>(null);
  const [takeoverSubmitting, setTakeoverSubmitting] = useState(false);
  const [transferDialogOpen, setTransferDialogOpen] = useState(false);
  const [groupInviteDialogOpen, setGroupInviteDialogOpen] = useState(false);
  const [groupInviteUsers, setGroupInviteUsers] = useState("");
  const [invitingGroupMembers, setInvitingGroupMembers] = useState(false);
  const isLgUp = useIsLgUp();
  const isClosedConversation = conversation?.status === 4;
  const manualAttention = conversation?.manualAttention;
  const hasManualStatus = Boolean(manualAttention && manualAttention.level !== "none");
  const takeoverState = conversation?.takeoverState;
  const replyRouteUnavailable =
    conversation?.wxWorkReplyStatus === "waiting_target_message" ||
    conversation?.wxWorkReplyStatus === "unavailable";
  const canAgentReply =
    !isClosedConversation &&
    !replyRouteUnavailable &&
    takeoverState?.canReply === true;
  // Older conversation detail responses may omit routeStatus. Preserve the
  // historical default of AI service in that case; the backend remains the
  // final permission gate for every send/takeover action.
  const aiReplyServing = Boolean(
    conversation &&
      (!conversation.routeStatus ||
        conversation.routeStatus === "AI_SERVING" ||
        conversation.routeStatus === "AI_FALLBACK"),
  );
  const showBottomEditor = !isClosedConversation;
  const currentUserId = readSession()?.user?.id ?? 0;
  const protocolRoomID = getProtocolRoomID(conversation?.wxWorkExternalUserId);
  const manualStatusNotice = manualAttention?.dot
    ? `${manualAttention.label || "待人工"}，AI 暂停回复。`
    : manualAttention?.level === "serving"
      ? `${manualAttention.label || "人工处理中"}，AI 暂停回复。`
      : "";
  const manualStatusTone =
    manualAttention?.level === "urgent"
      ? "border-destructive/25 bg-destructive/5 text-destructive"
      : manualAttention?.dot
        ? "border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-500/30 dark:bg-amber-500/15 dark:text-amber-300"
        : manualAttention?.level === "serving"
          ? "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-500/30 dark:bg-emerald-500/15 dark:text-emerald-300"
          : "border-border bg-background text-muted-foreground";
  const replyPermissionHint = !conversation
    ? "请选择一个会话"
    : conversation.wxWorkReplyStatus === "waiting_target_message"
      ? "新企微账号尚未收到该客户消息，暂时无法发送。已输入内容会保留。"
      : conversation.wxWorkReplyStatus === "unavailable"
        ? "当前企微账号或会话路由不可用，暂时无法发送。已输入内容会保留。"
        : takeoverState?.pendingForMe
          ? "接管申请正在等待客服组长审批，审批通过后即可回复。"
          : takeoverState?.authorizedForMe
            ? "接管申请已通过，请再次点击右下角 AI 回复开关确认接管。"
            : takeoverState?.authorizedForAnother
              ? "该会话已有申请通过，等待申请人确认接管。"
          : takeoverState?.pendingForAnother
            ? takeoverState.canReview
              ? `${takeoverState.requesterName || "客服"}正在申请接管，请先审核。`
              : "该会话已有其他客服申请接管，请等待组长处理。"
            : takeoverState?.canDirectTakeover
              ? "你可以直接接管该会话，发送前会再次确认。"
              : takeoverState?.canRequest
                ? "主动接管需要客服组长审批，发送时会先发起申请。"
                : takeoverState?.canReply
                  ? ""
                  : "当前账号暂无回复权限。";

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

  const ensureReplyPermission = () => {
    if (!conversation || isClosedConversation) {
      return false;
    }
    if (replyRouteUnavailable) {
      toast.error(replyPermissionHint || "当前会话路由不可用，暂时无法发送。");
      return false;
    }
    if (takeoverState?.canReply) {
      return true;
    }
    if (!takeoverState) {
      toast.error("会话权限状态仍在加载，请稍后重试。");
      void refreshConversation(conversation.id);
      return false;
    }
    if (takeoverState.pendingForMe) {
      toast.info("接管申请正在等待客服组长审批。");
      return false;
    }
    if (takeoverState.authorizedForMe) {
      setTakeoverDialogMode("activate");
      return false;
    }
    if (takeoverState.authorizedForAnother) {
      toast.info("该会话已有申请通过，等待申请人确认接管。");
      return false;
    }
    if (takeoverState.pendingForAnother) {
      if (takeoverState.canReview) {
        setTakeoverDialogMode("review");
      } else {
        toast.info("该会话已有其他客服申请接管，请等待组长处理。");
      }
      return false;
    }
    if (takeoverState.canDirectTakeover) {
      setTakeoverDialogMode("direct");
      return false;
    }
    if (takeoverState.canRequest) {
      setTakeoverDialogMode("request");
      return false;
    }
    toast.error("当前账号暂无接管或回复权限。");
    return false;
  };

  const handleSend = async (html: string): Promise<boolean> => {
    if (!conversation || sending || isClosedConversation || !ensureReplyPermission()) {
      return false;
    }
    try {
      shouldStickToBottomRef.current = true;
      return Boolean(await sendMessage(html));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("conversation.sendMessageFailed"));
      return false;
    }
  };

  const reloadConversationState = async (conversationId: number) => {
    await refreshConversation(conversationId);
    await loadConversations();
  };

  const handleTakeoverSubmit = async (approved = true) => {
    if (!conversation || !takeoverDialogMode || takeoverSubmitting) {
      return;
    }
    setTakeoverSubmitting(true);
    try {
      switch (takeoverDialogMode) {
        case "request":
          await requestAgentConversationTakeover(conversation.id, "申请主动接管会话");
          toast.success("接管申请已提交，请等待客服组长审批。");
          break;
        case "direct":
          await directTakeoverAgentConversation(conversation.id, "客服组长主动接管会话");
          toast.success("会话已接管，可以继续回复。");
          break;
        case "activate":
          if (!takeoverState?.requestId) {
            throw new Error("接管申请已变化，请刷新后重试。");
          }
          await activateAgentConversationTakeover({
            requestId: takeoverState.requestId,
            reason: "确认接管会话",
          });
          toast.success("会话已接管，可以继续回复。");
          break;
        case "review":
          if (!takeoverState?.requestId) {
            throw new Error("接管申请已变化，请刷新后重试。");
          }
          await reviewAgentConversationTakeover({
            requestId: takeoverState.requestId,
            approved,
            remark: approved ? "同意主动接管" : "暂不同意主动接管",
          });
          toast.success(approved ? "接管申请已通过。" : "接管申请已拒绝。");
          break;
        case "resume":
          await resumeAgentConversationAI(conversation.id);
          toast.success("会话已交还 AI 接管。");
          break;
      }
      setTakeoverDialogMode(null);
      await reloadConversationState(conversation.id);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "会话状态更新失败");
    } finally {
      setTakeoverSubmitting(false);
    }
  };

  const handleToggleAIReply = async () => {
    if (
      !conversation ||
      !takeoverState ||
      takeoverSubmitting ||
      sending ||
      uploadingAsset ||
      invitingGroupMembers
    ) {
      return;
    }
    if (takeoverState.canResumeAi) {
      setTakeoverDialogMode("resume");
      return;
    }
    if (takeoverState.authorizedForMe) {
      setTakeoverDialogMode("activate");
      return;
    }
    if (takeoverState.pendingForMe) {
      toast.info("接管申请正在等待客服组长审批。");
      return;
    }
    if (takeoverState.pendingForAnother) {
      if (takeoverState.canReview) {
        setTakeoverDialogMode("review");
      } else {
        toast.info("该会话已有其他客服申请接管，请等待处理。");
      }
      return;
    }
    if (takeoverState.authorizedForAnother) {
      toast.info("该会话已有申请通过，等待申请人确认接管。");
      return;
    }
    if (takeoverState.canDirectTakeover) {
      setTakeoverDialogMode("direct");
      return;
    }
    if (takeoverState.canRequest) {
      setTakeoverDialogMode("request");
      return;
    }
    toast.info("当前账号暂无切换该会话的权限。");
  };

  const reloadConversationData = async (conversationId: number) => {
    await loadConversations();
    await loadMessages(conversationId, { forceLoading: true, reset: true });
  };

  const handleInviteGroupMembers = async () => {
    if (!conversation || !ensureReplyPermission()) {
      return;
    }
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
        conversationId: conversation.id,
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
      <div className="flex flex-1 items-center justify-center bg-muted/40 px-4">
        <div className="max-w-sm rounded-lg border border-border bg-card px-8 py-10 text-center text-muted-foreground shadow-sm">
          <p className="text-base font-semibold text-foreground">{t("conversation.empty")}</p>
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
      className="agent-desk-scrollbar h-full min-h-0 flex-1 overflow-y-auto bg-muted/40 px-5 py-6"
    >
      {hasManualStatus && manualStatusNotice ? (
        <div className={`mx-auto mb-4 max-w-2xl rounded-lg border px-3 py-2 text-xs shadow-none ${manualStatusTone}`}>
          {manualStatusNotice}
        </div>
      ) : null}
      {replyRouteUnavailable ? (
        <div className="mx-auto mb-4 max-w-2xl rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/15 dark:text-amber-200">
          {conversation.wxWorkReplyStatus === "waiting_target_message"
            ? "已保留全部历史消息。请等待客户先向当前企微账号发送一条消息，再继续回复。"
            : "当前企微实例或会话路由不可用，发送功能已暂停。"}
        </div>
      ) : null}
      <div ref={messagesContentRef} className="flex flex-col">
        {!loading && messages.length > 0 && messagesHasMore ? (
          <div className="mb-4 flex justify-center">
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="rounded-md border-border bg-background shadow-none"
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
          messages.map((message, index) => {
            const sessionNo = message.sessionNo > 0 ? message.sessionNo : 1;
            const session = findMessageHistorySegment(
              message,
              conversation.historySegments,
            );
            const segmentIndex = session?.index ?? message.historySegmentIndex ?? 0;
            const previousSegment = index > 0
              ? findMessageHistorySegment(messages[index - 1], conversation.historySegments)
              : undefined;
            const previousSegmentIndex = previousSegment?.index ??
              (index > 0 ? messages[index - 1].historySegmentIndex ?? 0 : 0);
            const showSessionDivider =
              (index > 0 && segmentIndex !== previousSegmentIndex) ||
              (index === 0 && !messagesHasMore && segmentIndex > 0);
            return (
              <Fragment key={`${message.conversationId}:${message.id}`}>
                {showSessionDivider ? (
                  <ConversationSessionDivider sessionNo={sessionNo} session={session} />
                ) : null}
                <MessageItem
                  message={message}
                  customerAvatar={conversation.customerAvatar}
                  onImageSettled={handleImageSettled}
                  canRecall={
                    message.conversationId === conversation.id &&
                    !message.inheritedHistory &&
                    !message.historicalOnly &&
                    message.senderType === "agent" &&
                    message.senderId === currentUserId
                  }
                  recalling={recallingMessageId === message.id}
                  onRecall={async (messageId) => {
                    await recallMessage(messageId);
                  }}
                />
              </Fragment>
            );
          })
        ) : (
          <div className="py-8 text-center text-sm text-muted-foreground">
            {t("conversation.emptyMessages")}
          </div>
        )}
      </div>
    </div>
  );

  const bottomPanel = (
    <div className="h-full overflow-auto border-t border-border bg-background">
      {isClosedConversation ? (
        <div className="flex h-full items-center justify-center bg-background text-sm text-muted-foreground">
          {t("conversation.closedNotice")}
        </div>
      ) : (
        <div className="flex h-full min-h-0 flex-col">
          <div className="min-h-0 flex-1">
            <AgentMessageEditor
              disabled={!conversation || sending}
              uploadingAsset={uploadingAsset}
              aiReplyEnabled={aiReplyServing}
              canAgentReply={canAgentReply}
              replyPermissionHint={replyPermissionHint}
              aiReplyToggleDisabled={
                !conversation ||
                isClosedConversation ||
                replyRouteUnavailable ||
                !takeoverState ||
                takeoverSubmitting ||
                sending ||
                uploadingAsset ||
                invitingGroupMembers
              }
              onToggleAIReply={handleToggleAIReply}
              onSend={handleSend}
              onUploadImage={async (file) => {
                if (!ensureReplyPermission()) {
                  return null;
                }
                shouldStickToBottomRef.current = true;
                const uploaded = await uploadImage(file);
                return uploaded;
              }}
              onSendImage={async (file) => {
                if (!ensureReplyPermission()) {
                  return;
                }
                shouldStickToBottomRef.current = true;
                try {
                  await sendImage(file);
                } catch (error) {
                  toast.error(error instanceof Error ? error.message : t("conversation.sendImageFailed"));
                }
              }}
              onSendAttachment={async (file) => {
                if (!ensureReplyPermission()) {
                  return;
                }
                shouldStickToBottomRef.current = true;
                try {
                  await sendAttachment(file);
                } catch (error) {
                  toast.error(error instanceof Error ? error.message : t("conversation.sendAttachmentFailed"));
                }
              }}
              onOpenGroupInvite={() => {
                if (ensureReplyPermission()) {
                  setGroupInviteDialogOpen(true);
                }
              }}
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
        open={takeoverDialogMode !== null}
        onOpenChange={(open) => {
          if (takeoverSubmitting) {
            return;
          }
          if (!open) {
            setTakeoverDialogMode(null);
          }
        }}
      >
        <DialogContent className="max-w-md" showCloseButton={false}>
          <DialogHeader>
            <DialogTitle>{takeoverDialogTitle(takeoverDialogMode)}</DialogTitle>
            <DialogDescription>{takeoverDialogDescription(takeoverDialogMode, takeoverState?.requesterName)}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={takeoverSubmitting}
              onClick={() => setTakeoverDialogMode(null)}
            >
              {t("conversation.cancel")}
            </Button>
            {takeoverDialogMode === "review" ? (
              <Button
                type="button"
                variant="destructive"
                disabled={takeoverSubmitting}
                onClick={() => void handleTakeoverSubmit(false)}
              >
                拒绝
              </Button>
            ) : null}
            <Button
              type="button"
              disabled={takeoverSubmitting}
              onClick={() => void handleTakeoverSubmit(true)}
            >
              {takeoverSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
              {takeoverDialogConfirmLabel(takeoverDialogMode)}
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
            <div className="rounded-xl border border-[#dbe7f6] bg-[#f6f9ff] px-3 py-2 text-xs text-muted-foreground shadow-inner shadow-blue-100/30">
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

function takeoverDialogTitle(mode: TakeoverDialogMode | null) {
  switch (mode) {
    case "request":
      return "申请接管会话";
    case "direct":
      return "确认直接接管";
    case "activate":
      return "确认接管会话";
    case "review":
      return "审核接管申请";
    case "resume":
      return "重新让 AI 接管会话";
    default:
      return "";
  }
}

function takeoverDialogDescription(mode: TakeoverDialogMode | null, requesterName?: string) {
  switch (mode) {
    case "request":
      return "主动接管会话需要向客服组长申请。审批通过前不能发送消息，当前草稿会保留。";
    case "direct":
      return "确认后该会话会立即分配给你，你可以在当前回复框继续处理。";
    case "activate":
      return "客服组长已批准本次申请。确认后会话才会正式分配给你。";
    case "review":
      return `${requesterName || "客服"}申请主动接管该会话。通过后只授予申请人接管资格，申请人还需再次点击 AI 回复开关确认接管。`;
    case "resume":
      return "确认后当前人工接待结束，后续客户消息将重新由 AI 处理。";
    default:
      return "";
  }
}

function takeoverDialogConfirmLabel(mode: TakeoverDialogMode | null) {
  switch (mode) {
    case "request":
      return "提交申请";
    case "direct":
      return "确认接管";
    case "activate":
      return "确认接管";
    case "review":
      return "通过";
    case "resume":
      return "确认交还 AI";
    default:
      return "确认";
  }
}

function ConversationSessionDivider({
  sessionNo,
  session,
}: {
  sessionNo: number;
  session?: ConversationHistorySegment;
}) {
  const reason = session?.startReason;
  const label =
    reason === "manual_inheritance"
      ? "以上为历史消息，已由主管安排会话继承"
      : reason === "instance_changed"
        ? "以上为历史消息，已更换企微账号"
        : "以上为历史消息，本次服务从这里开始";
  const account = [
    repairMojibakeText(session?.storeStaffDisplayName),
    repairMojibakeText(session?.wxWorkEmployeeDisplayName),
  ]
    .filter(Boolean)
    .join(" · ");

  return (
    <div className="my-5 flex items-center gap-3" role="separator">
      <div className="h-px flex-1 bg-border" />
      <div className="max-w-[70%] text-center text-xs text-muted-foreground">
        <div>{label}</div>
        <div className="mt-0.5 truncate text-[11px]">
          {account || `服务段 ${sessionNo}`}
          {session?.startedAt ? ` · ${formatDateTime(session.startedAt)}` : ""}
        </div>
      </div>
      <div className="h-px flex-1 bg-border" />
    </div>
  );
}

function findMessageHistorySegment(
  message: AgentMessage,
  segments?: ConversationHistorySegment[],
) {
  if (!segments?.length) {
    return undefined;
  }
  if (message.historySegmentIndex !== undefined) {
    const exact = segments.find((item) => item.index === message.historySegmentIndex);
    if (exact) {
      return exact;
    }
  }
  return segments.find(
    (item) =>
      item.currentConversation &&
      item.conversationId === message.conversationId &&
      item.sessionNo === (message.sessionNo > 0 ? message.sessionNo : 1),
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
  customerAvatar?: string;
  onImageSettled: () => void;
  canRecall: boolean;
  recalling: boolean;
  onRecall: (messageId: number) => Promise<void>;
};

const MessageItem = memo(
  function MessageItem({
    message,
    customerAvatar,
    onImageSettled,
    canRecall,
    recalling,
    onRecall,
  }: MessageItemProps) {
    const t = useI18n();
    const { open: openImageLightbox } = useImageLightbox();
    const isCustomer = message.senderType === "customer";
    const isAi = message.senderType === "ai";
    const isSystem = message.senderType === "system";
    const isAgentSide = message.senderType === "agent" || isAi || isSystem;
    const isRecalled = Boolean(message.recalledAt) || message.sendStatus === 6;
    const serviceEvent = parseServiceEvent(message.payload);
    if (serviceEvent.startsWith("manual_ai_resumed_")) {
      return (
        <div className="mb-5 flex justify-center px-4">
          <div className="max-w-[78%] text-center text-xs leading-5 text-muted-foreground">
            <div>{message.content}</div>
            <div className="mt-0.5 text-[10px] text-[#9aa4b4]">{formatDateTime(message.sentAt || "")}</div>
          </div>
        </div>
      );
    }
    const senderName = isCustomer
      ? repairMojibakeText(message.senderName) || t("conversation.customerSender")
      : isAi
        ? "AI"
        : isSystem
          ? t("conversation.systemSender")
          : repairMojibakeText(message.senderName) || t("conversation.agentSender");
    const senderBadge = isAi
      ? "AI回复"
      : isSystem
        ? t("conversation.systemBadge")
        : isAgentSide
          ? "人工"
          : "客户";
    const sendStatusLabel = isAgentSide && !isRecalled ? IMMessageStatusLabels[message.sendStatus as keyof typeof IMMessageStatusLabels] : "";
    const sendSourceLabel = !isAi && isAgentSide ? message.sendSourceLabel?.trim() : "";
    const senderBadgeClassName = isAi
      ? "border-primary/20 bg-primary/10 text-primary"
      : isAgentSide
        ? "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-500/25 dark:bg-emerald-500/15 dark:text-emerald-300"
        : "border-border bg-muted/40 text-muted-foreground";
    const sendSourceBadgeClassName =
      message.sendSource === "local"
        ? "border-sky-200 bg-sky-50 text-sky-700 dark:border-sky-500/25 dark:bg-sky-500/15 dark:text-sky-300"
        : "border-border bg-background/70 text-muted-foreground";
    const agentAvatarSrc =
      isAgentSide && !isAi && message.senderAvatar?.trim()
        ? message.senderAvatar.trim()
        : undefined;
    const avatarFallback = isAi ? "AI" : senderName.charAt(0);
    const htmlContent = buildMessageHTML(message);
    const bubbleClassName = isAi
        ? "border border-border bg-card text-foreground shadow-sm"
      : isAgentSide
        ? "bg-primary/15 text-foreground shadow-sm"
        : "bg-card text-foreground shadow-sm";
    const htmlClassName = isAi
      ? "[&_a]:text-foreground [&_a]:underline [&_img]:rounded-md"
      : isAgentSide
        ? "[&_p]:text-foreground [&_a]:text-foreground [&_a]:underline [&_img]:rounded-md"
        : "[&_a]:text-foreground [&_a]:underline [&_img]:rounded-md";
    const avatarClassName = isAi
      ? "border border-primary/20 bg-primary/10 text-xs text-foreground"
      : isAgentSide
        ? "bg-primary/10 text-xs text-muted-foreground"
        : "bg-card text-xs text-muted-foreground";
    const showRecallAction = canRecall && !isRecalled;

    return (
      <div
        className={`mb-5 flex items-start gap-2 ${
          isAgentSide ? "justify-end" : "justify-start"
        }`}
      >
        {isAgentSide ? (
          <>
            <div className="flex max-w-[72%] flex-col items-end">
              <div className="mb-1 flex items-center gap-2 text-[11px] text-[#8b95a5]">
                <span
                  className={`rounded-md border px-1.5 py-0.5 text-[10px] leading-none ${senderBadgeClassName}`}
                >
                  {senderBadge}
                </span>
                {senderName}
              </div>
              <div
                className={`w-fit rounded-[18px] rounded-tr-md px-3.5 py-2 text-left text-sm leading-6 ${
                  bubbleClassName
                }`}
              >
                <ImMessageHTML
                  html={htmlContent}
                  className={htmlClassName}
                  onImageSettled={onImageSettled}
                  onImageClick={openImageLightbox}
                />
              </div>
              <div className="mt-1 flex items-center gap-2 text-[11px] text-[#8b95a5]">
                <span>{formatDateTime(message.sentAt || "")}</span>
                {sendSourceLabel ? (
                  <span
                    className={`rounded-md border px-1.5 py-0.5 text-[10px] leading-none ${sendSourceBadgeClassName}`}
                  >
                    {sendSourceLabel}
                  </span>
                ) : null}
                {message.historicalOnly ? <span>历史归档</span> : null}
                {isRecalled ? <span>{t("conversation.messageRecalled")}</span> : null}
                {sendStatusLabel ? <span>{sendStatusLabel}</span> : null}
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
            <Avatar className="size-8 shrink-0 rounded-xl">
              <AvatarImage src={agentAvatarSrc ?? ""} />
              <AvatarFallback className={`${avatarClassName} rounded-xl`}>
                {avatarFallback}
              </AvatarFallback>
            </Avatar>
          </>
        ) : (
          <>
            <Avatar className="size-8 shrink-0 rounded-xl">
              <AvatarImage src={customerAvatar || ""} />
              <AvatarFallback className={`${avatarClassName} rounded-xl`}>
                {t("conversation.customerAvatar")}
              </AvatarFallback>
            </Avatar>
            <div className="max-w-[72%]">
              <div className="mb-1 flex items-center gap-2 text-[11px] text-[#8b95a5]">
                {senderName}
                <span
                  className={`rounded-md border px-1.5 py-0.5 text-[10px] leading-none ${senderBadgeClassName}`}
                >
                  {senderBadge}
                </span>
              </div>
              <div
                className={`w-fit rounded-[18px] rounded-tl-md px-3.5 py-2 text-sm leading-6 ${
                  bubbleClassName
                }`}
              >
                <ImMessageHTML
                  html={htmlContent}
                  className={htmlClassName}
                  onImageSettled={onImageSettled}
                  onImageClick={openImageLightbox}
                />
              </div>
              <div className="mt-1 flex items-center gap-2 text-[11px] text-[#8b95a5]">
                <span>{formatDateTime(message.sentAt || "")}</span>
                {message.historicalOnly ? <span>历史归档</span> : null}
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

function parseServiceEvent(payload?: string) {
  if (!payload?.trim().startsWith("{")) return "";
  try {
    const value = JSON.parse(payload) as { serviceEvent?: unknown };
    return typeof value.serviceEvent === "string" ? value.serviceEvent.trim() : "";
  } catch {
    return "";
  }
}
