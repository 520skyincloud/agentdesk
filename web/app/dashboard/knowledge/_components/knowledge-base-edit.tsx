"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { useEffect, useMemo, useState } from "react";
import { Controller, type Resolver, useForm } from "react-hook-form";
import { z } from "zod/v4";

import { OptionCombobox } from "@/components/option-combobox";
import { ProjectDialog } from "@/components/project-dialog";
import { Button } from "@/components/ui/button";
import { Field, FieldContent, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { useI18n } from "@/i18n/provider";
import {
  fetchKnowledgeBase,
  type KnowledgeBase,
  type UpdateKnowledgeBasePayload,
} from "@/lib/api/admin";
import { KnowledgeAnswerMode } from "@/lib/generated/enums";

type KnowledgeBaseEditDialogProps = {
  open: boolean;
  saving: boolean;
  itemId: number | null;
  onOpenChange: (open: boolean) => void;
  onSubmit: (payload: UpdateKnowledgeBasePayload) => Promise<void>;
};

type EditForm = {
  name: string;
  description: string;
  defaultTopK: string;
  defaultScoreThreshold: string;
  defaultRerankLimit: string;
  answerMode: string;
  resourceAllowedHosts: string;
};

type TFunction = (key: string, values?: Record<string, string | number>) => string;

const emptyForm: EditForm = {
  name: "",
  description: "",
  defaultTopK: "5",
  defaultScoreThreshold: "0.2",
  defaultRerankLimit: "10",
  answerMode: String(KnowledgeAnswerMode.Strict),
  resourceAllowedHosts: "",
};

function createKnowledgeBaseFormSchema(t: TFunction) {
  return z.object({
    name: z.string().trim().min(1, t("knowledge.nameRequired")).max(100, t("knowledge.nameMax")),
    description: z.string().trim().max(500, t("knowledge.descriptionMax")),
    defaultTopK: z.string().trim().refine((value) => integerInRange(value, 1, 100), t("knowledge.topKRequired")),
    defaultScoreThreshold: z.string().trim().refine((value) => numberInRange(value, 0, 1, false), t("knowledge.scoreRequired")),
    defaultRerankLimit: z.string().trim().refine((value) => integerInRange(value, 0, 100), t("knowledge.rerankRequired")),
    answerMode: z.string().trim().min(1, t("knowledge.answerModeRequired")),
    resourceAllowedHosts: z.string().trim().max(1000, "图片可信域名不能超过 1000 个字符"),
  });
}

function integerInRange(value: string, min: number, max: number) {
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed >= min && parsed <= max;
}

function numberInRange(value: string, min: number, max: number, includeMin = true) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && (includeMin ? parsed >= min : parsed > min) && parsed <= max;
}

function getAnswerModeOptions(t: TFunction) {
  return [
    { value: String(KnowledgeAnswerMode.Strict), label: t("knowledge.answerStrict") },
    { value: String(KnowledgeAnswerMode.Assist), label: t("knowledge.answerAssist") },
  ];
}

function buildForm(item: KnowledgeBase | null): EditForm {
  if (!item) return emptyForm;
  return {
    name: item.name,
    description: item.description || "",
    defaultTopK: String(item.defaultTopK),
    defaultScoreThreshold: String(item.defaultScoreThreshold),
    defaultRerankLimit: String(item.defaultRerankLimit),
    answerMode: String(item.answerMode),
    resourceAllowedHosts: (item.resourceAllowedHosts || []).join("\n"),
  };
}

function buildPayload(form: EditForm): UpdateKnowledgeBasePayload {
  return {
    name: form.name.trim(),
    description: form.description.trim(),
    defaultTopK: Number(form.defaultTopK),
    defaultScoreThreshold: Number(form.defaultScoreThreshold),
    defaultRerankLimit: Number(form.defaultRerankLimit),
    answerMode: Number(form.answerMode),
    resourceAllowedHosts: normalizeResourceAllowedHosts(form.resourceAllowedHosts),
  };
}

function normalizeResourceAllowedHosts(values: string) {
  return values
    .split(/[\n,]/)
    .map((value) => value.trim().replace(/^https?:\/\//, "").replace(/\/$/, ""))
    .filter((value, index, all) => value.length > 0 && all.indexOf(value) === index);
}

export function EditDialog(props: KnowledgeBaseEditDialogProps) {
  if (!props.open) return null;
  return <KnowledgeBaseFormDialogBody key={`edit-${props.itemId ?? "none"}`} {...props} />;
}

function KnowledgeBaseFormDialogBody({
  open,
  saving,
  itemId,
  onOpenChange,
  onSubmit,
}: KnowledgeBaseEditDialogProps) {
  const t = useI18n();
  const formId = "knowledge-base-edit-form";
  const [loading, setLoading] = useState(false);
  const schema = useMemo(() => createKnowledgeBaseFormSchema(t), [t]);
  const resolver = useMemo(() => zodResolver(schema) as Resolver<EditForm>, [schema]);
  const answerModeOptions = useMemo(() => getAnswerModeOptions(t), [t]);
  const form = useForm<EditForm>({ resolver, defaultValues: emptyForm });
  const {
    control,
    handleSubmit,
    register,
    reset,
    formState: { errors },
  } = form;

  useEffect(() => {
    if (!open || itemId === null) {
      reset(emptyForm);
      return;
    }
    let cancelled = false;
    async function loadItem() {
      setLoading(true);
      try {
        const item = await fetchKnowledgeBase(itemId!);
        if (!cancelled) reset(buildForm(item));
      } finally {
        if (!cancelled) setLoading(false);
      }
    }
    void loadItem();
    return () => {
      cancelled = true;
    };
  }, [itemId, open, reset]);

  return (
    <ProjectDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t("knowledge.editBaseTitle")}
      size="lg"
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={saving || loading}>
            {t("knowledge.cancel")}
          </Button>
          <Button type="submit" form={formId} disabled={saving || loading || itemId === null}>
            {saving ? t("knowledge.saving") : t("knowledge.save")}
          </Button>
        </>
      }
    >
      {loading ? (
        <div className="flex items-center justify-center py-8 text-sm text-muted-foreground">{t("knowledge.loading")}</div>
      ) : (
        <form id={formId} onSubmit={handleSubmit((values) => onSubmit(buildPayload(values)))} className="space-y-4">
          <Field data-invalid={!!errors.name}>
            <FieldLabel htmlFor="kb-name">{t("knowledge.name")}</FieldLabel>
            <FieldContent>
              <Input id="kb-name" aria-invalid={!!errors.name} {...register("name")} />
              <FieldError errors={[errors.name]} />
            </FieldContent>
          </Field>

          <Field data-invalid={!!errors.description}>
            <FieldLabel htmlFor="kb-description">{t("knowledge.description")}</FieldLabel>
            <FieldContent>
              <Textarea id="kb-description" rows={3} aria-invalid={!!errors.description} {...register("description")} />
              <FieldError errors={[errors.description]} />
            </FieldContent>
          </Field>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Field data-invalid={!!errors.defaultTopK}>
              <FieldLabel htmlFor="kb-default-top-k">{t("knowledge.defaultTopK")}</FieldLabel>
              <FieldContent>
                <Input id="kb-default-top-k" type="number" min="1" max="100" {...register("defaultTopK")} />
                <FieldError errors={[errors.defaultTopK]} />
              </FieldContent>
            </Field>
            <Field data-invalid={!!errors.defaultScoreThreshold}>
              <FieldLabel htmlFor="kb-default-score-threshold">{t("knowledge.defaultScoreThreshold")}</FieldLabel>
              <FieldContent>
                <Input id="kb-default-score-threshold" type="number" min="0.01" max="1" step="0.01" {...register("defaultScoreThreshold")} />
                <FieldError errors={[errors.defaultScoreThreshold]} />
              </FieldContent>
            </Field>
            <Field data-invalid={!!errors.defaultRerankLimit}>
              <FieldLabel htmlFor="kb-default-rerank-limit">{t("knowledge.defaultRerankLimit")}</FieldLabel>
              <FieldContent>
                <Input id="kb-default-rerank-limit" type="number" min="0" max="100" {...register("defaultRerankLimit")} />
                <FieldError errors={[errors.defaultRerankLimit]} />
              </FieldContent>
            </Field>
            <Field data-invalid={!!errors.answerMode}>
              <FieldLabel htmlFor="kb-answer-mode">{t("knowledge.answerMode")}</FieldLabel>
              <FieldContent>
                <Controller
                  control={control}
                  name="answerMode"
                  render={({ field }) => (
                    <OptionCombobox
                      value={field.value}
                      options={answerModeOptions}
                      placeholder={t("knowledge.selectAnswerMode")}
                      searchPlaceholder={t("knowledge.searchAnswerMode")}
                      emptyText={t("knowledge.emptyAnswerMode")}
                      onChange={field.onChange}
                    />
                  )}
                />
                <FieldError errors={[errors.answerMode]} />
              </FieldContent>
            </Field>
          </div>

          <Field data-invalid={!!errors.resourceAllowedHosts}>
            <FieldLabel htmlFor="kb-resource-allowed-hosts">图片可信域名</FieldLabel>
            <FieldContent>
              <Textarea id="kb-resource-allowed-hosts" rows={3} placeholder="cdn.example.com" {...register("resourceAllowedHosts")} />
              <FieldError errors={[errors.resourceAllowedHosts]} />
            </FieldContent>
          </Field>
        </form>
      )}
    </ProjectDialog>
  );
}
