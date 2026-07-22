"use client";

import { useEffect, useMemo, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { Controller, Resolver, useForm, useWatch } from "react-hook-form";
import { z } from "zod/v4";

import { ProjectDialog } from "@/components/project-dialog";
import { OptionCombobox } from "@/components/option-combobox";
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
import { fetchCompanies, type AdminCompany } from "@/lib/api/company";
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
  aliases: "",
  aiEnabled: false,
  replyEnabled: false,
  applicableScene: "",
  remark: "",
};

type EditForm = {
  companyId: number;
  parentId: string;
  name: string;
  aliases: string;
  aiEnabled: boolean;
  replyEnabled: boolean;
  applicableScene: string;
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
    aliases: item.aliases,
    aiEnabled: item.aiEnabled,
    replyEnabled: item.replyEnabled,
    applicableScene: item.applicableScene,
    remark: item.remark,
  };
}

function buildPayload(form: EditForm): CreateTagPayload {
  return {
    companyId: form.companyId,
    parentId: Number(form.parentId),
    name: form.name.trim(),
    aliases: form.aliases.trim(),
    aiEnabled: form.aiEnabled,
    replyEnabled: form.replyEnabled,
    applicableScene: form.applicableScene.trim(),
    remark: form.remark.trim(),
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
  const [parentOptionsLoaded, setParentOptionsLoaded] = useState(false);
  const [companies, setCompanies] = useState<AdminCompany[]>([]);
  const [systemDefined, setSystemDefined] = useState(false);

  const tagFormSchema = useMemo(
    () =>
      z.object({
        parentId: z.string(),
        name: z.string().trim().min(1, t("tag.nameRequired")),
        companyId: z.number(),
        aliases: z.string(),
        aiEnabled: z.boolean(),
        replyEnabled: z.boolean(),
        applicableScene: z.string(),
        remark: z.string(),
      }).superRefine((value, context) => {
        const maxLength = value.parentId === "0" ? 20 : 5;
        if ([...value.name.trim()].length > maxLength) {
          context.addIssue({ code: "custom", path: ["name"], message: `名称最多 ${maxLength} 个字` });
        }
        if (value.parentId !== "0" && value.replyEnabled && !value.applicableScene) {
          context.addIssue({ code: "custom", path: ["applicableScene"], message: "请选择适用场景" });
        }
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
    setValue,
    formState: { errors },
  } = form;
  const parentId = useWatch({ control, name: "parentId" });
  const companyId = useWatch({ control, name: "companyId" });
  const isCategory = parentId === "0";

  const visibleParentTags = useMemo(
    () => parentTags.filter((item) => item.companyId === 0 || item.companyId === companyId),
    [companyId, parentTags],
  );

  const companyOptions = useMemo(
    () => [
      { value: "0", label: "全局" },
      ...companies.map((company) => ({ value: String(company.id), label: company.name })),
    ],
    [companies],
  );
  const sceneOptions = useMemo(
    () => [
      { value: "", label: "不参与回复" },
      { value: "room_assignment", label: "房间安排" },
      { value: "room_selection", label: "房型选择" },
      { value: "arrival_service", label: "到店入住" },
      { value: "stay_service", label: "连住续住" },
      { value: "checkout_service", label: "退房服务" },
      { value: "invoice_service", label: "发票服务" },
      { value: "parking_service", label: "停车服务" },
      { value: "pet_service", label: "宠物服务" },
      { value: "room_service", label: "客房服务" },
      { value: "customer_profile", label: "客户画像" },
    ],
    [],
  );

  useEffect(() => {
    async function loadFormOptions() {
      try {
        const [tagData, companyData] = await Promise.all([
          fetchTagsAll(),
          fetchCompanies({ page: 1, limit: 1000, status: 0 }),
        ]);
        setParentTags(
          (Array.isArray(tagData) ? tagData : [])
            .filter((item) => item.parentId === 0)
            .map((item) => ({ ...item, children: [] })),
        );
        setParentOptionsLoaded(true);
        setCompanies(Array.isArray(companyData.results) ? companyData.results : []);
      } catch (error) {
        console.error("Failed to load tag form options:", error);
      }
    }
    void loadFormOptions();
  }, [itemId]);

  useEffect(() => {
    const selectedParentId = Number(parentId);
    if (
      !parentOptionsLoaded ||
      selectedParentId <= 0 ||
      visibleParentTags.some((item) => item.id === selectedParentId)
    ) {
      return;
    }
    setValue("parentId", "0", { shouldValidate: true });
  }, [parentId, parentOptionsLoaded, setValue, visibleParentTags]);

  useEffect(() => {
    async function loadDetail() {
      if (!itemId) {
        setSystemDefined(false);
        reset(emptyForm);
        return;
      }
      setLoading(true);
      try {
        const data = await fetchTag(itemId);
        setSystemDefined(data.systemDefined);
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
          <Field data-invalid={!!errors.companyId}>
            <FieldLabel>作用域</FieldLabel>
            <FieldContent>
              <Controller
                control={control}
                name="companyId"
                render={({ field }) => (
                  <OptionCombobox
                    value={String(field.value)}
                    options={companyOptions}
                    placeholder="选择作用域"
                    searchPlaceholder="搜索公司"
                    emptyText="暂无可用公司"
                    disabled={saving || Boolean(itemId)}
                    onChange={(value) => field.onChange(Number(value ?? 0))}
                  />
                )}
              />
              <FieldError errors={[errors.companyId]} />
            </FieldContent>
          </Field>
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
                    tags={visibleParentTags}
                    placeholder={t("tag.rootParent")}
                    searchPlaceholder={t("tag.searchParent")}
                    emptyText={t("tag.emptyParent")}
                    disabled={saving || systemDefined}
                    rootOption={!itemId || isCategory ? { value: 0, label: t("tag.rootParent") } : undefined}
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
                maxLength={isCategory ? 20 : 5}
                disabled={systemDefined}
                aria-invalid={!!errors.name}
                {...register("name")}
              />
              <FieldError errors={[errors.name]} />
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
            <Field data-invalid={!!errors.applicableScene}>
              <FieldLabel>适用场景</FieldLabel>
              <FieldContent>
                <Controller
                  control={control}
                  name="applicableScene"
                  render={({ field }) => (
                    <OptionCombobox
                      value={field.value}
                      options={sceneOptions}
                      placeholder="选择适用场景"
                      searchPlaceholder="搜索场景"
                      emptyText="暂无场景"
                      disabled={saving || isCategory}
                      onChange={(value) => field.onChange(value ?? "")}
                    />
                  )}
                />
                <FieldError errors={[errors.applicableScene]} />
              </FieldContent>
            </Field>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <Controller
              control={control}
              name="aiEnabled"
              render={({ field }) => (
                <label className="flex items-center justify-between gap-3 rounded-md border px-3 py-2.5 text-sm">
                  <span className="font-medium">AI 提取</span>
                  <Switch checked={field.value} disabled={isCategory} onCheckedChange={field.onChange} />
                </label>
              )}
            />
            <Controller
              control={control}
              name="replyEnabled"
              render={({ field }) => (
                <label className="flex items-center justify-between gap-3 rounded-md border px-3 py-2.5 text-sm">
                  <span className="font-medium">回复参考</span>
                  <Switch checked={field.value} disabled={isCategory} onCheckedChange={field.onChange} />
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
