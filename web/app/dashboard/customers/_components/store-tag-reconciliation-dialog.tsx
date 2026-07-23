"use client"

import { useEffect, useMemo, useState } from "react"
import { ArrowRightIcon, ShieldCheckIcon } from "lucide-react"

import { CustomerTagBadges } from "@/components/customer-tag-badges"
import { OptionCombobox } from "@/components/option-combobox"
import { ProjectDialog } from "@/components/project-dialog"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { useI18n } from "@/i18n/provider"
import type {
  ReconcileStoreCustomerRelationTagsPayload,
  StoreCustomerRelation,
} from "@/lib/api/customer"
import { StoreCustomerTagReconcileStrategy } from "@/lib/generated/enums"
import { cn } from "@/lib/utils"

type Props = {
  open: boolean
  customerName: string
  relations: StoreCustomerRelation[]
  onOpenChange: (open: boolean) => void
  onSubmit: (
    payload: ReconcileStoreCustomerRelationTagsPayload,
  ) => Promise<boolean>
}

const strategies = [
  StoreCustomerTagReconcileStrategy.PreserveSource,
  StoreCustomerTagReconcileStrategy.PreserveTarget,
  StoreCustomerTagReconcileStrategy.ClearRebuild,
] as const

export function StoreTagReconciliationDialog({
  open,
  customerName,
  relations,
  onOpenChange,
  onSubmit,
}: Props) {
  const t = useI18n()
  const [sourceRelationId, setSourceRelationId] = useState("")
  const [targetRelationId, setTargetRelationId] = useState("")
  const [strategy, setStrategy] =
    useState<StoreCustomerTagReconcileStrategy>(
      StoreCustomerTagReconcileStrategy.PreserveTarget,
    )
  const [confirmed, setConfirmed] = useState(false)
  const [saving, setSaving] = useState(false)

  const relationOptions = useMemo(
    () =>
      relations.map((relation) => ({
        value: String(relation.id),
        label: `${relation.storeName || t("customer.storeFallback", { id: relation.storeId })} · ${t("customer.activeTagCount", { count: relation.customerTags?.length ?? 0 })}`,
      })),
    [relations, t],
  )
  const sourceRelation = relations.find(
    (relation) => String(relation.id) === sourceRelationId,
  )
  const targetRelation = relations.find(
    (relation) => String(relation.id) === targetRelationId,
  )
  const targetOptions = relationOptions.filter(
    (option) => option.value !== sourceRelationId,
  )

  useEffect(() => {
    if (!open) return
    const first = relations[0]
    const second = relations.find((relation) => relation.id !== first?.id)
    setSourceRelationId(first ? String(first.id) : "")
    setTargetRelationId(second ? String(second.id) : "")
    setStrategy(StoreCustomerTagReconcileStrategy.PreserveTarget)
    setConfirmed(false)
  }, [open, relations])

  function chooseSource(value: string) {
    setSourceRelationId(value)
    if (targetRelationId === value) {
      const next = relations.find((relation) => String(relation.id) !== value)
      setTargetRelationId(next ? String(next.id) : "")
    }
    setConfirmed(false)
  }

  function chooseTarget(value: string) {
    setTargetRelationId(value)
    setConfirmed(false)
  }

  function chooseStrategy(value: StoreCustomerTagReconcileStrategy) {
    setStrategy(value)
    setConfirmed(false)
  }

  async function submit() {
    const sourceID = Number(sourceRelationId)
    const targetID = Number(targetRelationId)
    if (
      !Number.isSafeInteger(sourceID) ||
      !Number.isSafeInteger(targetID) ||
      sourceID <= 0 ||
      targetID <= 0 ||
      sourceID === targetID ||
      !confirmed
    ) {
      return
    }
    setSaving(true)
    try {
      const completed = await onSubmit({
        sourceStoreRelationId: sourceID,
        targetStoreRelationId: targetID,
        strategy,
        confirmed: true,
      })
      if (completed) {
        onOpenChange(false)
      }
    } finally {
      setSaving(false)
    }
  }

  return (
    <ProjectDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t("customer.reconcileTitle")}
      description={t("customer.reconcileDescription", { name: customerName })}
      size="lg"
      footer={
        <>
          <Button
            type="button"
            variant="outline"
            disabled={saving}
            onClick={() => onOpenChange(false)}
          >
            {t("common.cancel")}
          </Button>
          <Button
            type="button"
            disabled={
              saving ||
              !confirmed ||
              !sourceRelation ||
              !targetRelation ||
              sourceRelation.id === targetRelation.id
            }
            onClick={() => void submit()}
          >
            <ShieldCheckIcon />
            {saving
              ? t("customer.reconcileSaving")
              : t("customer.reconcileConfirm")}
          </Button>
        </>
      }
    >
      <div className="grid gap-3 sm:grid-cols-[1fr_auto_1fr] sm:items-end">
        <label className="space-y-1.5 text-sm">
          <span className="font-medium">{t("customer.sourceStore")}</span>
          <OptionCombobox
            value={sourceRelationId}
            options={relationOptions}
            placeholder={t("customer.selectSourceStore")}
            onChange={chooseSource}
            disabled={saving}
          />
        </label>
        <ArrowRightIcon className="mx-auto mb-2 hidden size-4 text-muted-foreground sm:block" />
        <label className="space-y-1.5 text-sm">
          <span className="font-medium">{t("customer.targetStore")}</span>
          <OptionCombobox
            value={targetRelationId}
            options={targetOptions}
            placeholder={t("customer.selectTargetStore")}
            onChange={chooseTarget}
            disabled={saving || !sourceRelation}
          />
        </label>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <RelationPreview
          title={t("customer.sourcePreview")}
          relation={sourceRelation}
        />
        <RelationPreview
          title={t("customer.targetPreview")}
          relation={targetRelation}
        />
      </div>

      <div className="space-y-2">
        <div className="text-sm font-medium">
          {t("customer.reconcileStrategy")}
        </div>
        <div
          className="grid overflow-hidden rounded-md border sm:grid-cols-3"
          role="radiogroup"
          aria-label={t("customer.reconcileStrategy")}
        >
          {strategies.map((value) => (
            <button
              key={value}
              type="button"
              role="radio"
              aria-checked={strategy === value}
              className={cn(
                "min-h-20 border-b px-3 py-2 text-left text-sm transition-colors last:border-b-0 hover:bg-muted/60 sm:border-r sm:border-b-0 sm:last:border-r-0",
                strategy === value && "bg-primary/8 text-foreground",
              )}
              disabled={saving}
              onClick={() => chooseStrategy(value)}
            >
              <span className="block font-medium">
                {t(`customer.reconcileStrategyLabel.${value}`)}
              </span>
              <span className="mt-1 block text-xs leading-5 text-muted-foreground">
                {t(`customer.reconcileStrategyHelp.${value}`)}
              </span>
            </button>
          ))}
        </div>
      </div>

      {strategy === StoreCustomerTagReconcileStrategy.ClearRebuild ? (
        <Alert variant="destructive">
          <AlertTitle>{t("customer.clearRebuildWarningTitle")}</AlertTitle>
          <AlertDescription>
            {t("customer.clearRebuildWarningDescription")}
          </AlertDescription>
        </Alert>
      ) : null}

      <label className="flex items-start gap-2 rounded-md border bg-muted/30 p-3 text-sm">
        <Checkbox
          checked={confirmed}
          disabled={saving || !sourceRelation || !targetRelation}
          onCheckedChange={(value) => setConfirmed(value === true)}
          aria-label={t("customer.reconcileConfirmation")}
        />
        <span className="leading-5">
          {t("customer.reconcileConfirmation")}
        </span>
      </label>
    </ProjectDialog>
  )
}

function RelationPreview({
  title,
  relation,
}: {
  title: string
  relation?: StoreCustomerRelation
}) {
  const t = useI18n()
  return (
    <div className="min-w-0 rounded-md border p-3">
      <div className="text-xs font-medium text-muted-foreground">{title}</div>
      <div className="mt-1 truncate text-sm font-medium">
        {relation?.storeName ||
          (relation
            ? t("customer.storeFallback", { id: relation.storeId })
            : t("customer.storeNotSelected"))}
      </div>
      <div className="mt-2 min-h-6">
        {relation?.customerTags?.length ? (
          <CustomerTagBadges tags={relation.customerTags} />
        ) : (
          <span className="text-xs text-muted-foreground">
            {t("customer.noActiveTags")}
          </span>
        )}
      </div>
    </div>
  )
}
