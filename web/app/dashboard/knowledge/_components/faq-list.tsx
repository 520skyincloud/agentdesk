"use client";

import { WrenchIcon } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";

import {
  DashboardCrudPage,
  type DashboardCrudActionState,
  type DashboardCrudColumn,
  type DashboardCrudFilter,
} from "@/components/dashboard/crud";
import { Badge } from "@/components/ui/badge";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import {
  buildKnowledgeFAQIndex,
  createKnowledgeFAQ,
  deleteKnowledgeFAQ,
  fetchKnowledgeFAQs,
  updateKnowledgeFAQ,
  type CreateKnowledgeFAQPayload,
  type KnowledgeFAQ,
} from "@/lib/api/admin";
import { KnowledgeDocumentIndexStatus } from "@/lib/generated/enums";
import { useI18n } from "@/i18n/provider";
import { formatDateTime } from "@/lib/utils";
import { FAQEditDialog } from "./faq-edit";
import { FAQImportDialog } from "./faq-import-dialog";

type FAQListProps = {
  knowledgeBaseId: number | null;
  onActionStateChange?: (state: FAQListActionState) => void;
};

export type FAQListActionState = DashboardCrudActionState & {
  onImport: () => void;
  importing: boolean;
};

type TFunction = (key: string, values?: Record<string, string | number>) => string;

function getIndexStatusOptions(t: TFunction) {
  return [
    { value: "all", label: t("knowledge.allIndexStatus") },
    {
      value: KnowledgeDocumentIndexStatus.Pending,
      label: t("knowledge.indexPending"),
    },
    {
      value: KnowledgeDocumentIndexStatus.Indexed,
      label: t("knowledge.indexIndexed"),
    },
    {
      value: KnowledgeDocumentIndexStatus.Failed,
      label: t("knowledge.indexFailed"),
    },
  ];
}

function getIndexStatusLabel(status: string, t: TFunction) {
  if (status === KnowledgeDocumentIndexStatus.Pending) {
    return t("knowledge.indexPending");
  }
  if (status === KnowledgeDocumentIndexStatus.Indexed) {
    return t("knowledge.indexIndexed");
  }
  if (status === KnowledgeDocumentIndexStatus.Failed) {
    return t("knowledge.indexFailed");
  }
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

function renderIndexStatusBadge(item: KnowledgeFAQ, t: TFunction) {
  const badge = (
    <Badge variant={getIndexStatusBadgeVariant(item.indexStatus)}>
      {getIndexStatusLabel(item.indexStatus, t)}
    </Badge>
  );

  if (
    item.indexStatus !== KnowledgeDocumentIndexStatus.Failed ||
    !item.indexError
  ) {
    return badge;
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
  );
}

export function FAQList({
  knowledgeBaseId,
  onActionStateChange,
}: FAQListProps) {
  const t = useI18n();
  const [importing, setImporting] = useState(false);
  const [importDialogOpen, setImportDialogOpen] = useState(false);
  const [crudActionState, setCrudActionState] =
    useState<DashboardCrudActionState | null>(null);
  const indexStatusOptions = useMemo(() => getIndexStatusOptions(t), [t]);

  useEffect(() => {
    if (!crudActionState) {
      return;
    }
    onActionStateChange?.({
      ...crudActionState,
      onImport: () => setImportDialogOpen(true),
      importing,
    });
  }, [crudActionState, importing, onActionStateChange]);

  const filters = useMemo<DashboardCrudFilter[]>(
    () => [
      {
        name: "question",
        label: t("knowledge.searchFAQ"),
        placeholder: t("knowledge.searchFAQ"),
        defaultValue: "",
        trim: true,
        className: "max-w-md flex-1",
      },
      {
        name: "indexStatus",
        label: t("knowledge.allIndexStatus"),
        type: "select",
        defaultValue: "all",
        allValue: "all",
        options: indexStatusOptions,
        className: "w-full sm:w-48",
      },
    ],
    [indexStatusOptions, t],
  );

  const columns = useMemo<DashboardCrudColumn<KnowledgeFAQ>[]>(
    () => [
      {
        key: "question",
        label: t("knowledge.question"),
        className: "max-w-sm",
        render: (item) => (
          <>
            <div className="font-medium">{item.question}</div>
            <div className="mt-1 line-clamp-2 text-sm text-muted-foreground">
              {item.answer}
            </div>
          </>
        ),
      },
      {
        key: "indexStatus",
        label: t("knowledge.indexStatus"),
        render: (item) => renderIndexStatusBadge(item, t),
      },
      {
        key: "similarQuestions",
        label: t("knowledge.similarQuestions"),
        render: (item) =>
          Array.isArray(item.similarQuestions)
            ? item.similarQuestions.length
            : 0,
      },
      {
        key: "updatedAt",
        label: t("knowledge.updatedAt"),
        render: (item) => formatDateTime(item.updatedAt),
      },
    ],
    [t],
  );

  if (!knowledgeBaseId) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        {t("knowledge.selectFAQBase")}
      </div>
    );
  }

  return (
    <>
      <div className="flex h-full flex-col gap-4 p-4">
        <DashboardCrudPage<KnowledgeFAQ, CreateKnowledgeFAQPayload>
          key={knowledgeBaseId}
          layout="fragment"
          showToolbarActions={false}
          filters={filters}
          columns={columns}
          fetchList={(query) =>
            fetchKnowledgeFAQs({
              knowledgeBaseId,
              question:
                typeof query.question === "string" ? query.question : undefined,
              indexStatus:
                typeof query.indexStatus === "string"
                  ? query.indexStatus
                  : undefined,
              page: Number(query.page),
              limit: Number(query.limit),
            })
          }
          getItemId={(item) => item.id}
          createItem={createKnowledgeFAQ}
          updateItem={(item, payload) =>
            updateKnowledgeFAQ({ id: item.id, ...payload })
          }
          deleteItem={(item) => deleteKnowledgeFAQ(item.id)}
          rowActions={[
            {
              key: "rebuild-index",
              icon: <WrenchIcon />,
              label: t("knowledge.rebuildIndex"),
              run: async ({ item, reload }) => {
                try {
                  await buildKnowledgeFAQIndex(item.id);
                  toast.success(t("knowledge.faqIndexRebuilt"));
                  await reload();
                } catch (error) {
                  toast.error(
                    error instanceof Error
                      ? error.message
                      : t("knowledge.faqIndexRebuildFailed"),
                  );
                }
              },
            },
          ]}
          renderEditDialog={({
            open,
            saving,
            itemId,
            onOpenChange,
            onSubmit,
          }) => (
            <FAQEditDialog
              open={open}
              saving={saving}
              itemId={itemId}
              knowledgeBaseId={knowledgeBaseId}
              onOpenChange={onOpenChange}
              onSubmit={onSubmit}
            />
          )}
          onActionStateChange={setCrudActionState}
          labels={{
            refresh: t("knowledge.refreshFAQ"),
            create: t("knowledge.newFAQ"),
            query: t("knowledge.query"),
            loading: t("knowledge.loadingFAQ"),
            empty: t("knowledge.emptyFAQ"),
            actions: t("knowledge.actions"),
            edit: t("knowledge.edit"),
            delete: t("knowledge.delete"),
            processing: t("knowledge.deleting"),
            moreActions: (item) =>
              t("knowledge.moreActions", { name: item.question }),
            loadFailed: t("knowledge.loadFAQFailed"),
            saveFailed: t("knowledge.faqSaveFailed"),
            deleteFailed: t("knowledge.faqDeleteFailed"),
            created: () => t("knowledge.faqSaved"),
            updated: () => t("knowledge.faqSaved"),
            deleted: () => t("knowledge.faqDeleted"),
          }}
        />
      </div>

      <FAQImportDialog
        open={importDialogOpen}
        knowledgeBaseId={knowledgeBaseId}
        importing={importing}
        onOpenChange={setImportDialogOpen}
        onImportingChange={setImporting}
        onImported={async () => {
          crudActionState?.onRefresh();
        }}
      />
    </>
  );
}
