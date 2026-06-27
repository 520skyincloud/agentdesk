"use client";

import {
  FileTextIcon,
  MoreHorizontalIcon,
  PencilIcon,
  SearchIcon,
  Trash2Icon,
  WrenchIcon,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState, type KeyboardEvent } from "react";
import { toast } from "sonner";

import {
  useDashboardPagedList,
  type DashboardPagedListFilter,
} from "@/components/dashboard/list";
import { ListPagination } from "@/components/list-pagination";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
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
import { OptionCombobox } from "@/components/option-combobox";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import {
  buildKnowledgeDocumentIndex,
  createKnowledgeDocument,
  deleteKnowledgeDocument,
  fetchKnowledgeDocuments,
  updateKnowledgeDocument,
  type CreateKnowledgeDocumentPayload,
  type KnowledgeDocumentListItem,
} from "@/lib/api/admin";
import { useI18n } from "@/i18n/provider";
import {
  KnowledgeDocumentIndexStatus,
  Status,
} from "@/lib/generated/enums";
import { cn, formatDateTime } from "@/lib/utils";
import { DocumentEditDialog } from "./document-edit";

type DocumentListProps = {
  knowledgeBaseId: number | null;
  onActionStateChange?: (state: DocumentListActionState) => void;
};

export type DocumentListActionState = {
  onRefresh: () => void;
  onChangeViewMode: (mode: "list" | "grid") => void;
  onCreate: () => void;
  viewMode: "list" | "grid";
  loading: boolean;
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

function getIndexStatusOptions(t: TFunction) {
  return [
    { value: "all", label: t("knowledge.allIndexStatus") },
    { value: KnowledgeDocumentIndexStatus.Pending, label: t("knowledge.indexPending") },
    { value: KnowledgeDocumentIndexStatus.Indexed, label: t("knowledge.indexIndexed") },
    { value: KnowledgeDocumentIndexStatus.Failed, label: t("knowledge.indexFailed") },
  ];
}

function getIndexStatusLabel(status: string, t: TFunction) {
  if (status === KnowledgeDocumentIndexStatus.Pending) return t("knowledge.indexPending");
  if (status === KnowledgeDocumentIndexStatus.Indexed) return t("knowledge.indexIndexed");
  if (status === KnowledgeDocumentIndexStatus.Failed) return t("knowledge.indexFailed");
  return status || "-";
}

function getIndexStatusBadgeVariant(status: string) {
  switch (status) {
    case KnowledgeDocumentIndexStatus.Indexed:
      return "secondary" as const;
    case KnowledgeDocumentIndexStatus.Failed:
      return "destructive" as const;
    default:
      return "outline" as const;
  }
}

function renderIndexStatusBadge(item: KnowledgeDocumentListItem, t: TFunction) {
  const badge = (
    <Badge variant={getIndexStatusBadgeVariant(item.indexStatus)}>
      {getIndexStatusLabel(item.indexStatus, t)}
    </Badge>
  )

  if (
    item.indexStatus !== KnowledgeDocumentIndexStatus.Failed ||
    !item.indexError
  ) {
    return badge
  }

  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger>
          <span className="inline-flex">{badge}</span>
        </TooltipTrigger>
        <TooltipContent align="start" className="max-w-sm whitespace-normal">
          {item.indexError}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

const VIEW_MODE_STORAGE_KEY = "knowledge-document-view-mode";

export function DocumentList({ knowledgeBaseId, onActionStateChange }: DocumentListProps) {
  const t = useI18n();
  const [saving, setSaving] = useState(false);
  const [actionLoadingMap, setActionLoadingMap] = useState<Record<number, { rebuildIndex: boolean; delete: boolean }>>({});
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingItem, setEditingItem] = useState<KnowledgeDocumentListItem | null>(
    null,
  );
  const [viewMode, setViewMode] = useState<"list" | "grid">(() => {
    if (typeof window === "undefined") return "grid";
    const saved = localStorage.getItem(VIEW_MODE_STORAGE_KEY);
    return saved === "list" || saved === "grid" ? saved : "grid";
  });
  const statusOptions = useMemo(() => getStatusOptions(t), [t]);
  const indexStatusOptions = useMemo(() => getIndexStatusOptions(t), [t]);

  const filters = useMemo<DashboardPagedListFilter[]>(() => [
    {
      name: "title",
      defaultValue: "",
      trim: true,
    },
    {
      name: "status",
      defaultValue: "all",
      allValue: "all",
    },
    {
      name: "indexStatus",
      defaultValue: "all",
      allValue: "all",
    },
  ], []);

  const fetchList = useCallback(async (query: Record<string, string | number | undefined>) => {
    return fetchKnowledgeDocuments({
      title: typeof query.title === "string" ? query.title : undefined,
      status: typeof query.status === "string" ? query.status : undefined,
      indexStatus: typeof query.indexStatus === "string" ? query.indexStatus : undefined,
      knowledgeBaseId: knowledgeBaseId ?? 0,
      page: typeof query.page === "number" ? query.page : Number(query.page ?? 1),
      limit: typeof query.limit === "number" ? query.limit : Number(query.limit ?? 20),
    });
  }, [knowledgeBaseId]);

  const {
    draftFilters,
    setDraftFilter,
    applyFilter,
    applyFilters: applyDraftFilters,
    loading,
    result: documents,
    loadData,
    handlePageChange,
    handleLimitChange,
  } = useDashboardPagedList<KnowledgeDocumentListItem>({
    filters,
    fetchList,
    enabled: Boolean(knowledgeBaseId),
    reloadKey: knowledgeBaseId,
    loadFailed: t("knowledge.loadDocumentsFailed"),
  });

  useEffect(() => {
    localStorage.setItem(VIEW_MODE_STORAGE_KEY, viewMode);
  }, [viewMode]);

  function handleStatusFilterChange(value: string | null) {
    applyFilter("status", value ?? "all");
  }

  function handleIndexStatusFilterChange(value: string | null) {
    applyFilter("indexStatus", value ?? "all");
  }

  function applyFilters() {
    applyDraftFilters();
  }

  function handleFilterKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key !== "Enter") {
      return;
    }
    event.preventDefault();
    applyFilters();
  }

  const openCreateDialog = useCallback(() => {
    setEditingItem(null);
    setDialogOpen(true);
  }, []);

  useEffect(() => {
    if (!onActionStateChange) {
      return;
    }

    onActionStateChange({
      onRefresh: () => void loadData(),
      onChangeViewMode: setViewMode,
      onCreate: openCreateDialog,
      viewMode,
      loading,
    });
  }, [onActionStateChange, loadData, openCreateDialog, viewMode, loading]);

  function openEditDialog(item: KnowledgeDocumentListItem) {
    setEditingItem(item);
    setDialogOpen(true);
  }

  function handleDialogOpenChange(open: boolean) {
    if (saving) {
      return;
    }
    if (!open) {
      setEditingItem(null);
    }
    setDialogOpen(open);
  }

  async function handleSubmit(payload: CreateKnowledgeDocumentPayload) {
    if (saving) {
      return;
    }

    setSaving(true);
    try {
      if (editingItem) {
        await updateKnowledgeDocument({
          id: editingItem.id,
          ...payload,
        });
        toast.success(t("knowledge.documentUpdated", { title: editingItem.title }));
      } else {
        await createKnowledgeDocument(payload);
        toast.success(t("knowledge.documentCreated", { title: payload.title }));
      }
      setDialogOpen(false);
      setEditingItem(null);
      await loadData();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("knowledge.documentSaveFailed"));
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete(item: KnowledgeDocumentListItem) {
    setActionLoadingMap((prev) => ({ ...prev, [item.id]: { ...prev[item.id], delete: true } }));
    try {
      await deleteKnowledgeDocument(item.id);
      toast.success(t("knowledge.documentDeleted", { title: item.title }));
      await loadData();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("knowledge.documentDeleteFailed"));
    } finally {
      setActionLoadingMap((prev) => ({ ...prev, [item.id]: { ...prev[item.id], delete: false } }));
    }
  }

  async function handleBuildIndex(item: KnowledgeDocumentListItem) {
    setActionLoadingMap((prev) => ({ ...prev, [item.id]: { ...prev[item.id], rebuildIndex: true } }));
    try {
      await buildKnowledgeDocumentIndex(item.id);
      toast.success(t("knowledge.documentIndexRebuilt", { title: item.title }));
      await loadData();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("knowledge.documentIndexRebuildFailed"));
    } finally {
      setActionLoadingMap((prev) => ({ ...prev, [item.id]: { ...prev[item.id], rebuildIndex: false } }));
    }
  }

  if (!knowledgeBaseId) {
    return (
      <div className="flex h-full flex-col items-center justify-center text-muted-foreground">
        <FileTextIcon className="mb-2 size-12 opacity-50" />
        <p>{t("knowledge.selectKnowledgeBaseForDocuments")}</p>
      </div>
    );
  }

  return (
    <>
      <div className="flex h-full min-h-0 flex-col">
        <div className="flex flex-col gap-2 border-b bg-background px-6 py-2">
          <div className="flex gap-2">
            <div className="relative flex-1">
              <SearchIcon className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={String(draftFilters.title ?? "")}
                onChange={(event) => setDraftFilter("title", event.target.value)}
                onKeyDown={handleFilterKeyDown}
                placeholder={t("knowledge.searchDocumentTitle")}
                className="h-8 pl-8 text-xs"
              />
            </div>
            <OptionCombobox
              value={String(draftFilters.status ?? "all")}
              onChange={handleStatusFilterChange}
              options={statusOptions}
              placeholder={t("knowledge.allStatus")}
              searchPlaceholder={t("knowledge.searchStatus")}
              emptyText={t("knowledge.emptyStatus")}
            />
            <OptionCombobox
              value={String(draftFilters.indexStatus ?? "all")}
              onChange={handleIndexStatusFilterChange}
              options={indexStatusOptions}
              placeholder={t("knowledge.allIndexStatus")}
              searchPlaceholder={t("knowledge.searchStatus")}
              emptyText={t("knowledge.emptyStatus")}
            />
          </div>
        </div>
        <div className="min-h-0 flex-1">
          <ScrollArea className="h-full">
            <div className={viewMode === "grid" ? "p-2 space-y-1" : "p-2 space-y-0.5"}>
            {documents.results.map((item) => (
              viewMode === "grid" ? (
                <ContextMenu key={item.id}>
                  <ContextMenuTrigger className="w-full">
                    <div
                      className="bg-background p-3 transition-colors hover:bg-accent w-full"
                    >
                      <div className="flex items-start justify-between gap-2">
                        <div className="flex min-w-0 flex-1 items-start gap-2">
                          {/* <FileTextIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" /> */}
                          <div className="min-w-0 flex-1">
                            <div className="flex items-center gap-2">
                              <div className="text-sm font-medium">{item.title}</div>
                              {renderIndexStatusBadge(item, t)}
                            </div>
                            <div className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
                              <span>{item.createUserName || "-"}</span>
                              <span>{formatDateTime(item.createdAt)}</span>
                              <span className={cn(item.indexStatus === KnowledgeDocumentIndexStatus.Failed && "text-destructive")}>
                                {item.indexStatus === KnowledgeDocumentIndexStatus.Indexed
                                  ? t("knowledge.indexedAt", { time: formatDateTime(item.indexedAt) })
                                  : getIndexStatusLabel(item.indexStatus, t)}
                              </span>
                            </div>
                          </div>
                        </div>
                        <DropdownMenu>
                          <DropdownMenuTrigger
                            render={
                              <Button
                                variant="ghost"
                                size="icon"
                                className="size-6"
                              />
                            }
                            aria-label={t("knowledge.moreActions", { name: item.title })}
                          >
                            <MoreHorizontalIcon className="size-3.5" />
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end" className="w-32 min-w-32">
                            <DropdownMenuItem onClick={() => openEditDialog(item)}>
                              <PencilIcon className="mr-2 size-3.5" />
                              {t("knowledge.edit")}
                            </DropdownMenuItem>
                            <DropdownMenuItem onClick={() => void handleBuildIndex(item)}>
                              <WrenchIcon className="mr-2 size-3.5" />
                              {actionLoadingMap[item.id]?.rebuildIndex ? t("knowledge.running") : t("knowledge.rebuildIndex")}
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              onClick={() => void handleDelete(item)}
                              className="text-destructive focus:text-destructive"
                            >
                              <Trash2Icon className="mr-2 size-3.5" />
                              {actionLoadingMap[item.id]?.delete ? t("knowledge.deleting") : t("knowledge.delete")}
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </div>
                    </div>
                  </ContextMenuTrigger>
                  <ContextMenuContent className="w-40">
                    <ContextMenuItem onClick={() => openEditDialog(item)}>
                      <PencilIcon className="mr-2 size-3.5" />
                      {t("knowledge.edit")}
                    </ContextMenuItem>
                    <ContextMenuItem onClick={() => void handleBuildIndex(item)} disabled={actionLoadingMap[item.id]?.rebuildIndex}>
                      <WrenchIcon className="mr-2 size-3.5" />
                      {actionLoadingMap[item.id]?.rebuildIndex ? t("knowledge.running") : t("knowledge.rebuildIndex")}
                    </ContextMenuItem>
                    <ContextMenuItem
                      onClick={() => void handleDelete(item)}
                      variant="destructive"
                      disabled={actionLoadingMap[item.id]?.delete}
                    >
                      <Trash2Icon className="mr-2 size-3.5" />
                      {actionLoadingMap[item.id]?.delete ? t("knowledge.deleting") : t("knowledge.delete")}
                    </ContextMenuItem>
                  </ContextMenuContent>
                </ContextMenu>
              ) : (
                <ContextMenu key={item.id}>
                  <ContextMenuTrigger className="w-full">
                    <div
                      className="flex items-center gap-3 bg-background p-2 transition-colors hover:bg-accent w-full"
                    >
                      {/* <FileTextIcon className="size-4 shrink-0 text-muted-foreground" /> */}
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                          <div className="truncate text-sm font-medium">{item.title}</div>
                          {renderIndexStatusBadge(item, t)}
                        </div>
                        <div className="mt-1 text-xs text-muted-foreground">
                          {item.indexStatus === KnowledgeDocumentIndexStatus.Indexed
                            ? t("knowledge.indexTime", { time: formatDateTime(item.indexedAt) })
                            : item.indexError || getIndexStatusLabel(item.indexStatus, t)}
                        </div>
                      </div>
                      <div className="shrink-0 text-xs text-muted-foreground">
                        {formatDateTime(item.createdAt)}
                      </div>
                      <DropdownMenu>
                        <DropdownMenuTrigger
                          render={
                            <Button
                              variant="ghost"
                              size="icon"
                              className="size-6"
                            />
                          }
                          aria-label={t("knowledge.moreActions", { name: item.title })}
                        >
                          <MoreHorizontalIcon className="size-3.5" />
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end" className="w-32 min-w-32">
                          <DropdownMenuItem onClick={() => openEditDialog(item)}>
                            <PencilIcon className="mr-2 size-3.5" />
                            {t("knowledge.edit")}
                          </DropdownMenuItem>
                          <DropdownMenuItem onClick={() => void handleBuildIndex(item)}>
                            <WrenchIcon className="mr-2 size-3.5" />
                            {actionLoadingMap[item.id]?.rebuildIndex ? t("knowledge.running") : t("knowledge.rebuildIndex")}
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onClick={() => void handleDelete(item)}
                            className="text-destructive focus:text-destructive"
                          >
                            <Trash2Icon className="mr-2 size-3.5" />
                            {actionLoadingMap[item.id]?.delete ? t("knowledge.deleting") : t("knowledge.delete")}
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </div>
                  </ContextMenuTrigger>
                  <ContextMenuContent className="w-40">
                    <ContextMenuItem onClick={() => openEditDialog(item)}>
                      <PencilIcon className="mr-2 size-3.5" />
                      {t("knowledge.edit")}
                    </ContextMenuItem>
                    <ContextMenuItem onClick={() => void handleBuildIndex(item)} disabled={actionLoadingMap[item.id]?.rebuildIndex}>
                      <WrenchIcon className="mr-2 size-3.5" />
                      {actionLoadingMap[item.id]?.rebuildIndex ? t("knowledge.running") : t("knowledge.rebuildIndex")}
                    </ContextMenuItem>
                    <ContextMenuItem
                      onClick={() => void handleDelete(item)}
                      variant="destructive"
                      disabled={actionLoadingMap[item.id]?.delete}
                    >
                      <Trash2Icon className="mr-2 size-3.5" />
                      {actionLoadingMap[item.id]?.delete ? t("knowledge.deleting") : t("knowledge.delete")}
                    </ContextMenuItem>
                  </ContextMenuContent>
                </ContextMenu>
              )
            ))}
            {!loading && documents.results.length === 0 ? (
              <div className="py-8 text-center text-sm text-muted-foreground">
                {t("knowledge.emptyDocuments")}
              </div>
            ) : null}
            </div>
          </ScrollArea>
        </div>
        <div className="border-t px-6 py-3">
          <ListPagination
            page={documents.page.page}
            limit={documents.page.limit}
            total={documents.page.total}
            loading={loading}
            onPageChange={handlePageChange}
            onLimitChange={handleLimitChange}
          />
        </div>
      </div>
      <DocumentEditDialog
        open={dialogOpen}
        saving={saving}
        itemId={editingItem?.id ?? null}
        knowledgeBaseId={knowledgeBaseId}
        onOpenChange={handleDialogOpenChange}
        onSubmit={handleSubmit}
      />
    </>
  );
}
