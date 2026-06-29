"use client"

import { useEffect, useRef, useState, type ChangeEvent } from "react"
import Placeholder from "@tiptap/extension-placeholder"
import { EditorContent, useEditor } from "@tiptap/react"
import StarterKit from "@tiptap/starter-kit"
import {
  LaughIcon,
  ImageIcon,
  MessageSquareTextIcon,
  PaperclipIcon,
  UsersRoundIcon,
  SendHorizonalIcon,
  SendIcon,
} from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Switch } from "@/components/ui/switch"
import {
  buildSendableEditorHTML,
  hasUploadingEditorImages,
  markEditorImageUploadedByTitle,
  MessageImageExtension,
  removeEditorImageByTitle,
  revokeEditorObjectUrl,
  revokeEditorObjectUrls,
  setEditorImageUploadingByTitle,
  type UploadedEditorImage,
} from "@/lib/im-editor-image"
import { generateUUID } from "@/lib/utils"
import { useI18n } from "@/i18n/provider"

export type UploadedMessageEditorImage = UploadedEditorImage & {
  url: string
}

export type MessageEditorQuickReply = {
  id: number
  groupName?: string
  title: string
  content: string
}

type SharedMessageEditorVariant = "customer" | "agent"

const QUICK_EMOJIS = ["😊", "好的", "👌", "收到", "🙏", "稍等", "❤️", "😄", "👍", "🌟"]

type SharedMessageEditorProps = {
  variant: SharedMessageEditorVariant
  disabled?: boolean
  uploadingAsset?: boolean
  manageLocalUploading?: boolean
  quickReplies?: {
    open: boolean
    loading: boolean
    items: MessageEditorQuickReply[]
    onOpenChange: (open: boolean) => void
  }
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

export function SharedMessageEditor({
  variant,
  disabled = false,
  uploadingAsset = false,
  manageLocalUploading = false,
  quickReplies,
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
}: SharedMessageEditorProps) {
  const t = useI18n()
  const [localUploading, setLocalUploading] = useState(false)
  const [emojiPickerOpen, setEmojiPickerOpen] = useState(false)
  const imageInputRef = useRef<HTMLInputElement | null>(null)
  const attachmentInputRef = useRef<HTMLInputElement | null>(null)
  const onSendRef = useRef(onSend)
  const onUploadImageRef = useRef(onUploadImage)
  const onSendImageRef = useRef(onSendImage)
  const onSendAttachmentRef = useRef(onSendAttachment)
  const shouldRestoreFocusRef = useRef(false)
  const objectUrlsRef = useRef<Set<string>>(new Set())
  const uploadedImagesRef = useRef(new Map<string, UploadedMessageEditorImage>())
  const placeholderRef = useRef(t("conversation.editorPlaceholder"))
  const isCustomer = variant === "customer"
  const isUploading = uploadingAsset || (manageLocalUploading && localUploading)
  const controlsDisabled = disabled || isUploading

  placeholderRef.current = t("conversation.editorPlaceholder")

  useEffect(() => {
    const objectUrls = objectUrlsRef.current
    return () => {
      revokeEditorObjectUrls(objectUrls)
    }
  }, [])

  useEffect(() => {
    onSendRef.current = onSend
  }, [onSend])

  useEffect(() => {
    onUploadImageRef.current = onUploadImage
  }, [onUploadImage])

  useEffect(() => {
    onSendImageRef.current = onSendImage
  }, [onSendImage])

  useEffect(() => {
    onSendAttachmentRef.current = onSendAttachment
  }, [onSendAttachment])

  const editor = useEditor({
    immediatelyRender: false,
    extensions: [
      StarterKit.configure({
        heading: false,
        blockquote: false,
        codeBlock: false,
        bulletList: false,
        orderedList: false,
        horizontalRule: false,
      }),
      MessageImageExtension,
      Placeholder.configure({
        placeholder: () => placeholderRef.current,
      }),
    ],
    content: "",
    editorProps: {
      attributes: {
        class: getEditorClassName(variant),
      },
      handleKeyDown: (_view, event) => {
        if (event.key === "Enter" && !event.shiftKey) {
          event.preventDefault()
          void handleSend()
          return true
        }
        return false
      },
      handlePaste: (_view, event) => {
        if (disabled || isUploading) {
          return false
        }
        const imageFile = getClipboardImageFile(event.clipboardData)
        if (!imageFile) {
          return false
        }
        event.preventDefault()
        void insertUploadedImage(imageFile)
        return true
      },
    },
  })

  useEffect(() => {
    if (!editor) {
      return
    }
    editor.setEditable(!disabled && !isUploading)
  }, [disabled, editor, isUploading])

  useEffect(() => {
    if (!editor || disabled || isUploading || !shouldRestoreFocusRef.current) {
      return
    }
    requestAnimationFrame(() => {
      editor.commands.focus()
    })
  }, [disabled, editor, isUploading])

  async function handleSend() {
    if (!editor || disabled || isUploading) {
      return
    }
    const rawHTML = editor.getHTML()
    if (hasUploadingEditorImages(rawHTML, uploadedImagesRef.current)) {
      return
    }
    const html = buildSendableEditorHTML(rawHTML, uploadedImagesRef.current)
    if (!isMeaningfulHTML(html)) {
      return
    }
    await onSendRef.current(html)
    editor.commands.clearContent(true)
    revokeEditorObjectUrls(objectUrlsRef.current)
    uploadedImagesRef.current.clear()
    if (!isCustomer) {
      requestAnimationFrame(() => {
        editor.commands.focus("end")
      })
    }
  }

  async function handleSelectImage(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0]
    event.target.value = ""
    if (!file || !editor || disabled || isUploading) {
      restoreFocusIfNeeded()
      return
    }
    if (!isCustomer && onSendImageRef.current) {
      shouldRestoreFocusRef.current = editor?.isFocused ?? true
      try {
        setLocalUploading(true)
        await onSendImageRef.current(file)
      } finally {
        setLocalUploading(false)
        restoreFocusIfNeeded()
      }
      return
    }
    await insertUploadedImage(file)
  }

  async function insertUploadedImage(file: File) {
    if (!editor || disabled || isUploading) {
      return
    }

    shouldRestoreFocusRef.current = true
    const objectUrl = URL.createObjectURL(file)
    objectUrlsRef.current.add(objectUrl)
    const placeholderId = `uploading-${generateUUID()}`
    editor
      .chain()
      .focus()
      .setImage({
        src: objectUrl,
        alt: file.name || "uploading-image",
        title: placeholderId,
      })
      .run()
    setEditorImageUploadingByTitle(editor, placeholderId)

    try {
      setLocalUploading(true)
      const uploaded = await onUploadImageRef.current(file)
      if (!uploaded?.assetId || !uploaded.provider || !uploaded.storageKey) {
        removeEditorImageByTitle(editor, placeholderId)
        revokeEditorObjectUrl(objectUrlsRef.current, objectUrl)
        return
      }
      markEditorImageUploadedByTitle(
        editor,
        placeholderId,
        uploaded,
        uploadedImagesRef.current
      )
    } finally {
      setLocalUploading(false)
      requestAnimationFrame(() => {
        if (!disabled && shouldRestoreFocusRef.current) {
          editor.commands.focus()
        }
      })
    }
  }

  async function handleSelectAttachment(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0]
    event.target.value = ""
    if (!file || disabled || isUploading) {
      restoreFocusIfNeeded()
      return
    }

    shouldRestoreFocusRef.current = editor?.isFocused ?? true
    try {
      setLocalUploading(true)
      await onSendAttachmentRef.current(file)
    } finally {
      setLocalUploading(false)
      requestAnimationFrame(() => {
        if (editor && !disabled && shouldRestoreFocusRef.current) {
          editor.commands.focus()
        }
      })
    }
  }

  function handleInsertQuickReply(item: MessageEditorQuickReply) {
    if (!editor || disabled || isUploading) {
      return
    }
    if (!item.content.trim()) {
      return
    }
    editor.chain().focus().insertContent(item.content).run()
    quickReplies?.onOpenChange(false)
  }

  function handleInsertEmoji(value: string) {
    if (!editor || disabled || isUploading) {
      return
    }
    editor.chain().focus().insertContent(value).run()
    setEmojiPickerOpen(false)
  }

  function restoreFocusIfNeeded() {
    if (editor && shouldRestoreFocusRef.current) {
      requestAnimationFrame(() => {
        editor.commands.focus()
      })
    }
  }

  const editorContent = (
    <>
      <input
        ref={imageInputRef}
        type="file"
        accept="image/*"
        className="hidden"
        onChange={handleSelectImage}
      />
      <input
        ref={attachmentInputRef}
        type="file"
        className="hidden"
        onChange={handleSelectAttachment}
      />
      {isCustomer ? (
        <div className="min-h-10">
        <div className="relative min-h-0 flex-1">
          <EditorContent editor={editor} />
        </div>
        </div>
      ) : (
        <div className="min-h-0 flex-1 overflow-hidden px-2 py-1">
          <EditorContent editor={editor} className="h-full" />
        </div>
      )}
      {!isCustomer && disabled && disabledReason ? (
        <div className="border-t border-border bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:bg-amber-500/10 dark:text-amber-200">
          {disabledReason}
        </div>
      ) : null}
      {!isCustomer && !canAgentReply && !aiReplyEnabled ? (
        <div className="border-t border-border bg-destructive/10 px-3 py-2 text-xs text-destructive">
          当前会话暂不可发送，请先接管或检查会话状态。
        </div>
      ) : null}
      <div className={getToolbarClassName(variant)}>
        <div className={isCustomer ? "flex items-center gap-1.5" : "flex items-center gap-1"}>
          <Popover open={emojiPickerOpen} onOpenChange={setEmojiPickerOpen}>
            <PopoverTrigger
              render={
                <Button
                  type="button"
                  variant={isCustomer ? "ghost" : "secondary"}
                  size={isCustomer ? "icon" : "sm"}
                  className={getToolButtonClassName(variant)}
                  onMouseDown={(event) => event.preventDefault()}
                  disabled={controlsDisabled}
                />
              }
            >
              <LaughIcon className="size-4" />
              {!isCustomer ? <span>{t("conversation.emoji")}</span> : null}
            </PopoverTrigger>
            <PopoverContent className="w-64 p-2" align="start">
              <div className="grid grid-cols-5 gap-1">
                {QUICK_EMOJIS.map((item) => (
                  <Button
                    key={item}
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="h-9 min-w-0 px-2 text-sm"
                    onMouseDown={(event) => event.preventDefault()}
                    onClick={() => handleInsertEmoji(item)}
                  >
                    {item}
                  </Button>
                ))}
              </div>
            </PopoverContent>
          </Popover>
          <Button
            type="button"
            variant={isCustomer ? "ghost" : "secondary"}
            size={isCustomer ? "icon" : "sm"}
            className={getToolButtonClassName(variant)}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => {
              shouldRestoreFocusRef.current = editor?.isFocused ?? true
              imageInputRef.current?.click()
            }}
            disabled={controlsDisabled}
            aria-label={isUploading ? t("conversation.imageUploading") : t("conversation.sendImage")}
            title={isUploading ? t("conversation.imageUploading") : t("conversation.sendImage")}
          >
            <ImageIcon className="size-4" />
            {!isCustomer ? <span>{t("conversation.image")}</span> : null}
          </Button>
          {isCustomer ? (
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className={getIconButtonClassName(variant)}
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => {
                shouldRestoreFocusRef.current = editor?.isFocused ?? true
                attachmentInputRef.current?.click()
              }}
              disabled={controlsDisabled}
              aria-label={isUploading ? t("conversation.attachmentUploading") : t("conversation.sendAttachment")}
              title={isUploading ? t("conversation.attachmentUploading") : t("conversation.sendAttachment")}
            >
              <PaperclipIcon />
            </Button>
          ) : null}
          {quickReplies ? (
            <Popover open={quickReplies.open} onOpenChange={quickReplies.onOpenChange}>
              <PopoverTrigger
                render={
                  <Button
                    type="button"
                    variant="secondary"
                    size="sm"
                    className={getToolButtonClassName(variant)}
                    disabled={controlsDisabled || quickReplies.loading}
                    onMouseDown={(event) => event.preventDefault()}
                  />
                }
              >
                <MessageSquareTextIcon className="size-4" />
                {!isCustomer ? <span>{t("conversation.quickReplyMaterial")}</span> : null}
              </PopoverTrigger>
              <PopoverContent className="w-[30rem] p-0" align="start">
                <Command>
                  <CommandInput placeholder={t("conversation.searchQuickReplies")} />
                  <CommandList>
                    <CommandEmpty>{t("conversation.emptyQuickReplies")}</CommandEmpty>
                    <CommandGroup>
                      {quickReplies.items.map((item) => (
                        <CommandItem
                          key={item.id}
                          value={`${item.groupName ?? ""} ${item.title} ${item.content}`}
                          onSelect={() => handleInsertQuickReply(item)}
                        >
                          <div className="flex min-w-0 flex-col gap-0.5 py-0.5">
                            <span className="line-clamp-1 text-sm">
                              {item.groupName
                                ? `${item.groupName} / ${item.title}`
                                : item.title}
                            </span>
                            <span className="line-clamp-2 text-xs text-muted-foreground">
                              {item.content}
                            </span>
                          </div>
                        </CommandItem>
                      ))}
                    </CommandGroup>
                  </CommandList>
                </Command>
              </PopoverContent>
            </Popover>
          ) : null}
          {!isCustomer ? (
            <Button
              type="button"
              variant="secondary"
              size="sm"
              className={getToolButtonClassName(variant)}
              disabled={controlsDisabled || !onOpenGroupInvite}
              onMouseDown={(event) => event.preventDefault()}
              onClick={onOpenGroupInvite}
              aria-label={t("conversation.groupInvite")}
              title={t("conversation.groupInvite")}
            >
              <UsersRoundIcon className="size-4" />
              <span>{t("conversation.groupInvite")}</span>
            </Button>
          ) : null}
        </div>
        <div className="flex items-center gap-2">
          {!isCustomer ? (
            <label className="flex items-center gap-2 text-sm text-foreground">
              <span>{t("conversation.aiReply")}</span>
              <Switch
                checked={aiReplyEnabled}
                disabled={aiReplyToggleDisabled}
                onCheckedChange={(checked) => void onToggleAIReply?.(checked)}
              />
            </label>
          ) : (
            <p className="hidden text-[10px] text-muted-foreground sm:block">
              {t("conversation.enterToSend")}
            </p>
          )}
          {isCustomer ? (
            <Button
              type="button"
              size="icon"
              onClick={() => void handleSend()}
              disabled={controlsDisabled}
              aria-label={t("conversation.send")}
              title={t("conversation.send")}
              className="bg-primary text-white shadow-[0_10px_20px_color-mix(in_srgb,var(--primary)_24%,transparent)] hover:bg-primary hover:brightness-105"
            >
              <SendHorizonalIcon />
            </Button>
          ) : (
            <Button
              type="button"
              size="icon"
              onClick={() => void handleSend()}
              disabled={controlsDisabled}
              aria-label={t("conversation.send")}
              title={t("conversation.send")}
              className="size-10 rounded-md bg-primary text-primary-foreground shadow-[0_10px_20px_color-mix(in_srgb,var(--primary)_22%,transparent)] hover:bg-primary hover:brightness-105 disabled:opacity-45"
            >
              <SendIcon className="size-5" />
            </Button>
          )}
        </div>
      </div>
    </>
  )

  if (isCustomer) {
    return (
      <div className="px-3 pt-2 pb-3">
        <div className="agentdesk-subtle-surface rounded-xl p-2 dark:shadow-none">
          {editorContent}
        </div>
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0 flex-col bg-white p-0">
      <div className="flex h-full min-h-0 flex-col overflow-hidden bg-white">
        {editorContent}
      </div>
    </div>
  )
}

function getEditorClassName(variant: SharedMessageEditorVariant) {
  if (variant === "customer") {
    return "agent-desk-scrollbar min-h-12 max-h-40 overflow-y-auto px-1.5 py-1 text-sm leading-6 text-foreground outline-none [&_p]:m-0 [&_p+*]:mt-2 [&_.agent-desk-editor-image-wrap]:my-2 [&_.agent-desk-editor-image]:max-h-64 [&_.agent-desk-editor-image]:max-w-full [&_.agent-desk-editor-image]:rounded-lg [&_.agent-desk-editor-image]:object-contain [&_.agent-desk-editor-image-wrap-uploading_.agent-desk-editor-image]:opacity-55"
  }
  return "h-full min-h-20 max-h-[20vh] overflow-y-auto px-5 py-4 text-sm leading-6 text-foreground outline-none sm:max-h-none [&_.ProseMirror-focused]:outline-none [&_p]:m-0 [&_p+.agent-desk-editor-image-wrap]:mt-2 [&_.agent-desk-editor-image-wrap]:my-2 [&_.agent-desk-editor-image]:max-h-64 [&_.agent-desk-editor-image]:max-w-full [&_.agent-desk-editor-image]:rounded-md [&_.agent-desk-editor-image]:object-contain [&_.agent-desk-editor-image-wrap-uploading_.agent-desk-editor-image]:opacity-55 [&_p.is-editor-empty:first-child]:before:text-muted-foreground"
}

function getToolbarClassName(variant: SharedMessageEditorVariant) {
  if (variant === "customer") {
    return "mt-2 flex items-center justify-between"
  }
  return "flex items-center justify-between border-t border-[#edf1f6] bg-[#fbfcfe] px-5 py-3"
}

function getIconButtonClassName(variant: SharedMessageEditorVariant) {
  if (variant === "customer") {
    return "rounded-xl text-muted-foreground transition hover:bg-[#f2f7ff] hover:text-primary"
  }
  return "size-8 rounded-lg border border-transparent bg-[#f3f6fa] text-[#637083] shadow-none hover:border-[#d9e2f2] hover:bg-white hover:text-[#2563eb]"
}

function getToolButtonClassName(variant: SharedMessageEditorVariant) {
  if (variant === "customer") {
    return "rounded-xl text-muted-foreground transition hover:bg-[#f2f7ff] hover:text-primary"
  }
  return "h-8 gap-1.5 rounded-lg border border-[#edf1f7] bg-[#f3f6fa] px-3 text-xs font-medium text-[#344054] shadow-none hover:border-[#d9e2f2] hover:bg-white hover:text-[#2563eb] disabled:opacity-45"
}

function isMeaningfulHTML(html: string) {
  const normalized = html
    .replace(/<p><\/p>/g, "")
    .replace(/<p><br><\/p>/g, "")
    .replace(/\s+/g, "")
  if (/<img[\s\S]*?>/i.test(normalized)) {
    return true
  }
  const plainText = normalized.replace(/<[^>]+>/g, "").trim()
  return plainText !== ""
}

function getClipboardImageFile(data: DataTransfer | null) {
  if (!data) {
    return null
  }

  for (const item of Array.from(data.items)) {
    if (item.kind === "file" && item.type.startsWith("image/")) {
      return item.getAsFile()
    }
  }
  return null
}
