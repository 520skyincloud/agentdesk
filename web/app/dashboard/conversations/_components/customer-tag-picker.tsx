"use client"

import { useMemo, useState } from "react"
import { toast } from "sonner"

import { TagSelector } from "@/components/tag-selector"
import {
  addCustomerTag,
  removeCustomerTag,
  replaceCustomerTag,
  type AgentConversation,
} from "@/lib/api/agent"
import type { Tag, TagTree } from "@/lib/api/admin"
import { useI18n } from "@/i18n/provider"

const MAX_CUSTOMER_TAGS = 6

type CustomerTagPickerProps = {
  conversation: AgentConversation
  availableTags: Tag[]
  loading?: boolean
  onTagsChanged: () => Promise<void> | void
}

function toSelectorTags(tags: Tag[]): TagTree[] {
  return tags.map((tag) => ({
    ...tag,
    name: tag.displayAlias.trim() || tag.name,
    children: [],
  }))
}

export function CustomerTagPicker({
  conversation,
  availableTags,
  loading = false,
  onTagsChanged,
}: CustomerTagPickerProps) {
  const t = useI18n()
  const [pendingTagId, setPendingTagId] = useState<number | null>(null)
  const currentTags = useMemo(
    () => conversation.customerTags ?? [],
    [conversation.customerTags],
  )
  const selectedValues = useMemo(
    () => currentTags.map((item) => item.tagId),
    [currentTags],
  )
  const selectedTagIds = useMemo(() => new Set(selectedValues), [selectedValues])
  const selectorTags = useMemo(() => toSelectorTags(availableTags), [availableTags])

  async function handleChange(nextTagIds: number[]) {
    if (pendingTagId !== null) return

    const tagId =
      nextTagIds.find((id) => !selectedTagIds.has(id)) ??
      selectedValues.find((id) => !nextTagIds.includes(id))
    if (!tagId) return

    const removing = selectedTagIds.has(tagId)
    const target = availableTags.find((item) => item.id === tagId)
    const conflictingTag = !removing && target?.conflictGroup
      ? currentTags.find((item) => item.conflictGroup === target.conflictGroup)
      : undefined

    if (!removing && currentTags.length >= MAX_CUSTOMER_TAGS && !conflictingTag) {
      toast.error(t("conversation.customerTagLimit", { count: MAX_CUSTOMER_TAGS }))
      return
    }

    setPendingTagId(tagId)
    try {
      if (removing) {
        await removeCustomerTag({ conversationId: conversation.id, tagId })
      } else if (conflictingTag) {
        await replaceCustomerTag({
          conversationId: conversation.id,
          oldTagId: conflictingTag.tagId,
          newTagId: tagId,
        })
      } else {
        await addCustomerTag({ conversationId: conversation.id, tagId })
      }
      await onTagsChanged()
      toast.success(removing
        ? t("conversation.customerTagRemoved")
        : conflictingTag
          ? t("conversation.customerTagReplaced")
          : t("conversation.customerTagAdded"))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("conversation.customerTagUpdateFailed"))
    } finally {
      setPendingTagId(null)
    }
  }

  return (
    <TagSelector
      mode="multiple"
      value={selectedValues}
      onChange={(value) => void handleChange(value)}
      tags={selectorTags}
      loading={loading}
      pendingTagId={pendingTagId}
      isTagDisabled={(tag) => tag.status !== 0}
      placeholder={t("conversation.editCustomerTags")}
      triggerText={t("conversation.edit")}
      selectedCountText={(count) => t("conversation.customerTagCount", { count })}
      searchPlaceholder={t("conversation.searchCustomerTags")}
      loadingText={t("conversation.loadingTags")}
      emptyText={t("conversation.emptyTags")}
      align="end"
      showSelectedBadges={false}
      triggerVariant="ghost"
      triggerSize="sm"
      triggerClassName="h-7 w-auto shrink-0 justify-start gap-1 px-2 text-xs"
      contentClassName="w-72"
    />
  )
}
