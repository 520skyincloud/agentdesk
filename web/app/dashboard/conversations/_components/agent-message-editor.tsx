"use client"

import { useEffect, useState } from "react"
import { toast } from "sonner"

import {
  SharedMessageEditor,
  type UploadedMessageEditorImage,
} from "@/components/chat/shared-message-editor"
import { useI18n } from "@/i18n/provider"
import { fetchQuickReplyListAll, type AdminQuickReply } from "@/lib/api/admin"

type AgentMessageEditorProps = {
  disabled?: boolean
  uploadingAsset?: boolean
  aiReplyEnabled?: boolean
  canAgentReply?: boolean
  disabledReason?: string
  aiReplyToggleDisabled?: boolean
  onSend: (html: string) => Promise<void>
  onUploadImage: (file: File) => Promise<UploadedMessageEditorImage | null>
  onSendImage?: (file: File) => Promise<void>
  onSendAttachment: (file: File) => Promise<void>
  onToggleAIReply?: (enabled: boolean) => Promise<void> | void
  onOpenGroupInvite?: () => void
}

export function AgentMessageEditor({
  disabled = false,
  uploadingAsset = false,
  aiReplyEnabled = true,
  canAgentReply = !disabled,
  disabledReason,
  aiReplyToggleDisabled = false,
  onSend,
  onUploadImage,
  onSendImage,
  onSendAttachment,
  onToggleAIReply,
  onOpenGroupInvite,
}: AgentMessageEditorProps) {
  const t = useI18n()
  const [quickReplies, setQuickReplies] = useState<AdminQuickReply[]>([])
  const [loadingQuickReplies, setLoadingQuickReplies] = useState(true)
  const [quickReplyPickerOpen, setQuickReplyPickerOpen] = useState(false)

  useEffect(() => {
    let cancelled = false
    void fetchQuickReplyListAll()
      .then((list) => {
        if (!cancelled) {
          setQuickReplies(list)
        }
      })
      .catch((error) => {
        if (!cancelled) {
          toast.error(error instanceof Error ? error.message : t("conversation.loadQuickRepliesFailed"))
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoadingQuickReplies(false)
        }
      })
    return () => {
      cancelled = true
    }
  }, [t])

  return (
    <SharedMessageEditor
      variant="agent"
      disabled={disabled}
      uploadingAsset={uploadingAsset}
      quickReplies={{
        open: quickReplyPickerOpen,
        loading: loadingQuickReplies,
        items: quickReplies,
        onOpenChange: setQuickReplyPickerOpen,
      }}
      aiReplyEnabled={aiReplyEnabled}
      canAgentReply={canAgentReply}
      disabledReason={disabledReason}
      aiReplyToggleDisabled={aiReplyToggleDisabled}
      onSend={onSend}
      onUploadImage={onUploadImage}
      onSendImage={onSendImage}
      onSendAttachment={onSendAttachment}
      onToggleAIReply={onToggleAIReply}
      onOpenGroupInvite={onOpenGroupInvite}
    />
  )
}
