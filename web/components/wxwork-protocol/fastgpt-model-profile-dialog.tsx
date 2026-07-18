"use client"

import { useEffect, useMemo, useState } from "react"
import { CheckCircle2Icon, FlaskConicalIcon, LoaderCircleIcon, ShieldCheckIcon } from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import {
  fetchFastGPTModelProfile,
  testFastGPTModelProfile,
  updateFastGPTModelProfile,
  type FastGPTModelCredential,
  type FastGPTModelProfile,
  type FastGPTModelProfilePayload,
  type FastGPTModelProfileTestResult,
  type WxWorkProtocolInstance,
} from "@/lib/api/admin"
import { repairMojibakeText } from "@/lib/utils"

type CredentialDraft = FastGPTModelCredential & { apiKey: string }

type ProfileDraft = {
  id: string
  name: string
  revision: number
  embedding: CredentialDraft
  documentParser: CredentialDraft
  vision: CredentialDraft
  rerankEnabled: boolean
  rerank: CredentialDraft
}

type Props = {
  open: boolean
  instance: WxWorkProtocolInstance | null
  canSave: boolean
  onOpenChange: (open: boolean) => void
  onSaved?: () => void
}

const emptyCredential = (): CredentialDraft => ({
  provider: "openai",
  baseUrl: "",
  model: "",
  apiKey: "",
  keyConfigured: false,
  keyFingerprint: "",
})

function toCredentialDraft(value?: FastGPTModelCredential | null): CredentialDraft {
  return { ...emptyCredential(), ...(value || {}), apiKey: "" }
}

function createDraft(instance: WxWorkProtocolInstance | null, profile?: FastGPTModelProfile | null): ProfileDraft {
  return {
    id: profile?.id || "",
    name: profile?.name || `${repairMojibakeText(instance?.employeeName || instance?.storeName || "门店")} 知识库模型`,
    revision: profile?.revision || 0,
    embedding: toCredentialDraft(profile?.embedding),
    documentParser: toCredentialDraft(profile?.documentParser),
    vision: toCredentialDraft(profile?.vision),
    rerankEnabled: Boolean(profile?.rerank),
    rerank: toCredentialDraft(profile?.rerank),
  }
}

function credentialIsComplete(value: CredentialDraft) {
  return Boolean(value.provider.trim() && value.baseUrl.trim() && value.model.trim() && (value.apiKey.trim() || value.keyConfigured))
}

function CredentialEditor({
  title,
  description,
  value,
  disabled,
  onChange,
}: {
  title: string
  description: string
  value: CredentialDraft
  disabled: boolean
  onChange: (next: CredentialDraft) => void
}) {
  const update = (field: keyof CredentialDraft, next: string) => onChange({ ...value, [field]: next })
  return (
    <section className="border-t border-border py-4 first:border-t-0 first:pt-0">
      <div className="mb-3 flex items-start justify-between gap-3">
        <div>
          <h3 className="text-sm font-semibold text-foreground">{title}</h3>
          <p className="mt-1 text-xs leading-5 text-muted-foreground">{description}</p>
        </div>
        {value.keyConfigured ? <Badge variant="outline">密钥已配置 · {value.keyFingerprint || "已加密"}</Badge> : null}
      </div>
      <div className="grid gap-3 md:grid-cols-2">
        <div className="space-y-1.5">
          <Label>Provider</Label>
          <Input value={value.provider} disabled={disabled} placeholder="openai" onChange={(event) => update("provider", event.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label>模型名</Label>
          <Input value={value.model} disabled={disabled} placeholder="请输入供应商真实模型名" onChange={(event) => update("model", event.target.value)} />
        </div>
        <div className="space-y-1.5 md:col-span-2">
          <Label>Base URL</Label>
          <Input value={value.baseUrl} disabled={disabled} placeholder="https://api.example.com/v1" onChange={(event) => update("baseUrl", event.target.value)} />
        </div>
        <div className="space-y-1.5 md:col-span-2">
          <Label>API Key</Label>
          <Input
            type="password"
            value={value.apiKey}
            disabled={disabled}
            autoComplete="new-password"
            placeholder={value.keyConfigured ? "留空沿用当前加密密钥；输入新值则替换" : "请输入 API Key"}
            onChange={(event) => update("apiKey", event.target.value)}
          />
        </div>
      </div>
    </section>
  )
}

export function FastGPTModelProfileDialog({ open, instance, canSave, onOpenChange, onSaved }: Props) {
  const [draft, setDraft] = useState<ProfileDraft>(() => createDraft(instance))
  const [loading, setLoading] = useState(false)
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [testResult, setTestResult] = useState<FastGPTModelProfileTestResult | null>(null)

  useEffect(() => {
    if (!open || !instance) return
    let cancelled = false
    setLoading(true)
    setTestResult(null)
    fetchFastGPTModelProfile(instance.id)
      .then((profile) => {
        if (!cancelled) setDraft(createDraft(instance, profile))
      })
      .catch((error) => {
        if (!cancelled) toast.error(error instanceof Error ? error.message : "读取 FastGPT 模型 Profile 失败")
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [instance, open])

  const valid = useMemo(() => {
    return Boolean(
      draft.name.trim() &&
        credentialIsComplete(draft.embedding) &&
        credentialIsComplete(draft.documentParser) &&
        credentialIsComplete(draft.vision) &&
        (!draft.rerankEnabled || credentialIsComplete(draft.rerank)),
    )
  }, [draft])

  function mutate(mutator: (current: ProfileDraft) => ProfileDraft) {
    setDraft((current) => mutator(current))
    setTestResult(null)
  }

  function buildPayload(testToken = ""): FastGPTModelProfilePayload {
    return {
      wxWorkInstanceId: instance?.id || 0,
      profileId: draft.id,
      name: draft.name.trim(),
      embedding: draft.embedding,
      documentParser: draft.documentParser,
      vision: draft.vision,
      rerankEnabled: draft.rerankEnabled,
      rerank: draft.rerankEnabled ? draft.rerank : null,
      testToken,
    }
  }

  async function handleTest() {
    if (!valid) {
      toast.error("请先完整填写启用阶段的地址、模型和密钥")
      return
    }
    setTesting(true)
    try {
      const result = await testFastGPTModelProfile(buildPayload())
      setTestResult(result)
      toast.success("所有启用阶段均已通过真实调用")
    } catch (error) {
      setTestResult(null)
      toast.error(error instanceof Error ? error.message : "真实模型测试失败")
    } finally {
      setTesting(false)
    }
  }

  async function handleSave() {
    if (!testResult?.testToken) {
      toast.error("当前配置必须先通过真实测试")
      return
    }
    setSaving(true)
    try {
      const result = await updateFastGPTModelProfile(buildPayload(testResult.testToken))
      setDraft(createDraft(instance, result.profile))
      setTestResult(null)
      toast.success(`已保存并绑定到 ${result.boundDatasetCount} 个门店知识库`)
      onSaved?.()
      onOpenChange(false)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存 FastGPT 模型 Profile 失败")
    } finally {
      setSaving(false)
    }
  }

  const disabled = loading || testing || saving || !canSave
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] max-w-3xl overflow-hidden p-0">
        <DialogHeader className="border-b px-5 py-4">
          <DialogTitle>FastGPT 知识库模型设置</DialogTitle>
          <DialogDescription>
            {repairMojibakeText(instance?.employeeName || "当前员工号")} 所属门店的知识解析、向量化和视觉模型。此处密钥由 FastGPT 加密保存，不影响 Agent Desk 回复生成模型。
          </DialogDescription>
        </DialogHeader>
        <div className="overflow-y-auto px-5 py-4">
          {loading ? (
            <div className="flex min-h-48 items-center justify-center gap-2 text-muted-foreground">
              <LoaderCircleIcon className="size-4 animate-spin" /> 正在读取门店 Profile
            </div>
          ) : (
            <>
              <div className="mb-4 flex items-start gap-3 rounded-lg border border-blue-200 bg-blue-50 p-3 text-sm text-blue-950">
                <ShieldCheckIcon className="mt-0.5 size-4 shrink-0" />
                <div className="leading-6">门店员工只能看到 Profile 名称、版本和状态。密钥不会返回前端；已配置密钥留空即可沿用。</div>
              </div>
              <div className="mb-4 grid gap-3 md:grid-cols-[1fr_auto] md:items-end">
                <div className="space-y-1.5">
                  <Label>Profile 名称</Label>
                  <Input value={draft.name} disabled={disabled} onChange={(event) => mutate((current) => ({ ...current, name: event.target.value }))} />
                </div>
                <div className="flex h-8 items-center gap-2 text-xs text-muted-foreground">
                  <Badge variant="secondary">版本 {draft.revision || "未创建"}</Badge>
                  <Badge variant="outline">{draft.id ? "已绑定" : "待创建"}</Badge>
                </div>
              </div>
              <CredentialEditor title="向量模型" description="文件索引和客户问题检索使用的 Embedding 模型。" value={draft.embedding} disabled={disabled} onChange={(value) => mutate((current) => ({ ...current, embedding: value }))} />
              <CredentialEditor title="文档理解模型" description="文档解析和问答拆分使用的文本模型。" value={draft.documentParser} disabled={disabled} onChange={(value) => mutate((current) => ({ ...current, documentParser: value }))} />
              <CredentialEditor title="视觉理解模型" description="图片知识解析使用的多模态模型；测试会真实发送一张内置图片。" value={draft.vision} disabled={disabled} onChange={(value) => mutate((current) => ({ ...current, vision: value }))} />
              <div className="flex items-center justify-between border-t border-border py-4">
                <div>
                  <h3 className="text-sm font-semibold">重排模型</h3>
                  <p className="mt-1 text-xs text-muted-foreground">可选。启用后用于提高多条召回结果的排序质量。</p>
                </div>
                <Switch checked={draft.rerankEnabled} disabled={disabled} onCheckedChange={(checked) => mutate((current) => ({ ...current, rerankEnabled: checked }))} />
              </div>
              {draft.rerankEnabled ? <CredentialEditor title="重排模型配置" description="使用 OpenAI 兼容的 rerank 请求格式。" value={draft.rerank} disabled={disabled} onChange={(value) => mutate((current) => ({ ...current, rerank: value }))} /> : null}
              {testResult ? (
                <div className="mt-2 flex flex-wrap items-center gap-2 rounded-lg border border-emerald-200 bg-emerald-50 p-3 text-sm text-emerald-950">
                  <CheckCircle2Icon className="size-4" />
                  {testResult.results.map((item) => <Badge key={item.stage} variant="outline">{item.stage} 已通过</Badge>)}
                  <span className="text-xs">测试凭证 10 分钟内有效</span>
                </div>
              ) : null}
            </>
          )}
        </div>
        <DialogFooter className="m-0">
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={testing || saving}>取消</Button>
          <Button type="button" variant="outline" onClick={() => void handleTest()} disabled={disabled || !valid}>
            {testing ? <LoaderCircleIcon className="size-4 animate-spin" /> : <FlaskConicalIcon className="size-4" />}
            {testing ? "真实测试中" : "测试全部模型"}
          </Button>
          <Button type="button" onClick={() => void handleSave()} disabled={disabled || !testResult?.testToken}>保存并绑定</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
