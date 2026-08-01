"use client"

import {
  Building2Icon,
  LocateFixedIcon,
  MapPinIcon,
  NetworkIcon,
  RotateCwIcon,
  UsersIcon,
} from "lucide-react"
import { useMemo, useState } from "react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import {
  createDashboardStatusColumn,
  DashboardCrudPage,
} from "@/components/dashboard/crud"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { useI18n } from "@/i18n/provider"
import { getBrowserCoordinates } from "@/lib/browser-geolocation"
import { Status } from "@/lib/generated/enums"
import {
  createStore,
  fetchStore,
  fetchStores,
  updateStore,
  updateStoreStatus,
  type Store,
  type StorePayload,
} from "@/lib/api/store"
import { formatDateTime } from "@/lib/utils"

export default function StoresPage() {
  const t = useI18n()
  const { session } = useAuth()
  const permissions = useMemo(
    () => new Set(session?.permissions ?? []),
    [session?.permissions]
  )
  const canCreate = permissions.has("store.create")
  const canUpdate = permissions.has("store.update")
  const [locating, setLocating] = useState(false)

  return (
    <DashboardCrudPage<Store, StorePayload>
      filters={[
        {
          name: "keyword",
          label: t("store.search"),
          placeholder: t("store.searchPlaceholder"),
          defaultValue: "",
          trim: true,
          className: "w-full sm:w-72",
        },
        {
          name: "status",
          label: t("store.status"),
          type: "select",
          defaultValue: "all",
          allValue: "all",
          valueType: "number",
          options: [
            { value: "all", label: t("status.all") },
            { value: String(Status.Ok), label: t("status.ok") },
            { value: String(Status.Disabled), label: t("status.disabled") },
          ],
          className: "w-full sm:w-36",
        },
      ]}
      columns={[
        {
          key: "store",
          label: t("store.columnStore"),
          render: (item) => (
            <div className="flex min-w-56 items-start gap-3">
              <div className="agentdesk-icon-tile mt-0.5 size-8 rounded-lg">
                <Building2Icon className="size-4" />
              </div>
              <div className="min-w-0">
                <div className="truncate font-medium">{item.name}</div>
                <div className="truncate text-xs text-muted-foreground">
                  {item.storeCode}
                </div>
                {item.brandName ? (
                  <Badge variant="outline" className="mt-1">
                    {item.brandName}
                  </Badge>
                ) : null}
              </div>
            </div>
          ),
        },
        {
          key: "address",
          label: t("store.columnAddress"),
          render: (item) => (
            <div className="flex max-w-80 items-start gap-2 text-sm">
              <MapPinIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
              <span className="line-clamp-2">
                {item.address || item.navigationName || "-"}
              </span>
            </div>
          ),
        },
        {
          key: "staff",
          label: t("store.columnStaff"),
          render: (item) => (
            <div className="flex items-center gap-2 text-sm">
              <UsersIcon className="size-4 text-muted-foreground" />
              {item.activeStaffCount}
            </div>
          ),
        },
        {
          key: "instances",
          label: t("store.columnInstances"),
          render: (item) => (
            <div className="flex items-center gap-2 text-sm">
              <NetworkIcon className="size-4 text-muted-foreground" />
              {item.currentInstanceCount}
            </div>
          ),
        },
        createDashboardStatusColumn<Store, Status>({
          label: t("store.status"),
          getStatus: (item) => item.status as Status,
          getLabel: (status) =>
            status === Status.Ok ? t("status.ok") : t("status.disabled"),
          getBadgeVariant: (status) =>
            status === Status.Ok ? "secondary" : "outline",
          isEnabled: (status) => status === Status.Ok,
          toggle: canUpdate
            ? {
                getNextStatus: (item) =>
                  item.status === Status.Ok ? Status.Disabled : Status.Ok,
                updateStatus: (item, status) => updateStoreStatus(item.id, status),
                successMessage: (item, status) =>
                  t(status === Status.Ok ? "store.enabled" : "store.disabled", {
                    name: item.name,
                  }),
                errorMessage: t("store.statusUpdateFailed"),
                ariaLabel: (item) => t("store.toggleStatus", { name: item.name }),
              }
            : undefined,
        }),
        {
          key: "updatedAt",
          label: t("store.columnUpdatedAt"),
          render: (item) => formatDateTime(item.updatedAt),
        },
      ]}
      fetchList={fetchStores}
      getItemId={(item) => item.id}
      createItem={createStore}
      updateItem={(item, payload) => updateStore(item.id, payload)}
      showCreate={canCreate}
      showEdit={canUpdate}
      showActionsColumn={canUpdate}
      form={{
        fetchDetail: fetchStore,
        fields: [
          {
            name: "name",
            label: t("store.name"),
            placeholder: t("store.namePlaceholder"),
            required: true,
            requiredMessage: t("store.nameRequired"),
            trim: true,
          },
          {
            name: "brandName",
            label: t("store.brandName"),
            placeholder: t("store.brandNamePlaceholder"),
            trim: true,
          },
          {
            name: "address",
            label: t("store.address"),
            placeholder: t("store.addressPlaceholder"),
            trim: true,
            colSpan: 2,
          },
          {
            name: "navigationName",
            label: t("store.navigationName"),
            placeholder: t("store.navigationNamePlaceholder"),
            trim: true,
            colSpan: 2,
          },
          {
            name: "longitude",
            label: t("store.longitude"),
            placeholder: "117.000000",
            trim: true,
          },
          {
            name: "latitude",
            label: t("store.latitude"),
            placeholder: "31.000000",
            trim: true,
          },
          {
            name: "browserCoordinates",
            label: t("store.coordinates"),
            type: "custom",
            colSpan: 2,
            render: ({ setValue }) => (
              <Button
                type="button"
                variant="outline"
                disabled={locating}
                onClick={async () => {
                  setLocating(true)
                  try {
                    const coordinates = await getBrowserCoordinates()
                    setValue("latitude", coordinates.latitude.toFixed(6))
                    setValue("longitude", coordinates.longitude.toFixed(6))
                    setValue("mapProvider", "browser_geolocation")
                    toast.success(
                      t("store.coordinatesLocated", {
                        accuracy: Math.round(coordinates.accuracy),
                      })
                    )
                  } catch (error) {
                    toast.error(
                      error instanceof Error
                        ? error.message
                        : t("store.coordinatesFailed")
                    )
                  } finally {
                    setLocating(false)
                  }
                }}
              >
                {locating ? (
                  <RotateCwIcon className="animate-spin" />
                ) : (
                  <LocateFixedIcon />
                )}
                {t(locating ? "store.coordinatesLocating" : "store.coordinatesLocate")}
              </Button>
            ),
          },
          {
            name: "mapProvider",
            label: t("store.mapProvider"),
            type: "select",
            options: [
              { value: "", label: t("store.mapProviderUnset") },
              {
                value: "browser_geolocation",
                label: t("store.mapProviderBrowser"),
              },
              { value: "tencent", label: t("store.mapProviderTencent") },
              { value: "baidu", label: t("store.mapProviderBaidu") },
              { value: "amap", label: t("store.mapProviderAmap") },
            ],
          },
          {
            name: "contactPhone",
            label: t("store.contactPhone"),
            placeholder: t("store.contactPhonePlaceholder"),
            trim: true,
          },
          {
            name: "remark",
            label: t("store.remark"),
            placeholder: t("store.remarkPlaceholder"),
            type: "textarea",
            rows: 4,
            trim: true,
            colSpan: 2,
          },
        ],
        transformSubmitValues: (values) => ({
          name: String(values.name ?? ""),
          brandName: String(values.brandName ?? ""),
          address: String(values.address ?? ""),
          navigationName: String(values.navigationName ?? ""),
          longitude: String(values.longitude ?? ""),
          latitude: String(values.latitude ?? ""),
          mapProvider: String(values.mapProvider ?? ""),
          contactPhone: String(values.contactPhone ?? ""),
          remark: String(values.remark ?? ""),
        }),
        labels: {
          createTitle: t("store.createTitle"),
          editTitle: t("store.editTitle"),
          create: t("store.create"),
          save: t("store.save"),
          saving: t("store.saving"),
          cancel: t("common.cancel"),
          loadingDetail: t("store.loadingDetail"),
          required: t("store.nameRequired"),
          invalidNumber: t("store.invalidNumber"),
          minValue: () => t("store.invalidNumber"),
          maxValue: () => t("store.invalidNumber"),
        },
      }}
      labels={{
        refresh: t("common.refresh"),
        create: t("store.new"),
        query: t("common.query"),
        loading: t("common.loadingData"),
        empty: t("store.empty"),
        actions: t("common.actions"),
        edit: t("store.edit"),
        delete: t("store.delete"),
        processing: t("store.processing"),
        moreActions: (item) => t("store.moreActions", { name: item.name }),
        loadFailed: t("store.loadFailed"),
        saveFailed: t("store.saveFailed"),
        deleteFailed: t("store.deleteFailed"),
        created: (payload) => t("store.created", { name: payload.name }),
        updated: (item) => t("store.updated", { name: item.name }),
      }}
    />
  )
}
