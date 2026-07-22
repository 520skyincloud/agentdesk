"use client"

import { useEffect, useMemo, useState } from "react"
import {
  BeakerIcon,
  LoaderCircleIcon,
  PlusIcon,
  SaveIcon,
  Settings2Icon,
  Trash2Icon,
} from "lucide-react"
import { toast } from "sonner"

import { OptionCombobox } from "@/components/option-combobox"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import {
  fetchModelProfileTemplate,
  fetchStoreModelCredentialStores,
  testModelProfileSlot,
  updateModelProfileTemplate,
  type ModelProfileSlot,
  type ModelProfileTemplate,
  type StoreModelCredentialStoreOption,
  type UpdateModelProfileTemplatePayload,
} from "@/lib/api/admin"

type Props = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const requiredTagUsageCode = "customer_tag_llm"

function newSlot(sortNo: number): ModelProfileSlot {
  return {
    id: 0,
    usageCode: "",
    displayName: "",
    modelType: "llm",
    provider: "openai",
    modelName: "",
    apiMode: "chat_completions",
    dimension: 0,
    maxContextTokens: 0,
    maxOutputTokens: 1024,
    timeoutMs: 30000,
    maxRetryCount: 0,
    temperature: 0,
    schemaVersion: "",
    promptTemplate: "",
    jsonSchema: "",
    enabled: true,
    sortNo,
  }
}

function toDraft(template: ModelProfileTemplate): UpdateModelProfileTemplatePayload {
  return {
    name: template.name,
    gatewayBaseUrl: template.gatewayBaseUrl,
    customerTagEvolutionEnabled: template.customerTagEvolutionEnabled,
    customerTagEvolutionStoreIds: template.customerTagEvolutionStoreIds ?? [],
    replyTagContextEnabled: template.replyTagContextEnabled,
    slots: template.slots.map((slot) => ({ ...slot })),
  }
}

function numberValue(value: string, fallback = 0) {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : fallback
}

export function ModelProfileTemplateDialog({ open, onOpenChange }: Props) {
  const [template, setTemplate] = useState<ModelProfileTemplate | null>(null)
  const [draft, setDraft] = useState<UpdateModelProfileTemplatePayload | null>(null)
  const [stores, setStores] = useState<StoreModelCredentialStoreOption[]>([])
  const [testStoreId, setTestStoreId] = useState("")
  const [testUsageCode, setTestUsageCode] = useState(requiredTagUsageCode)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)

  useEffect(() => {
    if (!open) {
      return
    }
    setLoading(true)
    Promise.all([fetchModelProfileTemplate(), fetchStoreModelCredentialStores()])
      .then(([nextTemplate, nextStores]) => {
        setTemplate(nextTemplate)
        setDraft(toDraft(nextTemplate))
        setStores(Array.isArray(nextStores) ? nextStores : [])
        const firstConfiguredStore = nextStores.find((item) => item.hasKey) ?? nextStores[0]
        setTestStoreId(firstConfiguredStore ? String(firstConfiguredStore.storeId) : "")
      })
      .catch((error) => {
        toast.error(error instanceof Error ? error.message : "读取全链路模型模板失败")
      })
      .finally(() => setLoading(false))
  }, [open])

  const valid = useMemo(() => {
    if (!draft?.name.trim() || !draft.gatewayBaseUrl.trim() || draft.slots.length === 0) {
      return false
    }
    if (
      draft.customerTagEvolutionEnabled &&
      draft.customerTagEvolutionStoreIds.length === 0
    ) {
      return false
    }
    const usageCodes = new Set<string>()
    let hasTagSlot = false
    for (const slot of draft.slots) {
      const usageCode = slot.usageCode.trim()
      if (
        !usageCode ||
        !slot.displayName.trim() ||
        !slot.modelType.trim() ||
        !slot.provider.trim() ||
        !slot.modelName.trim() ||
        usageCodes.has(usageCode)
      ) {
        return false
      }
      usageCodes.add(usageCode)
      if (usageCode === requiredTagUsageCode) {
        hasTagSlot =
          slot.modelType === "llm" &&
          slot.schemaVersion === "customer_tag_evolution.v1" &&
          Boolean(slot.jsonSchema.trim())
      }
    }
    return hasTagSlot
  }, [draft])

  function updateSlot(index: number, patch: Partial<ModelProfileSlot>) {
    setDraft((current) => {
      if (!current) return current
      return {
        ...current,
        slots: current.slots.map((slot, slotIndex) =>
          slotIndex === index ? { ...slot, ...patch } : slot
        ),
      }
    })
  }

  function removeSlot(index: number) {
    setDraft((current) => {
      if (!current || current.slots[index]?.usageCode === requiredTagUsageCode) {
        return current
      }
      return {
        ...current,
        slots: current.slots
          .filter((_, slotIndex) => slotIndex !== index)
          .map((slot, slotIndex) => ({ ...slot, sortNo: slotIndex + 1 })),
      }
    })
  }

  function toggleEvolutionStore(storeId: number, checked: boolean) {
    setDraft((current) => {
      if (!current) return current
      const selected = new Set(current.customerTagEvolutionStoreIds)
      if (checked) {
        selected.add(storeId)
      } else {
        selected.delete(storeId)
      }
      return {
        ...current,
        customerTagEvolutionStoreIds: Array.from(selected).sort((a, b) => a - b),
      }
    })
  }

  async function save() {
    if (!draft || !valid || saving) {
      toast.error("请完整填写模板，并保留有效的 customer_tag_llm 槽")
      return
    }
    setSaving(true)
    try {
      const result = await updateModelProfileTemplate({
        ...draft,
        slots: draft.slots.map((slot, index) => ({
          ...slot,
          usageCode: slot.usageCode.trim(),
          displayName: slot.displayName.trim(),
          provider: slot.provider.trim(),
          modelName: slot.modelName.trim(),
          sortNo: index + 1,
        })),
      })
      setTemplate(result)
      setDraft(toDraft(result))
      toast.success(`模型模板版本 ${result.revision} 已保存`)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存全链路模型模板失败")
    } finally {
      setSaving(false)
    }
  }

  async function testSelectedSlot() {
    const storeId = Number(testStoreId)
    if (!storeId || !testUsageCode || testing) {
      toast.error("请选择已设置门店密钥的测试门店和模型槽")
      return
    }
    setTesting(true)
    try {
      const result = await testModelProfileSlot(storeId, testUsageCode)
      toast.success(`${result.modelName} 测试通过，耗时 ${result.latencyMs}ms`)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "模型槽测试失败")
    } finally {
      setTesting(false)
    }
  }

  const disabled = loading || saving || testing
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[calc(100dvh-1.5rem)] w-[calc(100%-1.5rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-5xl">
        <DialogHeader className="shrink-0 border-b px-5 py-4 pr-12">
          <DialogTitle className="flex items-center gap-2">
            <Settings2Icon className="size-5" />
            全链路模型模板
          </DialogTitle>
          <DialogDescription>
            这里只维护统一网关和模型参数。所有真实调用自动读取对应门店当前生效的唯一密钥。
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          {loading || !draft ? (
            <div className="flex min-h-64 items-center justify-center gap-2 text-muted-foreground">
              <LoaderCircleIcon className="size-4 animate-spin" />
              正在读取模型模板
            </div>
          ) : (
            <div className="space-y-5">
              <section className="grid gap-4 md:grid-cols-2">
                <div className="space-y-1.5">
                  <Label>模板名称</Label>
                  <Input
                    value={draft.name}
                    disabled={disabled}
                    onChange={(event) =>
                      setDraft((current) =>
                        current ? { ...current, name: event.target.value } : current
                      )
                    }
                  />
                </div>
                <div className="space-y-1.5">
                  <Label>统一 New API 网关地址</Label>
                  <Input
                    value={draft.gatewayBaseUrl}
                    disabled={disabled}
                    placeholder="https://gateway.example.com/v1"
                    onChange={(event) =>
                      setDraft((current) =>
                        current
                          ? { ...current, gatewayBaseUrl: event.target.value }
                          : current
                      )
                    }
                  />
                </div>
                <label className="flex items-center justify-between gap-3 border px-3 py-2.5 text-sm">
                  <span>
                    <span className="block font-medium">24 小时客户标签进化</span>
                    <span className="block text-xs text-muted-foreground">静默满 24 小时且有增量消息才调用</span>
                  </span>
                  <Switch
                    checked={draft.customerTagEvolutionEnabled}
                    disabled={disabled}
                    onCheckedChange={(checked) =>
                      setDraft((current) =>
                        current
                          ? { ...current, customerTagEvolutionEnabled: checked }
                          : current
                      )
                    }
                  />
                </label>
                <label className="flex items-center justify-between gap-3 border px-3 py-2.5 text-sm">
                  <span>
                    <span className="block font-medium">标签参与最终回复</span>
                    <span className="block text-xs text-muted-foreground">默认关闭，不影响意图、检索、工具和路由</span>
                  </span>
                  <Switch
                    checked={draft.replyTagContextEnabled}
                    disabled={disabled}
                    onCheckedChange={(checked) =>
                      setDraft((current) =>
                        current ? { ...current, replyTagContextEnabled: checked } : current
                      )
                    }
                  />
                </label>
              </section>

              <section className="space-y-3 border-t pt-4">
                <div>
                  <h3 className="text-sm font-semibold">客户标签影子运行门店</h3>
                  <p className="mt-1 text-xs text-muted-foreground">
                    只处理勾选门店静默满 24 小时的增量会话；当前不会把标签写入回复上下文。
                  </p>
                </div>
                {stores.length === 0 ? (
                  <p className="border px-3 py-4 text-sm text-muted-foreground">
                    暂无可配置门店
                  </p>
                ) : (
                  <div className="grid gap-2 md:grid-cols-2">
                    {stores.map((store) => {
                      const checked = draft.customerTagEvolutionStoreIds.includes(
                        store.storeId
                      )
                      return (
                        <label
                          key={store.storeId}
                          className="flex items-start gap-3 border px-3 py-3 text-sm"
                        >
                          <Checkbox
                            checked={checked}
                            disabled={disabled}
                            onCheckedChange={(nextChecked) =>
                              toggleEvolutionStore(store.storeId, nextChecked === true)
                            }
                          />
                          <span className="min-w-0">
                            <span className="block font-medium">{store.storeName}</span>
                            <span className="block text-xs text-muted-foreground">
                              {store.storeCode || `门店 ${store.storeId}`}
                              {store.hasKey
                                ? ` · 密钥版本 ${store.credentialRevision}`
                                : " · 尚未设置门店密钥"}
                            </span>
                          </span>
                        </label>
                      )
                    })}
                  </div>
                )}
                {draft.customerTagEvolutionEnabled &&
                draft.customerTagEvolutionStoreIds.length === 0 ? (
                  <p className="text-xs text-destructive">
                    开启客户标签进化前，至少选择一个影子运行门店。
                  </p>
                ) : null}
              </section>

              <section className="space-y-3 border-t pt-4">
                <div className="flex items-center justify-between gap-3">
                  <div>
                    <h3 className="text-sm font-semibold">模型槽</h3>
                    <p className="mt-1 text-xs text-muted-foreground">
                      用途码决定调用位置；新增模型无需新增密钥字段。
                    </p>
                  </div>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={disabled}
                    onClick={() =>
                      setDraft((current) =>
                        current
                          ? {
                              ...current,
                              slots: [...current.slots, newSlot(current.slots.length + 1)],
                            }
                          : current
                      )
                    }
                  >
                    <PlusIcon className="size-4" />
                    新增模型槽
                  </Button>
                </div>

                <div className="space-y-3">
                  {draft.slots.map((slot, index) => (
                    <div key={`${slot.id}-${index}`} className="border p-4">
                      <div className="flex items-start justify-between gap-3">
                        <div>
                          <h4 className="text-sm font-semibold">
                            {slot.displayName || `模型槽 ${index + 1}`}
                          </h4>
                          <p className="mt-1 font-mono text-xs text-muted-foreground">
                            {slot.usageCode || "尚未填写用途码"}
                          </p>
                        </div>
                        <div className="flex items-center gap-2">
                          <Switch
                            checked={slot.enabled}
                            disabled={disabled}
                            aria-label={`启用 ${slot.displayName || slot.usageCode}`}
                            onCheckedChange={(checked) =>
                              updateSlot(index, { enabled: checked })
                            }
                          />
                          <Button
                            type="button"
                            size="icon-sm"
                            variant="ghost"
                            disabled={disabled || slot.usageCode === requiredTagUsageCode}
                            title={
                              slot.usageCode === requiredTagUsageCode
                                ? "客户标签模型槽必须保留"
                                : "删除模型槽"
                            }
                            onClick={() => removeSlot(index)}
                          >
                            <Trash2Icon className="size-4" />
                          </Button>
                        </div>
                      </div>

                      <div className="mt-4 grid gap-3 md:grid-cols-2 lg:grid-cols-3">
                        <div className="space-y-1.5">
                          <Label>用途码</Label>
                          <Input
                            value={slot.usageCode}
                            disabled={disabled}
                            placeholder="例如 customer_tag_llm"
                            onChange={(event) =>
                              updateSlot(index, { usageCode: event.target.value })
                            }
                          />
                        </div>
                        <div className="space-y-1.5">
                          <Label>显示名称</Label>
                          <Input
                            value={slot.displayName}
                            disabled={disabled}
                            onChange={(event) =>
                              updateSlot(index, { displayName: event.target.value })
                            }
                          />
                        </div>
                        <div className="space-y-1.5">
                          <Label>模型类型</Label>
                          <OptionCombobox
                            value={slot.modelType}
                            disabled={disabled}
                            placeholder="选择模型类型"
                            options={[
                              { value: "llm", label: "大语言模型" },
                              { value: "vision", label: "视觉模型" },
                              { value: "asr", label: "语音识别" },
                              { value: "embedding", label: "向量模型" },
                              { value: "rerank", label: "重排模型" },
                            ]}
                            onChange={(value) => updateSlot(index, { modelType: value })}
                          />
                        </div>
                        <div className="space-y-1.5">
                          <Label>Provider</Label>
                          <Input
                            value={slot.provider}
                            disabled={disabled}
                            onChange={(event) =>
                              updateSlot(index, { provider: event.target.value })
                            }
                          />
                        </div>
                        <div className="space-y-1.5">
                          <Label>模型名</Label>
                          <Input
                            value={slot.modelName}
                            disabled={disabled}
                            onChange={(event) =>
                              updateSlot(index, { modelName: event.target.value })
                            }
                          />
                        </div>
                        <div className="space-y-1.5">
                          <Label>调用协议</Label>
                          <OptionCombobox
                            value={slot.apiMode || "chat_completions"}
                            disabled={disabled}
                            placeholder="选择调用协议"
                            options={[
                              { value: "chat_completions", label: "Chat Completions" },
                              { value: "responses", label: "Responses" },
                            ]}
                            onChange={(value) => updateSlot(index, { apiMode: value })}
                          />
                        </div>
                        <div className="space-y-1.5">
                          <Label>上下文 Token</Label>
                          <Input
                            type="number"
                            min={0}
                            value={slot.maxContextTokens}
                            disabled={disabled}
                            onChange={(event) =>
                              updateSlot(index, {
                                maxContextTokens: numberValue(event.target.value),
                              })
                            }
                          />
                        </div>
                        <div className="space-y-1.5">
                          <Label>输出 Token</Label>
                          <Input
                            type="number"
                            min={0}
                            value={slot.maxOutputTokens}
                            disabled={disabled}
                            onChange={(event) =>
                              updateSlot(index, {
                                maxOutputTokens: numberValue(event.target.value),
                              })
                            }
                          />
                        </div>
                        <div className="space-y-1.5">
                          <Label>超时毫秒</Label>
                          <Input
                            type="number"
                            min={1000}
                            value={slot.timeoutMs}
                            disabled={disabled}
                            onChange={(event) =>
                              updateSlot(index, { timeoutMs: numberValue(event.target.value) })
                            }
                          />
                        </div>
                        <div className="space-y-1.5">
                          <Label>重试次数</Label>
                          <Input
                            type="number"
                            min={0}
                            max={2}
                            value={slot.maxRetryCount}
                            disabled={disabled}
                            onChange={(event) =>
                              updateSlot(index, {
                                maxRetryCount: numberValue(event.target.value),
                              })
                            }
                          />
                        </div>
                        <div className="space-y-1.5">
                          <Label>Temperature</Label>
                          <Input
                            type="number"
                            min={0}
                            max={2}
                            step={0.1}
                            value={slot.temperature}
                            disabled={disabled}
                            onChange={(event) =>
                              updateSlot(index, {
                                temperature: numberValue(event.target.value),
                              })
                            }
                          />
                        </div>
                        <div className="space-y-1.5">
                          <Label>向量维度</Label>
                          <Input
                            type="number"
                            min={0}
                            value={slot.dimension}
                            disabled={disabled}
                            onChange={(event) =>
                              updateSlot(index, { dimension: numberValue(event.target.value) })
                            }
                          />
                        </div>
                      </div>

                      {slot.usageCode === requiredTagUsageCode ? (
                        <div className="mt-4 grid gap-3 border-t pt-4">
                          <div className="space-y-1.5">
                            <Label>Schema 版本</Label>
                            <Input
                              value={slot.schemaVersion}
                              disabled={disabled}
                              onChange={(event) =>
                                updateSlot(index, { schemaVersion: event.target.value })
                              }
                            />
                          </div>
                          <div className="space-y-1.5">
                            <Label>标签模型系统 Prompt</Label>
                            <Textarea
                              rows={5}
                              value={slot.promptTemplate}
                              disabled={disabled}
                              onChange={(event) =>
                                updateSlot(index, { promptTemplate: event.target.value })
                              }
                            />
                          </div>
                          <div className="space-y-1.5">
                            <Label>严格 JSON Schema</Label>
                            <Textarea
                              rows={8}
                              className="font-mono text-xs"
                              value={slot.jsonSchema}
                              disabled={disabled}
                              onChange={(event) =>
                                updateSlot(index, { jsonSchema: event.target.value })
                              }
                            />
                          </div>
                        </div>
                      ) : null}
                    </div>
                  ))}
                </div>
              </section>

              <section className="grid gap-3 border-t pt-4 md:grid-cols-[1fr_1fr_auto] md:items-end">
                <div className="space-y-1.5">
                  <Label>真实测试门店</Label>
                  <OptionCombobox
                    value={testStoreId}
                    disabled={disabled}
                    placeholder="选择测试门店"
                    options={stores.map((store) => ({
                      value: String(store.storeId),
                      label: `${store.storeName}${store.hasKey ? "" : "（未设置密钥）"}`,
                    }))}
                    onChange={setTestStoreId}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label>测试模型槽</Label>
                  <OptionCombobox
                    value={testUsageCode}
                    disabled={disabled}
                    placeholder="选择测试模型槽"
                    options={draft.slots
                      .filter((slot) => slot.enabled && slot.modelType === "llm")
                      .map((slot) => ({
                        value: slot.usageCode,
                        label: slot.displayName || slot.usageCode,
                      }))}
                    onChange={setTestUsageCode}
                  />
                </div>
                <Button
                  type="button"
                  variant="outline"
                  disabled={disabled || !testStoreId || !testUsageCode}
                  onClick={() => void testSelectedSlot()}
                >
                  {testing ? (
                    <LoaderCircleIcon className="size-4 animate-spin" />
                  ) : (
                    <BeakerIcon className="size-4" />
                  )}
                  真实测试
                </Button>
              </section>
            </div>
          )}
        </div>

        <DialogFooter className="m-0 shrink-0 rounded-none border-t px-5 py-4">
          <div className="mr-auto text-xs text-muted-foreground">
            当前版本 {template?.revision || "-"}，密钥不在本页面传输或返回
          </div>
          <Button
            type="button"
            variant="outline"
            disabled={saving || testing}
            onClick={() => onOpenChange(false)}
          >
            关闭
          </Button>
          <Button type="button" disabled={disabled || !valid} onClick={() => void save()}>
            {saving ? (
              <LoaderCircleIcon className="size-4 animate-spin" />
            ) : (
              <SaveIcon className="size-4" />
            )}
            保存模板
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
