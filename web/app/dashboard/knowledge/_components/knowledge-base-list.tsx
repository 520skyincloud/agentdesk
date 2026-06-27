"use client";

import {
  DndContext,
  KeyboardSensor,
  MouseSensor,
  TouchSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  arrayMove,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import {
  CircleHelpIcon,
  CloudIcon,
  FileTextIcon,
  MoreHorizontalIcon,
  PencilIcon,
  PlusIcon,
  RefreshCwIcon,
  SearchIcon,
  Trash2Icon,
} from "lucide-react";
import type { CSSProperties } from "react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { OptionCombobox } from "@/components/option-combobox";
import {
  createKnowledgeBase,
  deleteKnowledgeBase,
  fetchKnowledgeBases,
  rebuildKnowledgeBaseIndex,
  updateKnowledgeBase,
  updateKnowledgeBaseSort,
  type CreateKnowledgeBasePayload,
  type KnowledgeBase,
} from "@/lib/api/admin";
import { useI18n } from "@/i18n/provider";
import { KnowledgeBaseType, Status } from "@/lib/generated/enums";
import { cn } from "@/lib/utils";
import { EditDialog } from "./knowledge-base-edit";

type KnowledgeBaseListProps = {
  selectedKnowledgeBaseId: number | null;
  onSelectKnowledgeBase: (knowledgeBase: KnowledgeBase | null) => void;
};

type TFunction = (key: string, values?: Record<string, string | number>) => string;

function getStatusOptions(t: TFunction) {
  return [
    { value: "all", label: t("knowledge.allStatus") },
    { value: String(Status.Ok), label: t("knowledge.statusOk") },
    { value: String(Status.Disabled), label: t("knowledge.statusDisabled") },
    { value: String(Status.Deleted), label: t("knowledge.statusDeleted") },
  ];
}

type SortableKnowledgeBaseCardProps = {
  item: KnowledgeBase;
  isSelected: boolean;
  disabled: boolean;
  onSelect: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onRebuildIndex: () => void;
  deleteLoadingId: number | null;
  rebuildIndexLoadingId: number | null;
  t: TFunction;
};

function SortableKnowledgeBaseCard({
  item,
  isSelected,
  disabled,
  onSelect,
  onEdit,
  onDelete,
  onRebuildIndex,
  deleteLoadingId,
  rebuildIndexLoadingId,
  t,
}: SortableKnowledgeBaseCardProps) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({
    id: item.id,
    disabled,
  });

  const style: CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
  };

  return (
    <ContextMenu>
      <ContextMenuTrigger
        ref={setNodeRef}
        style={style}
        className={cn(
          "group flex items-center gap-1 rounded mx-2 px-2 py-1.5 text-sm transition-colors hover:bg-accent cursor-pointer",
          isSelected && "bg-accent text-accent-foreground",
          isDragging && "bg-muted/60 shadow-sm opacity-80",
        )}
        onClick={onSelect}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onSelect();
          }
        }}
        {...attributes}
        {...listeners}
      >
        {item.knowledgeType === KnowledgeBaseType.FastGPTCloud ? (
          <CloudIcon className="size-4 shrink-0 text-muted-foreground" />
        ) : item.knowledgeType === KnowledgeBaseType.FAQ ? (
          <CircleHelpIcon className="size-4 shrink-0 text-muted-foreground" />
        ) : (
          <FileTextIcon className="size-4 shrink-0 text-muted-foreground" />
        )}
        <span className="min-w-0 flex-1 truncate">{item.name}</span>
        <span className="shrink-0 text-xs text-muted-foreground">
          {item.knowledgeType === KnowledgeBaseType.FastGPTCloud ? "-" : item.knowledgeType === KnowledgeBaseType.FAQ ? item.faqCount : item.documentCount}
        </span>
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button
                variant="ghost"
                size="icon"
                className="size-6 opacity-0 group-hover:opacity-100"
              />
            }
            aria-label={t("knowledge.moreActions", { name: item.name })}
          >
            <MoreHorizontalIcon className="size-3.5" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-40 min-w-40">
            <DropdownMenuItem
              onClick={(e) => {
                e.stopPropagation();
                onEdit();
              }}
            >
              <PencilIcon className="mr-2 size-3.5" />
              {t("knowledge.edit")}
            </DropdownMenuItem>
            {item.knowledgeType !== KnowledgeBaseType.FastGPTCloud ? (
              <DropdownMenuItem
                onClick={(e) => {
                  e.stopPropagation();
                  onRebuildIndex();
                }}
              >
                <RefreshCwIcon className="mr-2 size-3.5" />
                {rebuildIndexLoadingId === item.id ? t("knowledge.rebuilding") : t("knowledge.rebuildIndex")}
              </DropdownMenuItem>
            ) : null}
            <DropdownMenuItem
              onClick={(e) => {
                e.stopPropagation();
                onDelete();
              }}
              className="text-destructive focus:text-destructive"
            >
              <Trash2Icon className="mr-2 size-3.5" />
              {deleteLoadingId === item.id ? t("knowledge.deleting") : t("knowledge.delete")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem
          onClick={(e) => {
            e.stopPropagation();
            onEdit();
          }}
        >
          <PencilIcon className="mr-2 size-3.5" />
          {t("knowledge.edit")}
        </ContextMenuItem>
        {item.knowledgeType !== KnowledgeBaseType.FastGPTCloud ? (
          <ContextMenuItem
            onClick={(e) => {
              e.stopPropagation();
              onRebuildIndex();
            }}
          >
            <RefreshCwIcon className="mr-2 size-3.5" />
            {rebuildIndexLoadingId === item.id ? t("knowledge.rebuilding") : t("knowledge.rebuildIndex")}
          </ContextMenuItem>
        ) : null}
        <ContextMenuItem
          onClick={(e) => {
            e.stopPropagation();
            onDelete();
          }}
          variant="destructive"
        >
          <Trash2Icon className="mr-2 size-3.5" />
          {deleteLoadingId === item.id ? t("knowledge.deleting") : t("knowledge.delete")}
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

export function KnowledgeBaseList({
  selectedKnowledgeBaseId,
  onSelectKnowledgeBase,
}: KnowledgeBaseListProps) {
  const t = useI18n();
  const [keywordInput, setKeywordInput] = useState("");
  const [statusFilterInput, setStatusFilterInput] = useState("all");
  const [keyword, setKeyword] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [sorting, setSorting] = useState(false);
  const [deleteLoadingId, setDeleteLoadingId] = useState<number | null>(null);
  const [rebuildIndexLoadingId, setRebuildIndexLoadingId] = useState<
    number | null
  >(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingItemId, setEditingItemId] = useState<number | null>(null);
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([]);
  const statusOptions = useMemo(() => getStatusOptions(t), [t]);

  const sensors = useSensors(
    useSensor(MouseSensor, {
      activationConstraint: { distance: 8 },
    }),
    useSensor(TouchSensor, {
      activationConstraint: { delay: 150, tolerance: 8 },
    }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  );

  const loadData = useCallback(async () => {
    setLoading(true);
    try {
      const data = await fetchKnowledgeBases({
        name: keyword.trim() || undefined,
        status: statusFilter === "all" ? undefined : statusFilter,
        limit: 1000,
      });
      setKnowledgeBases(data.results);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("knowledge.loadBasesFailed"));
    } finally {
      setLoading(false);
    }
  }, [keyword, statusFilter, t]);

  useEffect(() => {
    void loadData();
  }, [loadData]);

  useEffect(() => {
    if (
      selectedKnowledgeBaseId === null &&
      knowledgeBases.length > 0 &&
      !loading
    ) {
      onSelectKnowledgeBase(knowledgeBases[0]);
    }
  }, [selectedKnowledgeBaseId, knowledgeBases, loading, onSelectKnowledgeBase]);

  function handleStatusFilterChange(value: string | null) {
    setStatusFilterInput(value ?? "all");
  }

  function applyFilters() {
    setKeyword(keywordInput);
    setStatusFilter(statusFilterInput);
  }

  function handleFilterKeyDown(event: React.KeyboardEvent<HTMLInputElement>) {
    if (event.key !== "Enter") {
      return;
    }
    event.preventDefault();
    applyFilters();
  }

  function openCreateDialog() {
    setEditingItemId(null);
    setDialogOpen(true);
  }

  function openEditDialog(item: KnowledgeBase) {
    setEditingItemId(item.id);
    setDialogOpen(true);
  }

  function handleDialogOpenChange(open: boolean) {
    if (saving) {
      return;
    }
    if (!open) {
      setEditingItemId(null);
    }
    setDialogOpen(open);
  }

  async function handleSubmit(payload: CreateKnowledgeBasePayload) {
    if (saving) {
      return;
    }

    setSaving(true);
    try {
      if (editingItemId) {
        await updateKnowledgeBase({
          id: editingItemId,
          ...payload,
        });
        const editingItem = knowledgeBases.find(
          (item) => item.id === editingItemId,
        );
        toast.success(t("knowledge.baseUpdated", { name: editingItem?.name || payload.name }));
      } else {
        await createKnowledgeBase(payload);
        toast.success(t("knowledge.baseCreated", { name: payload.name }));
      }
      setDialogOpen(false);
      setEditingItemId(null);
      await loadData();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("knowledge.baseSaveFailed"));
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete(item: KnowledgeBase) {
    setDeleteLoadingId(item.id);
    try {
      await deleteKnowledgeBase(item.id);
      toast.success(t("knowledge.baseDeleted", { name: item.name }));
      if (selectedKnowledgeBaseId === item.id) {
        onSelectKnowledgeBase(null);
      }
      await loadData();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("knowledge.baseDeleteFailed"));
    } finally {
      setDeleteLoadingId(null);
    }
  }

  async function handleRebuildIndex(item: KnowledgeBase) {
    setRebuildIndexLoadingId(item.id);
    try {
      await rebuildKnowledgeBaseIndex(item.id);
      toast.success(t("knowledge.rebuildStarted", { name: item.name }));
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t("knowledge.rebuildFailed"),
      );
    } finally {
      setRebuildIndexLoadingId(null);
    }
  }

  async function handleDragEnd(event: DragEndEvent) {
    const { active, over } = event;
    if (!over || active.id === over.id || sorting) {
      return;
    }

    const previousResults = knowledgeBases;
    const oldIndex = previousResults.findIndex((item) => item.id === active.id);
    const newIndex = previousResults.findIndex((item) => item.id === over.id);
    if (oldIndex < 0 || newIndex < 0) {
      return;
    }

    const nextResults = arrayMove(previousResults, oldIndex, newIndex);
    setKnowledgeBases(nextResults);
    setSorting(true);

    try {
      await updateKnowledgeBaseSort(nextResults.map((item) => item.id));
      toast.success(t("knowledge.sortUpdated"));
      await loadData();
    } catch (error) {
      setKnowledgeBases(previousResults);
      toast.error(
        error instanceof Error ? error.message : t("knowledge.sortUpdateFailed"),
      );
    } finally {
      setSorting(false);
    }
  }

  return (
    <>
      <div className="flex h-full flex-col border-r bg-muted/30">
        <div className="flex flex-col gap-2 border-b bg-background p-4">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-semibold">{t("knowledge.title")}</h2>
            <div className="flex items-center gap-1">
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                onClick={() => void loadData()}
                disabled={loading || sorting}
              >
                <RefreshCwIcon
                  className={loading || sorting ? "animate-spin" : "size-4"}
                />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                onClick={openCreateDialog}
              >
                <PlusIcon className="size-4" />
              </Button>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <div className="relative flex-1">
              <SearchIcon className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={keywordInput}
                onChange={(event) => setKeywordInput(event.target.value)}
                onKeyDown={handleFilterKeyDown}
                placeholder={t("knowledge.searchBase")}
                className="h-8 pl-8 text-xs"
              />
            </div>
            <OptionCombobox
              value={statusFilterInput}
              onChange={handleStatusFilterChange}
              options={statusOptions}
              placeholder={t("knowledge.allStatus")}
              searchPlaceholder={t("knowledge.searchBase")}
              emptyText={t("knowledge.emptyBases")}
            />
          </div>
        </div>
        <ScrollArea className="flex-1">
          <div className="py-1 space-y-0.5">
            <DndContext
              sensors={sensors}
              collisionDetection={closestCenter}
              onDragEnd={(event) => void handleDragEnd(event)}
            >
              <SortableContext
                items={knowledgeBases.map((item) => item.id)}
                strategy={verticalListSortingStrategy}
              >
                {knowledgeBases.map((item) => (
                  <SortableKnowledgeBaseCard
                    key={item.id}
                    item={item}
                    isSelected={selectedKnowledgeBaseId === item.id}
                    disabled={loading || sorting}
                    onSelect={() => onSelectKnowledgeBase(item)}
                    onEdit={() => openEditDialog(item)}
                    onDelete={() => void handleDelete(item)}
                    onRebuildIndex={() => void handleRebuildIndex(item)}
                    deleteLoadingId={deleteLoadingId}
                    rebuildIndexLoadingId={rebuildIndexLoadingId}
                    t={t}
                  />
                ))}
              </SortableContext>
            </DndContext>
            {!loading && knowledgeBases.length === 0 ? (
              <div className="py-8 text-center text-sm text-muted-foreground">
                {t("knowledge.emptyBases")}
              </div>
            ) : null}
          </div>
        </ScrollArea>
      </div>
      <EditDialog
        open={dialogOpen}
        saving={saving}
        itemId={editingItemId}
        onOpenChange={handleDialogOpenChange}
        onSubmit={handleSubmit}
      />
    </>
  );
}
