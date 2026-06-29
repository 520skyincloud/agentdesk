"use client";

import { BanIcon, CheckCircle2Icon, EyeIcon, MessageCircleIcon } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { type CustomerFormSavePayload } from "@/components/customer-form";
import {
  DashboardCrudPage,
  createDashboardStatusColumn,
  createDashboardStatusToggleAction,
  type DashboardCrudColumn,
  type DashboardCrudFilter,
} from "@/components/dashboard/crud";
import { type ComboboxOption } from "@/components/option-combobox";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { fetchCompanies, type AdminCompany } from "@/lib/api/company";
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
  const [companyOptions, setCompanyOptions] = useState<ComboboxOption[]>([
    { value: "0", label: t("customer.allCompanies") },
  ]);
  const [companyNameMap, setCompanyNameMap] = useState<Record<number, string>>(
    {},
  );
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

  useEffect(() => {
    async function loadCompanies() {
      try {
        const data = await fetchCompanies({ status: 0, page: 1, limit: 500 });
        setCompanyOptions([
          { value: "0", label: t("customer.allCompanies") },
          ...data.results.map((item) => ({
            value: String(item.id),
            label: item.name,
          })),
        ]);
        const map: Record<number, string> = {};
        data.results.forEach((item: AdminCompany) => {
          map[item.id] = item.name;
        });
        setCompanyNameMap(map);
      } catch {
        // Company names are optional display enrichment for this list.
      }
    }
    void loadCompanies();
  }, [t]);

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
        name: "companyId",
        label: t("customer.columnCompany"),
        type: "select",
        defaultValue: "0",
        allValue: "0",
        valueType: "number",
        options: companyOptions,
        className: "w-full sm:w-56",
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
    [companyOptions, genderOptions, listStatusOptions, t],
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
        key: "company",
        label: t("customer.columnCompany"),
        render: (item) => (
          <span className="text-muted-foreground">
            {item.companyId > 0
              ? (companyNameMap[item.companyId] ?? String(item.companyId))
              : "-"}
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
                  className="rounded-full border border-slate-200 bg-slate-50 px-2 py-0.5 text-xs text-slate-600"
                  title={`员工号：${relation.wxWorkInstanceName || relation.wxWorkInstanceId || "-"}`}
                >
                  {relation.storeName || `门店 ${relation.storeId}`} · {relation.visitCount}次
                </span>
              ))}
              {relations.length > 2 ? (
                <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-500">
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
    [companyNameMap, t],
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
          companyId:
            typeof query.companyId === "number" ? query.companyId : undefined,
          page: Number(query.page),
          limit: Number(query.limit),
        })
      }
      getItemId={(item) => item.id}
      createItem={saveCustomerProfile}
      updateItem={(_item, payload) => saveCustomerProfile(payload)}
      deleteItem={(item) => deleteCustomer(item.id)}
      canDelete={(item) => item.status !== Status.Deleted}
      rowActions={[
        {
          key: "detail",
          label: "详情",
          icon: <EyeIcon />,
          run: ({ item }) => setDetailCustomer(item),
        },
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
            t(nextStatus === Status.Ok ? "customer.enabled" : "customer.disabled", {
              name: item.name,
            }),
          errorMessage: t("customer.statusUpdateFailed"),
        }),
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
        <DialogContent className="max-w-3xl">
          <DialogHeader>
            <DialogTitle>{detailCustomer?.name || "客户详情"}</DialogTitle>
            <DialogDescription>同一自然客户在不同门店下保留独立关系和上下文。</DialogDescription>
          </DialogHeader>
          {detailCustomer ? (
            <div className="space-y-5">
              <div className="rounded-2xl border bg-slate-50/70 p-4">
                <div className="flex items-center gap-3">
                  <div className="flex size-12 items-center justify-center overflow-hidden rounded-full bg-white text-sm font-semibold text-slate-600 ring-1 ring-slate-200">
                    {detailCustomer.avatar ? (
                      // eslint-disable-next-line @next/next/no-img-element
                      <img src={detailCustomer.avatar} alt="" className="size-full object-cover" />
                    ) : (
                      detailCustomer.name.slice(0, 1)
                    )}
                  </div>
                  <div className="min-w-0">
                    <div className="font-medium">{detailCustomer.name}</div>
                    <div className="text-sm text-muted-foreground">
                      {detailCustomer.primaryMobile || "无手机号"} · {detailCustomer.primaryEmail || "无邮箱"}
                    </div>
                  </div>
                </div>
                {detailCustomer.remark ? <div className="mt-3 text-sm text-slate-600">{detailCustomer.remark}</div> : null}
              </div>
              <div>
                <div className="mb-2 text-sm font-medium">门店关系</div>
                <div className="space-y-2">
                  {(detailCustomer.storeRelations ?? []).length === 0 ? (
                    <div className="rounded-xl border border-dashed p-4 text-sm text-muted-foreground">暂无门店关系</div>
                  ) : (
                    detailCustomer.storeRelations?.map((relation) => (
                      <div key={relation.id} className="flex items-center justify-between gap-3 rounded-xl border bg-white p-3 shadow-sm">
                        <div className="min-w-0">
                          <div className="font-medium">{relation.storeName || `门店 ${relation.storeId}`}</div>
                          <div className="mt-1 text-xs text-muted-foreground">
                            员工号：{relation.wxWorkInstanceName || relation.wxWorkInstanceId || "-"} · 到访 {relation.visitCount} 次 · 最近 {relation.lastActiveAt || "-"}
                          </div>
                          {relation.stableNotes ? <div className="mt-2 text-sm text-slate-600">{relation.stableNotes}</div> : null}
                        </div>
                        {relation.lastConversationId > 0 ? (
                          <Button variant="outline" size="sm" className="rounded-xl">
                            <a href={`/dashboard/conversations?conversationId=${relation.lastConversationId}`} className="inline-flex items-center gap-1.5">
                              <MessageCircleIcon className="size-4" />
                              会话
                            </a>
                          </Button>
                        ) : null}
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
