"use client"

import { useEffect, useMemo, useState } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import { type Resolver, useForm } from "react-hook-form"
import { toast } from "sonner"
import { z } from "zod/v4"

import { ProjectDialog } from "@/components/project-dialog"
import { Button } from "@/components/ui/button"
import { Field, FieldContent, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { fetchTag, type Tag } from "@/lib/api/admin"
import { useI18n } from "@/i18n/provider"

type EditDialogProps = {
  open: boolean
  saving: boolean
  itemId: number | null
  onOpenChange: (open: boolean) => void
  onSubmit: (displayAlias: string) => Promise<void>
}

type EditForm = {
  displayAlias: string
}

export function EditDialog(props: EditDialogProps) {
  if (!props.open || !props.itemId) {
    return null
  }
  return <EditDialogBody key={props.itemId} {...props} itemId={props.itemId} />
}

function EditDialogBody({
  open,
  saving,
  itemId,
  onOpenChange,
  onSubmit,
}: Omit<EditDialogProps, "itemId"> & { itemId: number }) {
  const t = useI18n()
  const formId = "tag-alias-edit-form"
  const [loading, setLoading] = useState(true)
  const [item, setItem] = useState<Tag | null>(null)
  const schema = useMemo(
    () => z.object({ displayAlias: z.string().trim().max(80, t("tag.aliasTooLong")) }),
    [t],
  )
  const form = useForm<EditForm>({
    resolver: zodResolver(schema as never) as Resolver<EditForm>,
    defaultValues: { displayAlias: "" },
  })

  useEffect(() => {
    let cancelled = false
    void fetchTag(itemId)
      .then((data) => {
        if (cancelled) return
        setItem(data)
        form.reset({ displayAlias: data.displayAlias ?? "" })
      })
      .catch((error) => {
        if (!cancelled) {
          setItem(null)
          toast.error(error instanceof Error ? error.message : t("tag.loadFailed"))
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [form, itemId, t])

  return (
    <ProjectDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t("tag.editAlias")}
      size="md"
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
            {t("tag.cancel")}
          </Button>
          <Button type="submit" form={formId} disabled={saving || loading || !item}>
            {saving ? t("tag.saving") : t("tag.save")}
          </Button>
        </>
      }
    >
      {loading ? (
        <div className="py-10 text-center text-sm text-muted-foreground">{t("tag.loadingDetail")}</div>
      ) : (
        <form
          id={formId}
          className="space-y-4"
          onSubmit={form.handleSubmit(async ({ displayAlias }) => onSubmit(displayAlias.trim()))}
        >
          <Field>
            <FieldLabel>{t("tag.standardName")}</FieldLabel>
            <FieldContent>
              <Input value={item?.name ?? ""} disabled />
            </FieldContent>
          </Field>
          <Field>
            <FieldLabel>{t("tag.semanticKey")}</FieldLabel>
            <FieldContent>
              <Input value={item?.semanticKey ?? ""} disabled className="font-mono text-xs" />
            </FieldContent>
          </Field>
          <Field data-invalid={!!form.formState.errors.displayAlias}>
            <FieldLabel htmlFor="tag-display-alias">{t("tag.displayAlias")}</FieldLabel>
            <FieldContent>
              <Input
                id="tag-display-alias"
                placeholder={t("tag.displayAliasPlaceholder")}
                aria-invalid={!!form.formState.errors.displayAlias}
                {...form.register("displayAlias")}
              />
              <FieldError errors={[form.formState.errors.displayAlias]} />
            </FieldContent>
          </Field>
        </form>
      )}
    </ProjectDialog>
  )
}
