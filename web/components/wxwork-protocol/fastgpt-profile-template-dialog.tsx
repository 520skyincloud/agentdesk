"use client"

import { useEffect, useMemo, useState } from "react"
import { DatabaseZapIcon, LoaderCircleIcon, RefreshCwIcon, SaveIcon, ShieldCheckIcon } from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { OptionCombobox } from "@/components/option-combobox"
import {
  fetchFastGPTProfileTemplate,
  syncFastGPTProfileTemplate,
  updateFastGPTProfileTemplate,
  type FastGPTProfileTemplate,
  type FastGPTProfileTemplateCredential,
  type UpdateFastGPTProfileTemplatePayload,
} from "@/lib/api/admin"
import { formatDateTime, repairMojibakeText } from "@/lib/utils"

type Props = {
  open: boolean
  canSave: boolean
  onOpenChange: (open: boolean) => void
  onChanged?: () => void
}

const emptyCredential = (): FastGPTProfileTemplateCredential => ({
  provider: "",
  baseUrl: "",
  model: "",
})

const emptyDraft = (): UpdateFastGPTProfileTemplatePayload => ({
  name: "门店知识库模型模板",
  chat: { ...emptyCredential(), apiMode: "chat_completions" },
  asr: emptyCredential(),
  embedding: emptyCredential(),
  documentParser: emptyCredential(),
  vision: emptyCredential(),
  rerank: emptyCredential(),
})

function toDraft(template?: FastGPTProfileTemplate | null): UpdateFastGPTProfileTemplatePayload {
  if (!template) return emptyDraft()
  return {
    name: template.name || "门店知识库模型模板",
    chat: { ...template.chat, apiMode: template.chat.apiMode || "chat_completions" },
    asr: { ...template.asr },
    embedding: { ...template.embedding },
    documentParser: { ...template.documentParser },
    vision: { ...template.vision },
    rerank: { ...template.rerank },
  }
}

function credentialComplete(value: FastGPTProfileTemplateCredential) {
  return Boolean(value.provider.trim() && value.baseUrl.trim() && value.model.trim())
}

function syncStatusLabel(status: string) {
  switch (status) {
    case "ready":
      return "已同步"
    case "syncing":
      return "同步中"
    case "pending":
      return "等待同步"
    case "failed":
      return "等待重试"
    case "blocked":
      return "需要配置密钥"
    default:
      return "未同步"
  }
}

function syncStatusVariant(status: string): "default" | "secondary" | "outline" | "destructive" {
  if (status === "ready") return "default"
  if (status === "failed") return "destructive"
  if (status === "pending" || status === "syncing") return "secondary"
  return "outline"
}

function syncErrorLabel(value: string) {
  switch (value) {
    case "store_profile_key_unconfigured":
      return "请先为该门店设置独立密钥"
    case "knowledge_base_unavailable":
      return "门店暂无可用知识库"
    case "fastgpt_timeout":
      return "FastGPT 同步超时"
    case "fastgpt_http_4xx":
      return "模型参数或密钥未通过验证"
    case "fastgpt_http_5xx":
      return "FastGPT 服务暂时不可用"
    case "fastgpt_request_failed":
      return "FastGPT 同步失败"
    default:
      return value
  }
}

function CredentialTemplateEditor({
  title,
  description,
  value,
  disabled,
  showApiMode = false,
  onChange,
}: {
  title: string
  description: string
  value: FastGPTProfileTemplateCredential
  disabled: boolean
  showApiMode?: boolean
  onChange: (value: FastGPTProfileTemplateCredential) => void
}) {
  const update = (field: keyof FastGPTProfileTemplateCredential, next: string) => onChange({ ...value, [field]: next })
  return (
    <section className="border-t border-border py-4 first:border-t-0 first:pt-0">
      <h3 className="text-sm font-semibold">{title}</h3>
      <p className="mt-1 text-xs leading-5 text-muted-foreground">{description}</p>
      <div className="mt-3 grid gap-3 md:grid-cols-2">
        <div className="space-y-1.5">
          <Label>Provider</Label>
          <Input value={value.provider} disabled={disabled} placeholder="DashScope" onChange={(event) => update("provider", event.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label>模型名</Label>
          <Input value={value.model} disabled={disabled} placeholder="供应商真实模型名" onChange={(event) => update("model", event.target.value)} />
        </div>
        {showApiMode ? (
          <div className="space-y-1.5 md:col-span-2">
            <Label>调用协议</Label>
            <OptionCombobox
              value={value.apiMode || "chat_completions"}
              options={[
                { value: "chat_completions", label: "Chat Completions" },
                { value: "responses", label: "Responses" },
              ]}
              placeholder="选择调用协议"
              disabled={disabled}
              onChange={(next) => update("apiMode", next)}
            />
          </div>
        ) : null}
      </div>
    </section>
  )
}

export function FastGPTProfileTemplateDialog({ open, canSave, onOpenChange, onChanged }: Props) {
  const [template, setTemplate] = useState<FastGPTProfileTemplate | null>(null)
  const [draft, setDraft] = useState<UpdateFastGPTProfileTemplatePayload>(() => emptyDraft())
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [syncing, setSyncing] = useState(false)

  async function load(showError = true) {
    try {
      const result = await fetchFastGPTProfileTemplate()
      setTemplate(result)
      if (!saving) setDraft(toDraft(result))
      return result
    } catch (error) {
      if (showError) toast.error(error instanceof Error ? error.message : "读取 Profile 模板失败")
      return null
    }
  }

  useEffect(() => {
    if (!open) return
    setLoading(true)
    void load().finally(() => setLoading(false))
  }, [open])

  useEffect(() => {
    if (!open || !template || template.sync.pending + template.sync.failed === 0) return
    const timer = window.setInterval(() => {
      void load(false)
    }, 3000)
    return () => window.clearInterval(timer)
  }, [open, template?.revision, template?.sync.pending, template?.sync.failed])

  const valid = useMemo(
    () =>
      Boolean(
        draft.name.trim() &&
          credentialComplete(draft.chat) &&
          credentialComplete(draft.asr) &&
          credentialComplete(draft.embedding) &&
          credentialComplete(draft.documentParser) &&
          credentialComplete(draft.vision) &&
          credentialComplete(draft.rerank),
      ),
    [draft],
  )

  function updateCredential(
    field: "chat" | "asr" | "embedding" | "documentParser" | "vision" | "rerank",
    value: FastGPTProfileTemplateCredential,
  ) {
    setDraft((current) => ({ ...current, [field]: value }))
  }

  function updateGatewayBaseURL(baseUrl: string) {
    setDraft((current) => ({
      ...current,
      chat: { ...current.chat, baseUrl },
      asr: { ...current.asr, baseUrl },
      embedding: { ...current.embedding, baseUrl },
      documentParser: { ...current.documentParser, baseUrl },
      vision: { ...current.vision, baseUrl },
      rerank: { ...current.rerank, baseUrl },
    }))
  }

  async function save() {
    if (!valid) {
      toast.error("请完整填写六个模型的 Provider、Base URL 和模型名")
      return
    }
    setSaving(true)
    try {
      const result = await updateFastGPTProfileTemplate(draft)
      setTemplate(result)
      setDraft(toDraft(result))
      toast.success(`模板版本 ${result.revision} 已保存，门店正在热更新`)
      onChanged?.()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存 Profile 模板失败")
    } finally {
      setSaving(false)
    }
  }

  async function retryAll() {
    setSyncing(true)
    try {
      const result = await syncFastGPTProfileTemplate()
      setTemplate(result)
      toast.success("已重新提交全部门店同步任务")
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "重新同步失败")
    } finally {
      setSyncing(false)
    }
  }

  const disabled = loading || saving || syncing || !canSave
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[calc(100dvh-1.5rem)] w-[calc(100%-1.5rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-4xl">
        <DialogHeader className="shrink-0 border-b px-5 py-4 pr-12">
          <DialogTitle className="flex items-center gap-2">
            <DatabaseZapIcon className="size-5" />
            知识库 Profile 模板
          </DialogTitle>
          <DialogDescription>统一所有门店的模型参数；每个门店的 API Key 继续独立保存在 FastGPT。</DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          {loading ? (
            <div className="flex min-h-56 items-center justify-center gap-2 text-muted-foreground">
              <LoaderCircleIcon className="size-4 animate-spin" />
              正在读取模板
            </div>
          ) : (
            <>
              <div className="mb-4 flex items-start gap-3 border border-blue-200 bg-blue-50 p-3 text-sm text-blue-950">
                <ShieldCheckIcon className="mt-0.5 size-4 shrink-0" />
                <p className="leading-6">
                  模板不接收也不保存密钥。保存后，每个门店使用自己当前生效的唯一密钥完成真实模型测试，并热更新全部模型槽。
                </p>
              </div>

              <div className="mb-4 grid gap-3 md:grid-cols-[1fr_auto] md:items-end">
                <div className="space-y-1.5">
                  <Label>模板名称</Label>
                  <Input value={draft.name} disabled={disabled} onChange={(event) => setDraft((current) => ({ ...current, name: event.target.value }))} />
                </div>
                <div className="flex h-9 items-center gap-2">
                  <Badge variant="secondary">版本 {template?.revision || "未保存"}</Badge>
                  <Badge variant="outline">{template?.status || "unconfigured"}</Badge>
                </div>
              </div>

              <section className="mb-4 border border-border bg-muted/30 p-4">
                <div className="space-y-1.5">
                  <Label>统一 New API 网关地址</Label>
                  <Input
                    value={draft.chat.baseUrl}
                    disabled={disabled}
                    placeholder="https://gateway.example.com/v1"
                    onChange={(event) => updateGatewayBaseURL(event.target.value)}
                  />
                  <p className="text-xs leading-5 text-muted-foreground">
                    修改一次会自动应用到全部模型槽；保存后门店密钥保持不变，模型名和 Provider 仍可分别自定义。
                  </p>
                </div>
              </section>

              <CredentialTemplateEditor title="对话模型" description="回复、意图识别、技能路由、记忆与转人工摘要统一使用。" value={draft.chat} disabled={disabled} showApiMode onChange={(value) => updateCredential("chat", value)} />
              <CredentialTemplateEditor title="语音识别模型" description="保留后台配置供语音转写使用，不参与门店密钥切换的阻断验证。" value={draft.asr} disabled={disabled} onChange={(value) => updateCredential("asr", value)} />
              <CredentialTemplateEditor title="向量模型" description="文件索引和检索查询使用。" value={draft.embedding} disabled={disabled} onChange={(value) => updateCredential("embedding", value)} />
              <CredentialTemplateEditor title="文档理解模型" description="文档解析与问答拆分使用。" value={draft.documentParser} disabled={disabled} onChange={(value) => updateCredential("documentParser", value)} />
              <CredentialTemplateEditor title="视觉理解模型" description="图片知识解析使用。" value={draft.vision} disabled={disabled} onChange={(value) => updateCredential("vision", value)} />
              <CredentialTemplateEditor title="重排模型" description="知识检索重排必填模型。" value={draft.rerank} disabled={disabled} onChange={(value) => updateCredential("rerank", value)} />

              <section className="border-t pt-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <h3 className="text-sm font-semibold">门店热更新状态</h3>
                    <p className="mt-1 text-xs text-muted-foreground">
                      共 {template?.sync.total || 0} 家，已同步 {template?.sync.ready || 0}，处理中 {template?.sync.pending || 0}，失败 {template?.sync.failed || 0}，待密钥 {template?.sync.blocked || 0}
                    </p>
                  </div>
                  <Button type="button" size="sm" variant="outline" disabled={disabled || !template?.revision} onClick={() => void retryAll()}>
                    <RefreshCwIcon className={syncing ? "size-4 animate-spin" : "size-4"} />
                    全部重新同步
                  </Button>
                </div>
                <div className="mt-3 divide-y border">
                  {(template?.stores || []).map((item) => (
                    <div key={item.storeId} className="grid gap-2 px-3 py-3 text-sm md:grid-cols-[minmax(0,1fr)_auto_auto] md:items-center">
                      <div className="min-w-0">
                        <div className="truncate font-medium">{repairMojibakeText(item.storeName) || `门店 ${item.storeId}`}</div>
                        <div className="mt-1 truncate text-xs text-muted-foreground">
                          {item.profileName || "尚未创建 Profile"} · Profile 版本 {item.profileRevision || "-"}
                        </div>
                        {item.lastError ? <div className="mt-1 text-xs text-destructive">{syncErrorLabel(item.lastError)}</div> : null}
                      </div>
                      <Badge variant={syncStatusVariant(item.status)}>{syncStatusLabel(item.status)}</Badge>
                      <div className="text-xs text-muted-foreground">
                        {item.lastSyncedAt ? formatDateTime(item.lastSyncedAt) : "尚未同步"}
                      </div>
                    </div>
                  ))}
                  {!template?.stores.length ? <div className="px-3 py-8 text-center text-sm text-muted-foreground">暂无已托管门店</div> : null}
                </div>
              </section>
            </>
          )}
        </div>
        <DialogFooter className="m-0 shrink-0 rounded-none px-5 py-4">
          <Button type="button" variant="outline" disabled={saving || syncing} onClick={() => onOpenChange(false)}>关闭</Button>
          <Button type="button" disabled={disabled || !valid} onClick={() => void save()}>
            {saving ? <LoaderCircleIcon className="size-4 animate-spin" /> : <SaveIcon className="size-4" />}
            {saving ? "保存中" : "保存并热更新"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
