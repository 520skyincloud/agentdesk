"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import {
  CheckCircle2Icon,
  ChevronDownIcon,
  RefreshCwIcon,
  SaveIcon,
  SearchIcon,
  Settings2Icon,
} from "lucide-react"
import { type Resolver, useForm } from "react-hook-form"
import { toast } from "sonner"
import { z } from "zod/v4"

import {
  DashboardTableShell,
  DashboardTableStateRow,
  DashboardToolbar,
} from "@/components/dashboard-page"
import { ListPagination } from "@/components/list-pagination"
import { OptionCombobox } from "@/components/option-combobox"
import { ProjectDialog } from "@/components/project-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Field, FieldContent, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  batchToggleCustomerTagRuntime,
  createAdminWebSocketUrl,
  fetchCustomerTagPolicy,
  fetchStoreCustomerTagRuntimePolicies,
  updateCustomerTagPolicy,
  type CustomerTagPolicy,
  type PageResult,
  type StoreCustomerTagRuntimePolicy,
} from "@/lib/api/admin"
import { useI18n } from "@/i18n/provider"

type RuntimePolicyProps = {
  canUpdate: boolean
}

type PolicyForm = {
  quietPeriodHours: number
  minimumConfidencePercent: number
  maxOperationsPerRun: number
  evolutionDefaultEnabled: boolean
  replyTagContextDefaultEnabled: boolean
}

type RuntimeFeature = "evolution" | "reply"

type PendingAllAction = {
  feature: RuntimeFeature
  enabled: boolean
}

const emptyStoreResult: PageResult<StoreCustomerTagRuntimePolicy> = {
  results: [],
  page: { page: 1, limit: 20, total: 0 },
}

export function CustomerTagRuntimePolicyPanel({ canUpdate }: RuntimePolicyProps) {
  const t = useI18n()
  const policySchema = useMemo(
    () => z.object({
      quietPeriodHours: z.number().min(1, t("tag.policyQuietMin")).max(720, t("tag.policyQuietMax")),
      minimumConfidencePercent: z.number().min(80, t("tag.policyConfidenceMin")).max(100, t("tag.policyConfidenceMax")),
      maxOperationsPerRun: z.number().int().min(1, t("tag.policyOperationsMin")).max(6, t("tag.policyOperationsMax")),
      evolutionDefaultEnabled: z.boolean(),
      replyTagContextDefaultEnabled: z.boolean(),
    }),
    [t],
  )
  const form = useForm<PolicyForm>({
    resolver: zodResolver(policySchema as never) as Resolver<PolicyForm>,
    defaultValues: {
      quietPeriodHours: 24,
      minimumConfidencePercent: 80,
      maxOperationsPerRun: 6,
      evolutionDefaultEnabled: false,
      replyTagContextDefaultEnabled: false,
    },
  })
  const [policy, setPolicy] = useState<CustomerTagPolicy | null>(null)
  const [policyLoading, setPolicyLoading] = useState(true)
  const [policySaving, setPolicySaving] = useState(false)
  const [stores, setStores] = useState(emptyStoreResult)
  const [storesLoading, setStoresLoading] = useState(true)
  const [actionLoading, setActionLoading] = useState(false)
  const [keywordInput, setKeywordInput] = useState("")
  const [keyword, setKeyword] = useState("")
  const [storeStatusInput, setStoreStatusInput] = useState("all")
  const [storeStatus, setStoreStatus] = useState("all")
  const [evolutionFilterInput, setEvolutionFilterInput] = useState("all")
  const [evolutionFilter, setEvolutionFilter] = useState("all")
  const [replyFilterInput, setReplyFilterInput] = useState("all")
  const [replyFilter, setReplyFilter] = useState("all")
  const [page, setPage] = useState(1)
  const [limit, setLimit] = useState(20)
  const [selectedIds, setSelectedIds] = useState<Set<number>>(new Set())
  const [pendingAllAction, setPendingAllAction] = useState<PendingAllAction | null>(null)

  const statusOptions = useMemo(() => [
    { value: "all", label: t("tag.runtimeAllStatuses") },
    { value: "0", label: t("tag.runtimeActiveStores") },
    { value: "1", label: t("tag.runtimeDisabledStores") },
  ], [t])
  const featureOptions = useMemo(() => [
    { value: "all", label: t("tag.runtimeAllSwitches") },
    { value: "true", label: t("tag.runtimeEnabled") },
    { value: "false", label: t("tag.runtimeDisabled") },
  ], [t])

  const loadPolicy = useCallback(async () => {
    setPolicyLoading(true)
    try {
      const data = await fetchCustomerTagPolicy()
      setPolicy(data)
      form.reset({
        quietPeriodHours: data.quietPeriodMinutes / 60,
        minimumConfidencePercent: data.minimumConfidence * 100,
        maxOperationsPerRun: data.maxOperationsPerRun,
        evolutionDefaultEnabled: data.evolutionDefaultEnabled,
        replyTagContextDefaultEnabled: data.replyTagContextDefaultEnabled,
      })
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("tag.policyLoadFailed"))
    } finally {
      setPolicyLoading(false)
    }
  }, [form, t])

  const loadStores = useCallback(async () => {
    setStoresLoading(true)
    try {
      const data = await fetchStoreCustomerTagRuntimePolicies({
        page,
        limit,
        keyword: keyword || undefined,
        storeStatus: storeStatus === "all" ? undefined : storeStatus,
        evolutionEnabled: evolutionFilter === "all" ? undefined : evolutionFilter,
        replyEnabled: replyFilter === "all" ? undefined : replyFilter,
      })
      setStores(data)
      setSelectedIds(new Set())
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("tag.runtimeLoadFailed"))
    } finally {
      setStoresLoading(false)
    }
  }, [evolutionFilter, keyword, limit, page, replyFilter, storeStatus, t])

  useEffect(() => {
    void loadPolicy()
  }, [loadPolicy])

  useEffect(() => {
    void loadStores()
  }, [loadStores])

  useEffect(() => {
    let socket: WebSocket | null = null
    try {
      socket = new WebSocket(createAdminWebSocketUrl())
      socket.onmessage = (event) => {
        try {
          const payload = JSON.parse(event.data) as { type?: string }
          if (payload.type === "customer_tag_runtime_policy.changed") {
            void loadPolicy()
            void loadStores()
          }
        } catch {
          // Keep the current snapshot when an unrelated realtime frame is malformed.
        }
      }
    } catch {
      return
    }
    return () => socket?.close()
  }, [loadPolicy, loadStores])

  const currentPageIDs = useMemo(() => stores.results.map((item) => item.storeId), [stores.results])
  const selectedCount = selectedIds.size
  const allPageSelected = currentPageIDs.length > 0 && currentPageIDs.every((id) => selectedIds.has(id))
  const somePageSelected = currentPageIDs.some((id) => selectedIds.has(id))

  function applyFilters() {
    setPage(1)
    setKeyword(keywordInput.trim())
    setStoreStatus(storeStatusInput)
    setEvolutionFilter(evolutionFilterInput)
    setReplyFilter(replyFilterInput)
  }

  function togglePageSelection(checked: boolean) {
    setSelectedIds(checked ? new Set(currentPageIDs) : new Set())
  }

  function toggleSelection(storeId: number, checked: boolean) {
    setSelectedIds((current) => {
      const next = new Set(current)
      if (checked) next.add(storeId)
      else next.delete(storeId)
      return next
    })
  }

  async function savePolicy(values: PolicyForm) {
    if (!canUpdate || policySaving) return
    setPolicySaving(true)
    try {
      await updateCustomerTagPolicy({
        quietPeriodMinutes: Math.round(values.quietPeriodHours * 60),
        minimumConfidence: values.minimumConfidencePercent / 100,
        maxOperationsPerRun: values.maxOperationsPerRun,
        evolutionDefaultEnabled: values.evolutionDefaultEnabled,
        replyTagContextDefaultEnabled: values.replyTagContextDefaultEnabled,
      })
      toast.success(t("tag.policySaved"))
      await loadPolicy()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("tag.policySaveFailed"))
    } finally {
      setPolicySaving(false)
    }
  }

  async function runToggle(feature: RuntimeFeature, enabled: boolean, allStores: boolean, storeIDs: number[]) {
    if (!canUpdate || actionLoading) return
    setActionLoading(true)
    try {
      const result = await batchToggleCustomerTagRuntime({
        allStores,
        storeIds: allStores ? undefined : storeIDs,
        customerTagEvolutionEnabled: feature === "evolution" ? enabled : undefined,
        replyTagContextEnabled: feature === "reply" ? enabled : undefined,
      })
      toast.success(t("tag.runtimeUpdated", { count: result.affectedStoreCount }))
      setSelectedIds(new Set())
      await loadStores()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("tag.runtimeUpdateFailed"))
    } finally {
      setActionLoading(false)
    }
  }

  function selectedToggle(feature: RuntimeFeature, enabled: boolean) {
    void runToggle(feature, enabled, false, Array.from(selectedIds))
  }

  const evolutionDefault = form.watch("evolutionDefaultEnabled")
  const replyDefault = form.watch("replyTagContextDefaultEnabled")

  return (
    <div className="space-y-5">
      <section className="border-y bg-muted/20 px-4 py-5 sm:px-5">
        <div className="flex flex-col gap-3 border-b pb-4 sm:flex-row sm:items-start sm:justify-between">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-base font-semibold">{t("tag.policyTitle")}</h2>
              {policy ? <Badge variant="outline">{policy.industryName}</Badge> : null}
            </div>
            {policy ? <p className="mt-1 text-xs text-muted-foreground">{policy.industryCode}</p> : null}
          </div>
          <Button variant="outline" size="sm" onClick={() => void loadPolicy()} disabled={policyLoading || policySaving}>
            <RefreshCwIcon className={policyLoading ? "animate-spin" : ""} />
            {t("tag.refresh")}
          </Button>
        </div>

        <form className="mt-4 space-y-4" onSubmit={form.handleSubmit(savePolicy)}>
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-5">
            <Field data-invalid={!!form.formState.errors.quietPeriodHours}>
              <FieldLabel htmlFor="tag-policy-quiet-hours">{t("tag.policyQuietHours")}</FieldLabel>
              <FieldContent>
                <Input id="tag-policy-quiet-hours" type="number" min={1} max={720} step={1} disabled={policyLoading || !canUpdate} {...form.register("quietPeriodHours", { valueAsNumber: true })} />
                <FieldError errors={[form.formState.errors.quietPeriodHours]} />
              </FieldContent>
            </Field>
            <Field data-invalid={!!form.formState.errors.minimumConfidencePercent}>
              <FieldLabel htmlFor="tag-policy-confidence">{t("tag.policyConfidence")}</FieldLabel>
              <FieldContent>
                <Input id="tag-policy-confidence" type="number" min={80} max={100} step={1} disabled={policyLoading || !canUpdate} {...form.register("minimumConfidencePercent", { valueAsNumber: true })} />
                <FieldError errors={[form.formState.errors.minimumConfidencePercent]} />
              </FieldContent>
            </Field>
            <Field data-invalid={!!form.formState.errors.maxOperationsPerRun}>
              <FieldLabel htmlFor="tag-policy-operations">{t("tag.policyOperations")}</FieldLabel>
              <FieldContent>
                <Input id="tag-policy-operations" type="number" min={1} max={6} step={1} disabled={policyLoading || !canUpdate} {...form.register("maxOperationsPerRun", { valueAsNumber: true })} />
                <FieldError errors={[form.formState.errors.maxOperationsPerRun]} />
              </FieldContent>
            </Field>
            <Field>
              <FieldLabel>{t("tag.policyEvolutionDefault")}</FieldLabel>
              <FieldContent>
                <div className="flex h-9 items-center justify-between gap-3 border px-3">
                  <span className="text-sm">{evolutionDefault ? t("tag.runtimeEnabled") : t("tag.runtimeDisabled")}</span>
                  <Switch checked={evolutionDefault} disabled={policyLoading || !canUpdate} onCheckedChange={(checked) => form.setValue("evolutionDefaultEnabled", checked, { shouldDirty: true })} />
                </div>
              </FieldContent>
            </Field>
            <Field>
              <FieldLabel>{t("tag.policyReplyDefault")}</FieldLabel>
              <FieldContent>
                <div className="flex h-9 items-center justify-between gap-3 border px-3">
                  <span className="text-sm">{replyDefault ? t("tag.runtimeEnabled") : t("tag.runtimeDisabled")}</span>
                  <Switch checked={replyDefault} disabled={policyLoading || !canUpdate} onCheckedChange={(checked) => form.setValue("replyTagContextDefaultEnabled", checked, { shouldDirty: true })} />
                </div>
              </FieldContent>
            </Field>
          </div>
          {canUpdate ? (
            <div className="flex justify-end">
              <Button type="submit" disabled={policyLoading || policySaving || !policy}>
                <SaveIcon />
                {policySaving ? t("tag.saving") : t("tag.policySave")}
              </Button>
            </div>
          ) : null}
        </form>
      </section>

      <section className="space-y-3">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
          <div>
            <h2 className="text-base font-semibold">{t("tag.runtimeTitle")}</h2>
            <p className="mt-1 text-xs text-muted-foreground">{t("tag.runtimeStoreCount", { count: stores.page.total })}</p>
          </div>
          {canUpdate ? (
            <div className="flex flex-wrap gap-2">
              <DropdownMenu>
                <DropdownMenuTrigger render={<Button variant="outline" size="sm" disabled={selectedCount === 0 || actionLoading} />}>
                  <Settings2Icon />
                  {t("tag.runtimeSelected", { count: selectedCount })}
                  <ChevronDownIcon />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuGroup>
                    <DropdownMenuLabel>{t("tag.runtimeEvolution")}</DropdownMenuLabel>
                    <DropdownMenuItem onClick={() => selectedToggle("evolution", true)}>{t("tag.runtimeEnableSelected")}</DropdownMenuItem>
                    <DropdownMenuItem onClick={() => selectedToggle("evolution", false)}>{t("tag.runtimeDisableSelected")}</DropdownMenuItem>
                  </DropdownMenuGroup>
                  <DropdownMenuSeparator />
                  <DropdownMenuGroup>
                    <DropdownMenuLabel>{t("tag.runtimeReply")}</DropdownMenuLabel>
                    <DropdownMenuItem onClick={() => selectedToggle("reply", true)}>{t("tag.runtimeEnableSelected")}</DropdownMenuItem>
                    <DropdownMenuItem onClick={() => selectedToggle("reply", false)}>{t("tag.runtimeDisableSelected")}</DropdownMenuItem>
                  </DropdownMenuGroup>
                </DropdownMenuContent>
              </DropdownMenu>
              <DropdownMenu>
                <DropdownMenuTrigger render={<Button variant="outline" size="sm" disabled={actionLoading || !policy} />}>
                  <CheckCircle2Icon />
                  {t("tag.runtimeAllStores")}
                  <ChevronDownIcon />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuGroup>
                    <DropdownMenuLabel>{t("tag.runtimeEvolution")}</DropdownMenuLabel>
                    <DropdownMenuItem onClick={() => setPendingAllAction({ feature: "evolution", enabled: true })}>{t("tag.runtimeEnableAll")}</DropdownMenuItem>
                    <DropdownMenuItem onClick={() => setPendingAllAction({ feature: "evolution", enabled: false })}>{t("tag.runtimeDisableAll")}</DropdownMenuItem>
                  </DropdownMenuGroup>
                  <DropdownMenuSeparator />
                  <DropdownMenuGroup>
                    <DropdownMenuLabel>{t("tag.runtimeReply")}</DropdownMenuLabel>
                    <DropdownMenuItem onClick={() => setPendingAllAction({ feature: "reply", enabled: true })}>{t("tag.runtimeEnableAll")}</DropdownMenuItem>
                    <DropdownMenuItem onClick={() => setPendingAllAction({ feature: "reply", enabled: false })}>{t("tag.runtimeDisableAll")}</DropdownMenuItem>
                  </DropdownMenuGroup>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          ) : null}
        </div>

        <DashboardToolbar>
          <div className="relative w-full sm:w-64">
            <SearchIcon className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input value={keywordInput} onChange={(event) => setKeywordInput(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") { event.preventDefault(); applyFilters() } }} placeholder={t("tag.runtimeSearchStore")} className="pl-9" />
          </div>
          <div className="w-full sm:w-40"><OptionCombobox value={storeStatusInput} options={statusOptions} placeholder={t("tag.runtimeAllStatuses")} onChange={(value) => setStoreStatusInput(value ?? "all")} /></div>
          <div className="w-full sm:w-44"><OptionCombobox value={evolutionFilterInput} options={featureOptions} placeholder={t("tag.runtimeEvolution")} onChange={(value) => setEvolutionFilterInput(value ?? "all")} /></div>
          <div className="w-full sm:w-44"><OptionCombobox value={replyFilterInput} options={featureOptions} placeholder={t("tag.runtimeReply")} onChange={(value) => setReplyFilterInput(value ?? "all")} /></div>
          <Button variant="outline" onClick={applyFilters} disabled={storesLoading}><SearchIcon />{t("tag.query")}</Button>
          <Button variant="outline" size="icon" title={t("tag.refresh")} onClick={() => void loadStores()} disabled={storesLoading}><RefreshCwIcon className={storesLoading ? "animate-spin" : ""} /></Button>
        </DashboardToolbar>

        <DashboardTableShell>
          <Table>
            <TableHeader className="bg-muted/40">
              <TableRow>
                {canUpdate ? <TableHead className="w-12"><Checkbox aria-label={t("tag.runtimeSelectPage")} aria-checked={somePageSelected && !allPageSelected ? "mixed" : allPageSelected} checked={allPageSelected} onCheckedChange={(checked) => togglePageSelection(checked === true)} /></TableHead> : null}
                <TableHead className="min-w-[240px]">{t("tag.runtimeStore")}</TableHead>
                <TableHead className="w-[130px]">{t("tag.runtimeStoreStatus")}</TableHead>
                <TableHead className="w-[180px]">{t("tag.runtimeEvolution")}</TableHead>
                <TableHead className="w-[180px]">{t("tag.runtimeReply")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {stores.results.map((item) => (
                <TableRow key={item.storeId}>
                  {canUpdate ? <TableCell><Checkbox aria-label={t("tag.runtimeSelectStore", { name: item.storeName })} checked={selectedIds.has(item.storeId)} onCheckedChange={(checked) => toggleSelection(item.storeId, checked === true)} /></TableCell> : null}
                  <TableCell>
                    <div className="min-w-0">
                      <div className="truncate font-medium">{item.storeName}</div>
                      <div className="mt-0.5 truncate font-mono text-xs text-muted-foreground">{item.storeCode}</div>
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1.5">
                      <Badge variant={item.storeStatus === 0 ? "default" : "outline"}>{item.storeStatus === 0 ? t("tag.runtimeActive") : t("tag.runtimeInactive")}</Badge>
                      {!item.policyReady ? <Badge variant="destructive">{t("tag.runtimeNotReady")}</Badge> : null}
                    </div>
                  </TableCell>
                  <TableCell><RuntimeSwitch checked={item.customerTagEvolutionEnabled} disabled={!canUpdate || actionLoading} label={t("tag.runtimeEvolutionFor", { name: item.storeName })} stateLabel={t(item.customerTagEvolutionEnabled ? "tag.runtimeEnabled" : "tag.runtimeDisabled")} onChange={(checked) => void runToggle("evolution", checked, false, [item.storeId])} /></TableCell>
                  <TableCell><RuntimeSwitch checked={item.replyTagContextEnabled} disabled={!canUpdate || actionLoading} label={t("tag.runtimeReplyFor", { name: item.storeName })} stateLabel={t(item.replyTagContextEnabled ? "tag.runtimeEnabled" : "tag.runtimeDisabled")} onChange={(checked) => void runToggle("reply", checked, false, [item.storeId])} /></TableCell>
                </TableRow>
              ))}
              {storesLoading || stores.results.length === 0 ? <DashboardTableStateRow colSpan={4 + (canUpdate ? 1 : 0)} loading={storesLoading} loadingText={t("tag.runtimeLoading")} emptyText={t("tag.runtimeEmpty")} /> : null}
            </TableBody>
          </Table>
          <div className="border-t p-3">
            <ListPagination page={stores.page.page} total={stores.page.total} limit={stores.page.limit} loading={storesLoading} onPageChange={setPage} onLimitChange={(value) => { setLimit(value); setPage(1) }} />
          </div>
        </DashboardTableShell>
      </section>

      <ProjectDialog
        open={pendingAllAction !== null}
        onOpenChange={(open) => { if (!open && !actionLoading) setPendingAllAction(null) }}
        title={t("tag.runtimeConfirmAllTitle")}
        description={pendingAllAction ? t("tag.runtimeConfirmAllDescription", {
          feature: t(pendingAllAction.feature === "evolution" ? "tag.runtimeEvolution" : "tag.runtimeReply"),
          action: t(pendingAllAction.enabled ? "tag.runtimeEnable" : "tag.runtimeDisable"),
        }) : undefined}
        size="sm"
        footer={
          <>
            <Button variant="outline" onClick={() => setPendingAllAction(null)} disabled={actionLoading}>{t("tag.cancel")}</Button>
            <Button onClick={() => { if (!pendingAllAction) return; const action = pendingAllAction; setPendingAllAction(null); void runToggle(action.feature, action.enabled, true, []) }} disabled={actionLoading}>{t("tag.runtimeConfirm")}</Button>
          </>
        }
      >
        <p className="text-sm text-muted-foreground">{t("tag.runtimeConfirmAllScope", { count: stores.page.total })}</p>
      </ProjectDialog>
    </div>
  )
}

function RuntimeSwitch({ checked, disabled, label, stateLabel, onChange }: { checked: boolean; disabled: boolean; label: string; stateLabel: string; onChange: (checked: boolean) => void }) {
  return (
    <div className="flex min-w-[132px] items-center gap-2">
      <Switch checked={checked} disabled={disabled} aria-label={label} onCheckedChange={onChange} />
      <span className="text-sm">{stateLabel}</span>
    </div>
  )
}
