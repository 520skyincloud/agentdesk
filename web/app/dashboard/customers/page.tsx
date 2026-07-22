"use client";

import { BanIcon, CheckCircle2Icon, EyeIcon, MessageCircleIcon } from "lucide-react";
import { useMemo, useState } from "react";

import { type CustomerFormSavePayload } from "@/components/customer-form";
import { CustomerTagBadges } from "@/components/customer-tag-badges";
import { CustomerTagHistoryDialog } from "@/components/customer-tag-history-dialog";
import { useAuth } from "@/components/auth-provider";
import {
  DashboardCrudPage,
  createDashboardStatusColumn,
  createDashboardStatusToggleAction,
  type DashboardCrudColumn,
  type DashboardCrudFilter,
} from "@/components/dashboard/crud";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  deleteCustomer,
  fetchCustomers,
  saveCustomerProfile,
  updateCustomerStatus,
  type AdminCustomer,
} from "@/lib/api/customer";
import { Gender, Status } from "@/lib/generated/enums";
import { useI18n } from "@/i18n/provider";
import { EditDialog } from "./_components/edit";

type TFunction = (key: string, values?: Record<string, string | number>) => string;

function getGenderText(gender: number, t: TFunction) {
  if (gender === Gender.Male) return t("customerForm.genderMale");
  if (gender === Gender.Female) return t("customerForm.genderFemale");
  return t("customerForm.genderUnknown");
}

export default function DashboardCustomersPage() {
  const t = useI18n();
  const { session } = useAuth();
  const permissions = useMemo(
    () => new Set(session?.permissions ?? []),
    [session?.permissions],
  );
  const canCreate = permissions.has("customer.create");
  const canUpdate = permissions.has("customer.update");
  const canDelete = permissions.has("customer.delete");
  const canManageCustomerTags = permissions.has("conversation.tag");
  const [detailCustomer, setDetailCustomer] = useState<AdminCustomer | null>(null);

  const listStatusOptions = useMemo(
    () => [
      { value: "all", label: t("status.all") },
      { value: String(Status.Ok), label: t("status.ok") },
      { value: String(Status.Disabled), label: t("status.disabled") },
    ],
    [t],
  );
  const genderOptions = useMemo(
    () => [
      { value: "all", label: t("customer.allGenders") },
      { value: String(Gender.Unknown), label: t("customerForm.genderUnknown") },
      { value: String(Gender.Male), label: t("customerForm.genderMale") },
      { value: String(Gender.Female), label: t("customerForm.genderFemale") },
    ],
    [t],
  );

  const filters = useMemo<DashboardCrudFilter[]>(
    () => [
      {
        name: "keyword",
        label: t("customer.columnName"),
        placeholder: t("customer.keywordPlaceholder"),
        defaultValue: "",
        trim: true,
        className: "w-full sm:w-72",
      },
      {
        name: "gender",
        label: t("customer.columnGender"),
        type: "select",
        defaultValue: "all",
        allValue: "all",
        valueType: "number",
        options: genderOptions,
        className: "w-full sm:w-36",
      },
      {
        name: "status",
        label: t("customer.columnStatus"),
        type: "select",
        defaultValue: "all",
        allValue: "all",
        valueType: "number",
        options: listStatusOptions,
        className: "w-full sm:w-36",
      },
    ],
    [genderOptions, listStatusOptions, t],
  );

  const columns = useMemo<DashboardCrudColumn<AdminCustomer>[]>(
    () => [
      {
        key: "id",
        label: "ID",
        className: "w-20",
        render: (item) => item.id,
      },
      {
        key: "name",
        label: t("customer.columnName"),
        render: (item) => <span className="font-medium">{item.name}</span>,
      },
      {
        key: "gender",
        label: t("customer.columnGender"),
        className: "w-20",
        render: (item) => (
          <span className="text-muted-foreground">
            {getGenderText(item.gender, t)}
          </span>
        ),
      },
      {
        key: "storeRelations",
        label: "门店关系",
        render: (item) => {
          const relations = item.storeRelations ?? [];
          if (relations.length === 0) {
            return <span className="text-muted-foreground">-</span>;
          }
          return (
            <div className="flex max-w-[260px] flex-wrap gap-1.5">
              {relations.slice(0, 2).map((relation) => (
                <span
                  key={relation.id}
                  className="rounded-full border border-border bg-muted/50 px-2 py-0.5 text-xs text-muted-foreground"
                  title={`员工号：${relation.wxWorkInstanceName || relation.wxWorkInstanceId || "-"}`}
                >
                  {relation.storeName || `门店 ${relation.storeId}`} · {relation.visitCount}次
                </span>
              ))}
              {relations.length > 2 ? (
                <span className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">
                  +{relations.length - 2}
                </span>
              ) : null}
            </div>
          );
        },
      },
      {
        key: "mobile",
        label: t("customer.columnMobile"),
        render: (item) => (
          <span className="text-muted-foreground">
            {item.primaryMobile || "-"}
          </span>
        ),
      },
      {
        key: "email",
        label: t("customer.columnEmail"),
        render: (item) => (
          <span className="text-muted-foreground">
            {item.primaryEmail || "-"}
          </span>
        ),
      },
      createDashboardStatusColumn<AdminCustomer, number>({
        label: t("customer.columnStatus"),
        className: "w-24",
        getStatus: (item) => item.status,
        getLabel: (status) =>
          status === Status.Ok ? t("status.ok") : t("status.disabled"),
        getBadgeVariant: (status) =>
          status === Status.Ok ? "default" : "secondary",
      }),
    ],
    [t],
  );

  return (
    <>
      <DashboardCrudPage<AdminCustomer, CustomerFormSavePayload>
      filters={filters}
      columns={columns}
      fetchList={(query) =>
        fetchCustomers({
          keyword:
            typeof query.keyword === "string" ? query.keyword : undefined,
          status:
            typeof query.status === "number" ? query.status : undefined,
          gender:
            typeof query.gender === "number" ? query.gender : undefined,
          page: Number(query.page),
          limit: Number(query.limit),
        })
      }
      getItemId={(item) => item.id}
      createItem={saveCustomerProfile}
      showCreate={canCreate}
      showEdit={canUpdate}
      updateItem={(_item, payload) => saveCustomerProfile(payload)}
      deleteItem={canDelete ? (item) => deleteCustomer(item.id) : undefined}
      canDelete={(item) => item.status !== Status.Deleted}
      rowActions={[
        {
          key: "detail",
          label: "详情",
          icon: <EyeIcon />,
          run: ({ item }) => setDetailCustomer(item),
        },
        ...(canUpdate
          ? [
              createDashboardStatusToggleAction<AdminCustomer, number>({
                icon: (item) =>
                  item.status === Status.Ok ? <BanIcon /> : <CheckCircle2Icon />,
                label: (item) =>
                  item.status === Status.Ok
                    ? t("customer.disable")
                    : t("customer.enable"),
                disabled: (item) => item.status === Status.Deleted,
                getNextStatus: (item) =>
                  item.status === Status.Ok ? Status.Disabled : Status.Ok,
                updateStatus: (item, nextStatus) =>
                  updateCustomerStatus(item.id, nextStatus),
                successMessage: (item, nextStatus) =>
                  t(
                    nextStatus === Status.Ok
                      ? "customer.enabled"
                      : "customer.disabled",
                    { name: item.name },
                  ),
                errorMessage: t("customer.statusUpdateFailed"),
              }),
            ]
          : []),
      ]}
      renderEditDialog={({ open, saving, itemId, onOpenChange, onSubmit }) => (
        <EditDialog
          open={open}
          saving={saving}
          itemId={itemId}
          onOpenChange={onOpenChange}
          onSave={onSubmit}
        />
      )}
      labels={{
        refresh: t("customer.refresh"),
        create: t("customer.new"),
        query: t("customer.query"),
        loading: t("customer.loading"),
        empty: t("customer.empty"),
        actions: t("customer.columnActions"),
        edit: t("customer.edit"),
        delete: t("customer.delete"),
        processing: t("customer.processing"),
        moreActions: (item) => t("customer.moreActions", { name: item.name }),
        loadFailed: t("customer.loadFailed"),
        saveFailed: t("customer.saveFailed"),
        deleteFailed: t("customer.deleteFailed"),
        created: (payload) => t("customer.created", { name: payload.name }),
        updated: (item) => t("customer.updated", { name: item.name }),
        deleted: (item) => t("customer.deleted", { name: item.name }),
      }}
      />
      <Dialog open={!!detailCustomer} onOpenChange={(open) => !open && setDetailCustomer(null)}>
        <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-x-hidden overflow-y-auto sm:max-w-3xl">
          <DialogHeader className="min-w-0">
            <DialogTitle>{detailCustomer?.name || "客户详情"}</DialogTitle>
            <DialogDescription className="sr-only">客户门店关系与标签</DialogDescription>
          </DialogHeader>
          {detailCustomer ? (
            <div className="min-w-0 space-y-5">
              <div className="rounded-lg border bg-muted/40 p-4">
                <div className="flex items-center gap-3">
                  <div className="flex size-12 items-center justify-center overflow-hidden rounded-full bg-background text-sm font-semibold text-foreground ring-1 ring-border">
                    {detailCustomer.avatar ? (
                      // eslint-disable-next-line @next/next/no-img-element
                      <img src={detailCustomer.avatar} alt="" className="size-full object-cover" />
                    ) : (
                      detailCustomer.name.slice(0, 1)
                    )}
                  </div>
                  <div className="min-w-0">
                    <div className="font-medium">{detailCustomer.name}</div>
                    <div className="break-all text-sm text-muted-foreground">
                      {detailCustomer.primaryMobile || "无手机号"} · {detailCustomer.primaryEmail || "无邮箱"}
                    </div>
                  </div>
                </div>
                {detailCustomer.remark ? <div className="mt-3 break-words text-sm text-muted-foreground">{detailCustomer.remark}</div> : null}
              </div>
              <div>
                <div className="mb-2 text-sm font-medium">门店关系</div>
                <div className="space-y-2">
                  {(detailCustomer.storeRelations ?? []).length === 0 ? (
                    <div className="rounded-xl border border-dashed p-4 text-sm text-muted-foreground">暂无门店关系</div>
                  ) : (
                    detailCustomer.storeRelations?.map((relation) => (
                      <div key={relation.id} className="flex flex-col items-start justify-between gap-3 rounded-md border bg-card p-3 shadow-sm sm:flex-row">
                        <div className="min-w-0 flex-1">
                          <div className="font-medium">{relation.storeName || `门店 ${relation.storeId}`}</div>
                          <div className="mt-1 text-xs text-muted-foreground">
                            员工号：{relation.wxWorkInstanceName || relation.wxWorkInstanceId || "-"} · 到访 {relation.visitCount} 次 · 最近 {relation.lastActiveAt || "-"}
                          </div>
                          {relation.stableNotes ? <div className="mt-2 break-words text-sm text-muted-foreground">{relation.stableNotes}</div> : null}
                          <div className="mt-2">
                            <CustomerTagBadges tags={relation.customerTags} />
                            {!relation.customerTags?.length ? (
                              <span className="text-xs text-muted-foreground">暂无门店客户标签</span>
                            ) : null}
                          </div>
                        </div>
                        <div className="flex shrink-0 self-end items-center gap-1 sm:self-start">
                          {canManageCustomerTags && relation.lastConversationId > 0 ? (
                            <CustomerTagHistoryDialog conversationId={relation.lastConversationId} triggerVariant="outline" />
                          ) : null}
                          {relation.lastConversationId > 0 ? (
                            <Button variant="outline" size="sm">
                              <a href={`/dashboard/conversations?conversationId=${relation.lastConversationId}`} className="inline-flex items-center gap-1.5">
                                <MessageCircleIcon className="size-4" />
                                会话
                              </a>
                            </Button>
                          ) : null}
                        </div>
                      </div>
                    ))
                  )}
                </div>
              </div>
            </div>
          ) : null}
        </DialogContent>
      </Dialog>
    </>
  );
}
