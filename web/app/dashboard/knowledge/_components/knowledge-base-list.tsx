"use client"

import {
  DndContext,
  KeyboardSensor,
  MouseSensor,
  TouchSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core"
import {
  SortableContext,
  arrayMove,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable"
import { CSS } from "@dnd-kit/utilities"
import { CloudIcon, MoreHorizontalIcon, PencilIcon, PlusIcon, RefreshCwIcon, SearchIcon } from "lucide-react"
import type { CSSProperties } from "react"
import { useCallback, useEffect, useMemo, useState } from "react"
import { toast } from "sonner"

import { OptionCombobox } from "@/components/option-combobox"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import { useI18n } from "@/i18n/provider"
import {
  fetchKnowledgeBases,
  updateKnowledgeBase,
  updateKnowledgeBaseSort,
  type KnowledgeBase,
  type UpdateKnowledgeBasePayload,
} from "@/lib/api/admin"
import { Status } from "@/lib/generated/enums"
import { cn } from "@/lib/utils"

import { FastGPTProvisionDialog } from "./fastgpt-provision-dialog"
import { EditDialog } from "./knowledge-base-edit"

type KnowledgeBaseListProps = {
  selectedKnowledgeBaseId: number | null
  onSelectKnowledgeBase: (knowledgeBase: KnowledgeBase | null) => void
  canCreate: boolean
  canUpdate: boolean
}

type TFunction = (key: string, values?: Record<string, string | number>) => string

function statusOptions(t: TFunction) {
  return [
    { value: "all", label: t("knowledge.allStatus") },
    { value: String(Status.Ok), label: t("knowledge.statusOk") },
    { value: String(Status.Disabled), label: t("knowledge.statusDisabled") },
  ]
}

function SortableKnowledgeBase({
  item,
  selected,
  disabled,
  canUpdate,
  onSelect,
  onEdit,
  t,
}: {
  item: KnowledgeBase
  selected: boolean
  disabled: boolean
  canUpdate: boolean
  onSelect: () => void
  onEdit: () => void
  t: TFunction
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: item.id, disabled })
  const style: CSSProperties = { transform: CSS.Transform.toString(transform), transition }

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={cn(
        "group mx-2 flex cursor-pointer items-center gap-2 rounded-md px-2 py-2 text-sm transition-colors hover:bg-[#f2f7ff]",
        selected && "bg-[#eef5ff] text-primary shadow-sm shadow-blue-100/60",
        isDragging && "bg-[#eef5ff] opacity-90 shadow-lg",
      )}
      onClick={onSelect}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault()
          onSelect()
        }
      }}
      {...attributes}
      {...listeners}
    >
      <CloudIcon className="size-4 shrink-0 text-muted-foreground" />
      <span className="min-w-0 flex-1 truncate">{item.name}</span>
      {canUpdate ? (
        <DropdownMenu>
          <DropdownMenuTrigger
            render={<Button variant="ghost" size="icon" className="size-6 opacity-0 group-hover:opacity-100" />}
            aria-label={t("knowledge.moreActions", { name: item.name })}
            onClick={(event) => event.stopPropagation()}
          >
            <MoreHorizontalIcon className="size-3.5" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-40 min-w-40">
            <DropdownMenuItem
              onClick={(event) => {
                event.stopPropagation()
                onEdit()
              }}
            >
              <PencilIcon className="mr-2 size-3.5" />
              {t("knowledge.edit")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      ) : null}
    </div>
  )
}

export function KnowledgeBaseList({
  selectedKnowledgeBaseId,
  onSelectKnowledgeBase,
  canCreate,
  canUpdate,
}: KnowledgeBaseListProps) {
  const t = useI18n()
  const [keywordInput, setKeywordInput] = useState("")
  const [statusInput, setStatusInput] = useState("all")
  const [keyword, setKeyword] = useState("")
  const [status, setStatus] = useState("all")
  const [loading, setLoading] = useState(true)
  const [sorting, setSorting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [editingItemId, setEditingItemId] = useState<number | null>(null)
  const [editOpen, setEditOpen] = useState(false)
  const [provisionOpen, setProvisionOpen] = useState(false)
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([])
  const options = useMemo(() => statusOptions(t), [t])
  const sensors = useSensors(
    useSensor(MouseSensor, { activationConstraint: { distance: 8 } }),
    useSensor(TouchSensor, { activationConstraint: { delay: 150, tolerance: 8 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )

  const loadData = useCallback(async () => {
    setLoading(true)
    try {
      const data = await fetchKnowledgeBases({
        name: keyword.trim() || undefined,
        status: status === "all" ? undefined : status,
        limit: 1000,
      })
      setKnowledgeBases(data.results)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("knowledge.loadBasesFailed"))
    } finally {
      setLoading(false)
    }
  }, [keyword, status, t])

  useEffect(() => void loadData(), [loadData])
  useEffect(() => {
    if (selectedKnowledgeBaseId === null && knowledgeBases.length > 0 && !loading) {
      onSelectKnowledgeBase(knowledgeBases[0])
    }
  }, [knowledgeBases, loading, onSelectKnowledgeBase, selectedKnowledgeBaseId])

  async function submitEdit(payload: UpdateKnowledgeBasePayload) {
    if (!canUpdate || !editingItemId || saving) return
    setSaving(true)
    try {
      await updateKnowledgeBase({ id: editingItemId, ...payload })
      toast.success(t("knowledge.baseUpdated", { name: payload.name }))
      setEditOpen(false)
      setEditingItemId(null)
      await loadData()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("knowledge.baseSaveFailed"))
    } finally {
      setSaving(false)
    }
  }

  async function handleDragEnd(event: DragEndEvent) {
    if (!canUpdate || sorting || !event.over || event.active.id === event.over.id) return
    const oldIndex = knowledgeBases.findIndex((item) => item.id === event.active.id)
    const newIndex = knowledgeBases.findIndex((item) => item.id === event.over?.id)
    if (oldIndex < 0 || newIndex < 0) return
    const previous = knowledgeBases
    const next = arrayMove(previous, oldIndex, newIndex)
    setKnowledgeBases(next)
    setSorting(true)
    try {
      await updateKnowledgeBaseSort(next.map((item) => item.id))
    } catch (error) {
      setKnowledgeBases(previous)
      toast.error(error instanceof Error ? error.message : t("knowledge.sortUpdateFailed"))
    } finally {
      setSorting(false)
    }
  }

  function applyFilters() {
    setKeyword(keywordInput)
    setStatus(statusInput)
  }

  return (
    <>
      <div className="flex h-full flex-col border-r border-[#dbe7f6] bg-[#f8fbff]">
        <div className="flex flex-col gap-2 border-b border-[#dbe7f6] bg-white/70 p-4">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-semibold">{t("knowledge.title")}</h2>
            <div className="flex items-center gap-1">
              <Button variant="ghost" size="icon" className="size-7" onClick={() => void loadData()} disabled={loading || sorting} aria-label={t("knowledge.refreshDocuments")}>
                <RefreshCwIcon className={loading || sorting ? "size-4 animate-spin" : "size-4"} />
              </Button>
              {canCreate ? (
                <Button variant="ghost" size="icon" className="size-7" onClick={() => setProvisionOpen(true)} aria-label="新建门店知识库">
                  <PlusIcon className="size-4" />
                </Button>
              ) : null}
            </div>
          </div>
          <div className="flex items-center gap-2">
            <div className="relative min-w-0 flex-1">
              <SearchIcon className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={keywordInput}
                onChange={(event) => setKeywordInput(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter") applyFilters()
                }}
                placeholder={t("knowledge.searchBase")}
                className="h-8 pl-8 text-xs"
              />
            </div>
            <OptionCombobox
              value={statusInput}
              onChange={(value) => setStatusInput(value ?? "all")}
              options={options}
              placeholder={t("knowledge.allStatus")}
              searchPlaceholder={t("knowledge.searchBase")}
              emptyText={t("knowledge.emptyBases")}
            />
          </div>
        </div>
        <ScrollArea className="flex-1">
          <div className="space-y-0.5 py-1">
            <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={(event) => void handleDragEnd(event)}>
              <SortableContext items={knowledgeBases.map((item) => item.id)} strategy={verticalListSortingStrategy}>
                {knowledgeBases.map((item) => (
                  <SortableKnowledgeBase
                    key={item.id}
                    item={item}
                    selected={selectedKnowledgeBaseId === item.id}
                    disabled={loading || sorting || !canUpdate}
                    canUpdate={canUpdate}
                    onSelect={() => onSelectKnowledgeBase(item)}
                    onEdit={() => {
                      setEditingItemId(item.id)
                      setEditOpen(true)
                    }}
                    t={t}
                  />
                ))}
              </SortableContext>
            </DndContext>
            {!loading && knowledgeBases.length === 0 ? (
              <div className="py-8 text-center text-sm text-muted-foreground">{t("knowledge.emptyBases")}</div>
            ) : null}
          </div>
        </ScrollArea>
      </div>
      <EditDialog
        open={editOpen}
        saving={saving}
        itemId={editingItemId}
        onOpenChange={(open) => {
          if (!saving) setEditOpen(open)
          if (!open) setEditingItemId(null)
        }}
        onSubmit={submitEdit}
      />
      <FastGPTProvisionDialog open={provisionOpen} onOpenChange={setProvisionOpen} onProvisioned={loadData} />
    </>
  )
}
