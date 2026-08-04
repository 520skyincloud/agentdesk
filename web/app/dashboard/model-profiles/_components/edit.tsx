"use client"

import { useEffect } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import { Controller, useFieldArray, useForm } from "react-hook-form"
import { z } from "zod/v4"

import { OptionCombobox } from "@/components/option-combobox"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import type {
  ModelProfileSlotPayload,
  ModelProfileTemplate,
  ModelUsageSlotOption,
} from "@/lib/api/admin"

const slotSchema = z.object({
  usageCode: z.string().min(1),
  displayName: z.string().min(1),
  modelType: z.string().min(1),
  provider: z.literal("newapi"),
  modelName: z.string(),
  apiMode: z.string().min(1),
  dimension: z.number().int().min(0),
  maxContextTokens: z.number().int().min(0),
  maxOutputTokens: z.number().int().min(0),
  timeoutMs: z.number().int().positive(),
  maxRetryCount: z.number().int().min(0).max(10),
  temperature: z.number().min(0).max(2),
  schemaVersion: z.string(),
  promptTemplate: z.string(),
  jsonSchema: z.string(),
  enabled: z.boolean(),
  sortNo: z.number().int().positive(),
})

const formSchema = z.object({
  code: z.string().min(2).max(80),
  name: z.string().min(1).max(120),
  description: z.string(),
  gatewayBaseUrl: z.string(),
  slots: z.array(slotSchema).length(9),
})

export type ModelProfileFormValues = z.infer<typeof formSchema>

type ModelProfileEditDialogProps = {
  open: boolean
  saving: boolean
  profile: ModelProfileTemplate | null
  requiredSlots: ModelUsageSlotOption[]
  onOpenChange: (open: boolean) => void
  onSubmit: (values: ModelProfileFormValues) => Promise<void>
}

const apiModeOptions = [
  { value: "chat_completions", label: "Chat Completions" },
  { value: "responses", label: "Responses" },
  { value: "audio_transcriptions", label: "Audio Transcriptions" },
  { value: "embeddings", label: "Embeddings" },
  { value: "rerank", label: "Rerank" },
]

function defaultApiMode(usageCode: string) {
  if (usageCode === "asr") return "audio_transcriptions"
  if (usageCode === "embedding") return "embeddings"
  if (usageCode === "rerank") return "rerank"
  return "chat_completions"
}

function emptySlots(requiredSlots: ModelUsageSlotOption[]): ModelProfileSlotPayload[] {
  return requiredSlots.map((item, index) => ({
    usageCode: item.usageCode,
    displayName: item.displayName,
    modelType: item.expectedModelType,
    provider: "newapi",
    modelName: "",
    apiMode: defaultApiMode(item.usageCode),
    dimension: 0,
    maxContextTokens: 0,
    maxOutputTokens: 0,
    timeoutMs: 30000,
    maxRetryCount: 0,
    temperature: 0,
    schemaVersion:
      item.usageCode === "customer_tag_llm" ? "customer_tag_evolution.v1" : "",
    promptTemplate: "",
    jsonSchema: "",
    enabled: true,
    sortNo: index + 1,
  }))
}

function formValues(
  profile: ModelProfileTemplate | null,
  requiredSlots: ModelUsageSlotOption[],
): ModelProfileFormValues {
  if (!profile) {
    return {
      code: "",
      name: "",
      description: "",
      gatewayBaseUrl: "",
      slots: emptySlots(requiredSlots),
    }
  }
  return {
    code: profile.code,
    name: profile.name,
    description: profile.description,
    gatewayBaseUrl: profile.gatewayBaseUrl,
    slots: profile.slots.map((slot) => {
      const { id, ...payload } = slot
      void id
      return payload
    }),
  }
}

export function EditDialog({
  open,
  saving,
  profile,
  requiredSlots,
  onOpenChange,
  onSubmit,
}: ModelProfileEditDialogProps) {
  const form = useForm<ModelProfileFormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: formValues(profile, requiredSlots),
  })
  const fields = useFieldArray({ control: form.control, name: "slots" })

  useEffect(() => {
    if (open) form.reset(formValues(profile, requiredSlots))
  }, [form, open, profile, requiredSlots])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[92vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-6xl">
        <DialogHeader className="border-b px-6 py-5">
          <DialogTitle>{profile ? `编辑 ${profile.name}` : "新建模型方案"}</DialogTitle>
          <DialogDescription>
            {profile ? `${profile.code} · 版本 ${profile.revision}` : "草稿版本 1"}
          </DialogDescription>
        </DialogHeader>

        <form
          id="model-profile-form"
          className="min-h-0 flex-1 overflow-y-auto"
          onSubmit={form.handleSubmit(onSubmit)}
        >
          <section className="grid gap-4 border-b px-6 py-5 md:grid-cols-2 xl:grid-cols-4">
            <Field>
              <FieldLabel htmlFor="profile-code">方案编码</FieldLabel>
              <Input
                id="profile-code"
                disabled={Boolean(profile)}
                {...form.register("code")}
              />
              <FieldError>{form.formState.errors.code?.message}</FieldError>
            </Field>
            <Field>
              <FieldLabel htmlFor="profile-name">方案名称</FieldLabel>
              <Input id="profile-name" {...form.register("name")} />
              <FieldError>{form.formState.errors.name?.message}</FieldError>
            </Field>
            <Field className="md:col-span-2">
              <FieldLabel htmlFor="profile-gateway">统一 NewAPI 网关</FieldLabel>
              <Input
                id="profile-gateway"
                placeholder="https://gateway.example.com/v1"
                {...form.register("gatewayBaseUrl")}
              />
              <FieldError>{form.formState.errors.gatewayBaseUrl?.message}</FieldError>
            </Field>
            <Field className="md:col-span-2 xl:col-span-4">
              <FieldLabel htmlFor="profile-description">备注</FieldLabel>
              <Textarea
                id="profile-description"
                className="min-h-20 resize-y"
                {...form.register("description")}
              />
            </Field>
          </section>

          <section className="divide-y">
            {fields.fields.map((field, index) => {
              const modelType = field.modelType
              const usageCode = field.usageCode
              const needsTokens = modelType === "llm" || modelType === "vision"
              return (
                <div key={field.id} className="grid gap-4 px-6 py-5 xl:grid-cols-[180px_1fr_180px_130px_130px]">
                  <div className="min-w-0">
                    <div className="font-medium">{field.displayName}</div>
                    <div className="mt-1 flex flex-wrap gap-1.5">
                      <Badge variant="outline">{field.usageCode}</Badge>
                      <Badge variant="secondary">{field.modelType}</Badge>
                      <Badge variant="outline">NewAPI</Badge>
                    </div>
                    {usageCode === "asr" ? (
                      <Controller
                        control={form.control}
                        name={`slots.${index}.enabled`}
                        render={({ field: enabledField }) => (
                          <label className="mt-3 flex items-center gap-2 text-xs text-muted-foreground">
                            <Switch
                              checked={enabledField.value}
                              onCheckedChange={enabledField.onChange}
                              aria-label="启用语音识别模型槽"
                            />
                            启用语音识别
                          </label>
                        )}
                      />
                    ) : null}
                  </div>
                  <Field>
                    <FieldLabel htmlFor={`slot-${index}-model`}>模型名</FieldLabel>
                    <Input
                      id={`slot-${index}-model`}
                      {...form.register(`slots.${index}.modelName`)}
                    />
                  </Field>
                  <Field>
                    <FieldLabel>API 模式</FieldLabel>
                    <Controller
                      control={form.control}
                      name={`slots.${index}.apiMode`}
                      render={({ field: controllerField }) => (
                        <OptionCombobox
                          value={controllerField.value}
                          options={apiModeOptions}
                          placeholder="选择模式"
                          onChange={controllerField.onChange}
                        />
                      )}
                    />
                  </Field>
                  {modelType === "embedding" ? (
                    <Field>
                      <FieldLabel htmlFor={`slot-${index}-dimension`}>向量维度</FieldLabel>
                      <Input
                        id={`slot-${index}-dimension`}
                        type="number"
                        min={0}
                        {...form.register(`slots.${index}.dimension`, { valueAsNumber: true })}
                      />
                    </Field>
                  ) : (
                    <Field>
                      <FieldLabel htmlFor={`slot-${index}-timeout`}>超时 ms</FieldLabel>
                      <Input
                        id={`slot-${index}-timeout`}
                        type="number"
                        min={1}
                        {...form.register(`slots.${index}.timeoutMs`, { valueAsNumber: true })}
                      />
                    </Field>
                  )}
                  <Field>
                    <FieldLabel htmlFor={`slot-${index}-retry`}>重试</FieldLabel>
                    <Input
                      id={`slot-${index}-retry`}
                      type="number"
                      min={0}
                      max={10}
                      {...form.register(`slots.${index}.maxRetryCount`, { valueAsNumber: true })}
                    />
                  </Field>
                  {needsTokens ? (
                    <div className="grid gap-4 sm:grid-cols-3 xl:col-start-2 xl:col-span-4">
                      <Field>
                        <FieldLabel htmlFor={`slot-${index}-context`}>上下文 Token</FieldLabel>
                        <Input
                          id={`slot-${index}-context`}
                          type="number"
                          min={0}
                          {...form.register(`slots.${index}.maxContextTokens`, { valueAsNumber: true })}
                        />
                      </Field>
                      <Field>
                        <FieldLabel htmlFor={`slot-${index}-output`}>输出 Token</FieldLabel>
                        <Input
                          id={`slot-${index}-output`}
                          type="number"
                          min={0}
                          {...form.register(`slots.${index}.maxOutputTokens`, { valueAsNumber: true })}
                        />
                      </Field>
                      <Field>
                        <FieldLabel htmlFor={`slot-${index}-temperature`}>随机度</FieldLabel>
                        <Input
                          id={`slot-${index}-temperature`}
                          type="number"
                          min={0}
                          max={2}
                          step={0.1}
                          {...form.register(`slots.${index}.temperature`, { valueAsNumber: true })}
                        />
                      </Field>
                    </div>
                  ) : null}
                  {usageCode === "customer_tag_llm" ? (
                    <div className="grid gap-4 xl:col-start-2 xl:col-span-4 xl:grid-cols-2">
                      <Field>
                        <FieldLabel htmlFor={`slot-${index}-schema-version`}>Schema 版本</FieldLabel>
                        <Input
                          id={`slot-${index}-schema-version`}
                          {...form.register(`slots.${index}.schemaVersion`)}
                        />
                      </Field>
                      <div />
                      <Field>
                        <FieldLabel htmlFor={`slot-${index}-prompt`}>Prompt</FieldLabel>
                        <Textarea
                          id={`slot-${index}-prompt`}
                          className="min-h-32 resize-y font-mono text-xs"
                          {...form.register(`slots.${index}.promptTemplate`)}
                        />
                      </Field>
                      <Field>
                        <FieldLabel htmlFor={`slot-${index}-schema`}>JSON Schema</FieldLabel>
                        <Textarea
                          id={`slot-${index}-schema`}
                          className="min-h-32 resize-y font-mono text-xs"
                          {...form.register(`slots.${index}.jsonSchema`)}
                        />
                      </Field>
                    </div>
                  ) : null}
                </div>
              )
            })}
          </section>
        </form>

        <DialogFooter className="mx-0 mb-0 rounded-none px-6">
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button type="submit" form="model-profile-form" disabled={saving}>
            {saving ? "保存中..." : "保存草稿"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
