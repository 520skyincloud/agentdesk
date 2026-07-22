"use client";

import { useEffect, useMemo, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { Controller, Resolver, useForm } from "react-hook-form";
import { z } from "zod/v4";

import { ProjectDialog } from "@/components/project-dialog";
import { TagSelector } from "@/components/tag-selector";
import { Button } from "@/components/ui/button";
import {
  Field,
  FieldContent,
  FieldError,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
  type CreateTagPayload,
  fetchTag,
  fetchTagsAll,
  type Tag,
  type TagTree,
} from "@/lib/api/admin";
import { useI18n } from "@/i18n/provider";

type TagFormDialogProps = {
  open: boolean;
  saving: boolean;
  itemId: number | null;
  onOpenChange: (open: boolean) => void;
  onSubmit: (payload: CreateTagPayload) => Promise<void>;
};

const emptyForm: EditForm = {
  companyId: 0,
  parentId: "0",
  name: "",
  semanticKey: "",
  aliases: "",
  conflictGroup: "",
  aiEnabled: true,
  replyEnabled: false,
  applicableScene: "",
  mergedIntoTagId: "0",
  remark: "",
};

type EditForm = {
  companyId: number;
  parentId: string;
  name: string;
  semanticKey: string;
  aliases: string;
  conflictGroup: string;
  aiEnabled: boolean;
  replyEnabled: boolean;
  applicableScene: string;
  mergedIntoTagId: string;
  remark: string;
};

function buildForm(item: Tag | null): EditForm {
  if (!item) {
    return emptyForm;
  }

  return {
    companyId: item.companyId,
    parentId: String(item.parentId),
    name: item.name,
    semanticKey: item.semanticKey,
    aliases: item.aliases,
    conflictGroup: item.conflictGroup,
    aiEnabled: item.aiEnabled,
    replyEnabled: item.replyEnabled,
    applicableScene: item.applicableScene,
    mergedIntoTagId: String(item.mergedIntoTagId || 0),
    remark: item.remark,
  };
}

function buildPayload(form: EditForm): CreateTagPayload {
  return {
    companyId: form.companyId,
    parentId: Number(form.parentId),
    name: form.name.trim(),
    semanticKey: form.semanticKey.trim(),
    aliases: form.aliases.trim(),
    conflictGroup: form.conflictGroup.trim(),
    aiEnabled: form.aiEnabled,
    replyEnabled: form.replyEnabled,
    applicableScene: form.applicableScene.trim(),
    mergedIntoTagId: Number(form.mergedIntoTagId),
    remark: form.remark.trim(),
    status: 0,
  };
}

export function EditDialog({
  open,
  saving,
  itemId,
  onOpenChange,
  onSubmit,
}: TagFormDialogProps) {
  if (!open) {
    return null;
  }

  return (
    <TagFormDialogBody
      key={itemId ? `edit-${itemId}` : "create"}
      open={open}
      itemId={itemId}
      saving={saving}
      onOpenChange={onOpenChange}
      onSubmit={onSubmit}
    />
  );
}

type TagFormDialogBodyProps = {
  open: boolean;
  saving: boolean;
  itemId: number | null;
  onOpenChange: (open: boolean) => void;
  onSubmit: (payload: CreateTagPayload) => Promise<void>;
};

function TagFormDialogBody({
  open,
  saving,
  itemId,
  onOpenChange,
  onSubmit,
}: TagFormDialogBodyProps) {
  const t = useI18n();
  const formId = "tag-edit-form";
  const [loading, setLoading] = useState(false);
  const [parentTags, setParentTags] = useState<TagTree[]>([]);

  const tagFormSchema = useMemo(
    () =>
      z.object({
        parentId: z.string(),
        name: z.string().trim().min(1, t("tag.nameRequired")).max(5, "标签名称最多 5 个字"),
        companyId: z.number(),
        semanticKey: z.string(),
        aliases: z.string(),
        conflictGroup: z.string(),
        aiEnabled: z.boolean(),
        replyEnabled: z.boolean(),
        applicableScene: z.string(),
        mergedIntoTagId: z.string(),
        remark: z.string(),
      }),
    [t],
  );
  const editFormResolver = useMemo(
    () => zodResolver(tagFormSchema as never) as Resolver<EditForm>,
    [tagFormSchema],
  );
  const form = useForm<EditForm>({
    resolver: editFormResolver,
    defaultValues: emptyForm,
  });
  const {
    control,
    handleSubmit,
    reset,
    register,
    formState: { errors },
  } = form;

  useEffect(() => {
    async function loadParentTags() {
      try {
        const data = await fetchTagsAll();
        setParentTags(Array.isArray(data) ? data : []);
      } catch (error) {
        console.error("Failed to load parent tags:", error);
      }
    }
    void loadParentTags();
  }, [itemId]);

  useEffect(() => {
    async function loadDetail() {
      if (!itemId) {
        reset(emptyForm);
        return;
      }
      setLoading(true);
      try {
        const data = await fetchTag(itemId);
        reset(buildForm(data));
      } catch (error) {
        console.error("Failed to load tag:", error);
      } finally {
        setLoading(false);
      }
    }
    void loadDetail();
  }, [itemId, reset]);

  async function onFormSubmit(values: EditForm) {
    const payload = buildPayload(values);
    await onSubmit(payload);
  }

  return (
    <ProjectDialog
      open={open}
      onOpenChange={onOpenChange}
      title={itemId ? t("tag.editTitle") : t("tag.createTitle")}
      size="md"
      allowFullscreen
      footer={
        <>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={saving}
          >
            {t("tag.cancel")}
          </Button>
          <Button type="submit" form={formId} disabled={saving || loading}>
            {saving ? t("tag.saving") : itemId ? t("tag.save") : t("tag.create")}
          </Button>
        </>
      }
    >
      {loading ? (
        <div className="flex items-center justify-center py-12">
          <div className="text-muted-foreground">{t("tag.loadingDetail")}</div>
        </div>
      ) : (
        <form
          id={formId}
          onSubmit={handleSubmit(onFormSubmit)}
          className="space-y-4"
        >
          <Field data-invalid={!!errors.parentId}>
            <FieldLabel htmlFor="tag-parent-id">{t("tag.parent")}</FieldLabel>
            <FieldContent>
              <Controller
                control={control}
                name="parentId"
                render={({ field }) => (
                  <TagSelector
                    mode="single"
                    value={Number(field.value)}
                    onChange={(value) => field.onChange(String(value))}
                    tags={parentTags}
                    placeholder={t("tag.rootParent")}
                    searchPlaceholder={t("tag.searchParent")}
                    emptyText={t("tag.emptyParent")}
                    disabled={saving}
                    rootOption={{ value: 0, label: t("tag.rootParent") }}
                    excludeIds={itemId ? [itemId] : undefined}
                  />
                )}
              />
              <FieldError errors={[errors.parentId]} />
            </FieldContent>
          </Field>
          <Field data-invalid={!!errors.name}>
            <FieldLabel htmlFor="tag-name">{t("tag.name")}</FieldLabel>
            <FieldContent>
              <Input
                id="tag-name"
                placeholder={t("tag.namePlaceholder")}
                maxLength={5}
                aria-invalid={!!errors.name}
                {...register("name")}
              />
              <FieldError errors={[errors.name]} />
            </FieldContent>
          </Field>
          <Field data-invalid={!!errors.semanticKey}>
            <FieldLabel htmlFor="tag-semantic-key">语义标识</FieldLabel>
            <FieldContent>
              <Input
                id="tag-semantic-key"
                placeholder="例如 room.quiet；留空自动生成"
                aria-invalid={!!errors.semanticKey}
                {...register("semanticKey")}
              />
              <FieldError errors={[errors.semanticKey]} />
            </FieldContent>
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field data-invalid={!!errors.aliases}>
              <FieldLabel htmlFor="tag-aliases">同义词</FieldLabel>
              <FieldContent>
                <Input
                  id="tag-aliases"
                  placeholder="逗号分隔"
                  aria-invalid={!!errors.aliases}
                  {...register("aliases")}
                />
                <FieldError errors={[errors.aliases]} />
              </FieldContent>
            </Field>
            <Field data-invalid={!!errors.conflictGroup}>
              <FieldLabel htmlFor="tag-conflict-group">互斥组</FieldLabel>
              <FieldContent>
                <Input
                  id="tag-conflict-group"
                  placeholder="同组标签可相互替换"
                  aria-invalid={!!errors.conflictGroup}
                  {...register("conflictGroup")}
                />
                <FieldError errors={[errors.conflictGroup]} />
              </FieldContent>
            </Field>
          </div>
          <Field data-invalid={!!errors.applicableScene}>
            <FieldLabel htmlFor="tag-applicable-scene">适用场景</FieldLabel>
            <FieldContent>
              <Input
                id="tag-applicable-scene"
                placeholder="例如 room_preference"
                aria-invalid={!!errors.applicableScene}
                {...register("applicableScene")}
              />
              <FieldError errors={[errors.applicableScene]} />
            </FieldContent>
          </Field>
          <Field data-invalid={!!errors.mergedIntoTagId}>
            <FieldLabel htmlFor="tag-merged-into">已合并到</FieldLabel>
            <FieldContent>
              <Controller
                control={control}
                name="mergedIntoTagId"
                render={({ field }) => (
                  <TagSelector
                    mode="single"
                    value={Number(field.value)}
                    onChange={(value) => field.onChange(String(value))}
                    tags={parentTags}
                    placeholder="未合并"
                    searchPlaceholder="搜索标准标签"
                    emptyText="未找到标签"
                    disabled={saving}
                    rootOption={{ value: 0, label: "未合并" }}
                    excludeIds={itemId ? [itemId] : undefined}
                  />
                )}
              />
              <FieldError errors={[errors.mergedIntoTagId]} />
            </FieldContent>
          </Field>
          <div className="grid gap-3 sm:grid-cols-2">
            <Controller
              control={control}
              name="aiEnabled"
              render={({ field }) => (
                <label className="flex items-center justify-between gap-3 rounded-md border px-3 py-2.5 text-sm">
                  <span>
                    <span className="block font-medium">允许 AI 使用</span>
                    <span className="block text-xs text-muted-foreground">标签模型只能选择已开启标签</span>
                  </span>
                  <Switch checked={field.value} onCheckedChange={field.onChange} />
                </label>
              )}
            />
            <Controller
              control={control}
              name="replyEnabled"
              render={({ field }) => (
                <label className="flex items-center justify-between gap-3 rounded-md border px-3 py-2.5 text-sm">
                  <span>
                    <span className="block font-medium">允许回复参考</span>
                    <span className="block text-xs text-muted-foreground">总开关默认关闭，本轮不会注入回复</span>
                  </span>
                  <Switch checked={field.value} onCheckedChange={field.onChange} />
                </label>
              )}
            />
          </div>
          <Field data-invalid={!!errors.remark}>
            <FieldLabel htmlFor="tag-remark">{t("tag.remark")}</FieldLabel>
            <FieldContent>
              <Textarea
                id="tag-remark"
                placeholder={t("tag.remarkPlaceholder")}
                rows={3}
                aria-invalid={!!errors.remark}
                {...register("remark")}
              />
              <FieldError errors={[errors.remark]} />
            </FieldContent>
          </Field>
        </form>
      )}
    </ProjectDialog>
  );
}
