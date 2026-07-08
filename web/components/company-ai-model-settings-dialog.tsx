"use client"

import { toast } from "sonner"

import { OptionCombobox } from "@/components/option-combobox"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import type { AIConfig, StoreAIModelSetting } from "@/lib/api/admin"
import type { AdminCompany } from "@/lib/api/company"
import { Status } from "@/lib/generated/enums"

export function CompanyAIModelSettingsDialog({
  open,
  company,
  settings,
  aiConfigs,
  loading,
  saving,
  canSave,
  onOpenChange,
  onChange,
  onSubmit,
}: {
  open: boolean
  company: AdminCompany | null
  settings: StoreAIModelSetting[]
  aiConfigs: AIConfig[]
  loading: boolean
  saving: boolean
  canSave: boolean
  onOpenChange: (open: boolean) => void
  onChange: (settings: StoreAIModelSetting[]) => void
  onSubmit: () => void
}) {
  function updateSetting(usageCode: string, patch: Partial<StoreAIModelSetting>) {
    onChange(settings.map((item) => (item.usageCode === usageCode ? { ...item, ...patch } : item)))
  }

  function pickGlobalDefault(setting: StoreAIModelSetting) {
    const list = aiConfigs.filter((config) => config.status === Status.Ok && config.modelType === setting.expectedModelType)
    if (setting.usageCode === "intent_detect_llm") {
      return list.find((config) => config.intentDetectEnabled) || list[0]
    }
    return list[0]
  }

  function copyGlobalDefault(setting: StoreAIModelSetting) {
    const config = pickGlobalDefault(setting)
    if (!config) {
      toast.error("没有可复制的全局默认模型配置")
      return
    }
    updateSetting(setting.usageCode, {
      enabled: true,
      provider: config.provider || "openai",
      baseUrl: config.baseUrl || "",
      apiKey: "",
      apiMode: config.apiMode || "chat_completions",
      modelType: setting.expectedModelType,
      modelName: config.modelName || "",
      dimension: config.dimension || 0,
      maxContextTokens: config.maxContextTokens || 0,
      maxOutputTokens: config.maxOutputTokens || 0,
      timeoutMs: config.timeoutMs || 30000,
      maxRetryCount: config.maxRetryCount || 0,
      rpmLimit: config.rpmLimit || 0,
      tpmLimit: config.tpmLimit || 0,
      remark: setting.remark || "",
    })
    toast.success("已复制全局非密钥参数，请填写公司级 API Key")
  }

  function updateNumberSetting(usageCode: string, key: keyof StoreAIModelSetting, value: string) {
    updateSetting(usageCode, { [key]: Number(value || 0) } as Partial<StoreAIModelSetting>)
  }

  function sourceLabel(source: string) {
    if (source === "company_override") return "公司默认"
    if (source === "account_override") return "员工号设置"
    return "系统全局默认"
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[88vh] max-w-5xl overflow-y-auto rounded-3xl p-5">
        <DialogHeader>
          <DialogTitle>公司模型设置</DialogTitle>
          <DialogDescription>
            {company ? `${company.name} 的公司默认模型。实际回复优先级：员工号设置 > 公司默认 > 系统全局默认。` : "公司级默认模型设置"}
          </DialogDescription>
        </DialogHeader>
        {loading ? (
          <div className="rounded-2xl border border-[#dbe7f6] bg-[#f8fbff] p-6 text-sm text-muted-foreground">正在读取公司模型设置...</div>
        ) : (
          <div className="grid gap-3">
            {settings.map((setting) => (
              <div key={setting.usageCode} className="rounded-2xl border border-[#dbe7f6] bg-white p-4 shadow-[0_8px_24px_rgba(35,74,122,0.05)]">
                <div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
                  <div className="min-w-0 flex-1">
                    <div className="font-semibold text-foreground">{setting.usageName}</div>
                    <div className="mt-1 text-xs text-muted-foreground">用途：{setting.usageCode} · 类型：{setting.expectedModelType}</div>
                    <div className="mt-2 text-xs leading-5 text-muted-foreground">
                      当前生效：{setting.effectiveModelName || setting.effectiveAiConfigName || "-"}（{sourceLabel(setting.effectiveModelSource)}）
                      {setting.effectiveBaseUrl ? ` · ${setting.effectiveBaseUrl}` : ""}
                    </div>
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    <Button type="button" variant="outline" size="sm" className="rounded-xl" disabled={!canSave} onClick={() => copyGlobalDefault(setting)}>
                      复制全局默认参数
                    </Button>
                    <label className="flex cursor-pointer items-center gap-2 text-sm text-muted-foreground">
                      <Checkbox
                        checked={setting.enabled}
                        disabled={!canSave}
                        onCheckedChange={(checked) => updateSetting(setting.usageCode, { enabled: checked === true })}
                      />
                      启用公司默认
                    </label>
                  </div>
                </div>
                <div className="mt-4 grid gap-3 md:grid-cols-2">
                  <div className="space-y-1">
                    <div className="text-xs font-medium text-muted-foreground">供应商</div>
                    <Input value={setting.provider || "openai"} disabled={!canSave || !setting.enabled} onChange={(event) => updateSetting(setting.usageCode, { provider: event.target.value })} className="rounded-xl border-[#dbe7f6]" placeholder="openai" />
                  </div>
                  <div className="space-y-1">
                    <div className="text-xs font-medium text-muted-foreground">API 模式</div>
                    <OptionCombobox
                      value={setting.apiMode || "chat_completions"}
                      options={[
                        { value: "chat_completions", label: "Chat Completions" },
                        { value: "responses", label: "Responses API" },
                      ]}
                      placeholder="选择 API 模式"
                      triggerClassName="h-10 rounded-xl border-[#dbe7f6] bg-white"
                      disabled={!canSave || !setting.enabled}
                      onChange={(value) => updateSetting(setting.usageCode, { apiMode: value })}
                    />
                  </div>
                  <div className="space-y-1 md:col-span-2">
                    <div className="text-xs font-medium text-muted-foreground">Base URL</div>
                    <Input value={setting.baseUrl || ""} disabled={!canSave || !setting.enabled} onChange={(event) => updateSetting(setting.usageCode, { baseUrl: event.target.value })} className="rounded-xl border-[#dbe7f6]" placeholder="https://api.openai.com/v1" />
                  </div>
                  <div className="space-y-1">
                    <div className="text-xs font-medium text-muted-foreground">模型名</div>
                    <Input value={setting.modelName || ""} disabled={!canSave || !setting.enabled} onChange={(event) => updateSetting(setting.usageCode, { modelName: event.target.value, modelType: setting.expectedModelType })} className="rounded-xl border-[#dbe7f6]" placeholder="gpt-4.1-mini / qwen-vl-plus" />
                  </div>
                  <div className="space-y-1">
                    <div className="text-xs font-medium text-muted-foreground">API Key</div>
                    <Input type="password" value={setting.apiKey || ""} disabled={!canSave || !setting.enabled} onChange={(event) => updateSetting(setting.usageCode, { apiKey: event.target.value })} className="rounded-xl border-[#dbe7f6]" placeholder={setting.hasApiKey ? "已设置，留空不修改" : "请输入 API Key"} />
                  </div>
                  <div className="grid gap-3 md:col-span-2 md:grid-cols-4">
                    <div className="space-y-1">
                      <div className="text-xs font-medium text-muted-foreground">上下文 Token</div>
                      <Input type="number" value={setting.maxContextTokens || 0} disabled={!canSave || !setting.enabled} onChange={(event) => updateNumberSetting(setting.usageCode, "maxContextTokens", event.target.value)} className="rounded-xl border-[#dbe7f6]" />
                    </div>
                    <div className="space-y-1">
                      <div className="text-xs font-medium text-muted-foreground">输出 Token</div>
                      <Input type="number" value={setting.maxOutputTokens || 0} disabled={!canSave || !setting.enabled} onChange={(event) => updateNumberSetting(setting.usageCode, "maxOutputTokens", event.target.value)} className="rounded-xl border-[#dbe7f6]" />
                    </div>
                    <div className="space-y-1">
                      <div className="text-xs font-medium text-muted-foreground">超时 ms</div>
                      <Input type="number" value={setting.timeoutMs || 30000} disabled={!canSave || !setting.enabled} onChange={(event) => updateNumberSetting(setting.usageCode, "timeoutMs", event.target.value)} className="rounded-xl border-[#dbe7f6]" />
                    </div>
                    <div className="space-y-1">
                      <div className="text-xs font-medium text-muted-foreground">重试次数</div>
                      <Input type="number" value={setting.maxRetryCount || 0} disabled={!canSave || !setting.enabled} onChange={(event) => updateNumberSetting(setting.usageCode, "maxRetryCount", event.target.value)} className="rounded-xl border-[#dbe7f6]" />
                    </div>
                  </div>
                  <div className="grid gap-3 md:col-span-2 md:grid-cols-3">
                    <div className="space-y-1">
                      <div className="text-xs font-medium text-muted-foreground">向量维度</div>
                      <Input type="number" value={setting.dimension || 0} disabled={!canSave || !setting.enabled || setting.expectedModelType !== "embedding"} onChange={(event) => updateNumberSetting(setting.usageCode, "dimension", event.target.value)} className="rounded-xl border-[#dbe7f6]" />
                    </div>
                    <div className="space-y-1">
                      <div className="text-xs font-medium text-muted-foreground">RPM 限制</div>
                      <Input type="number" value={setting.rpmLimit || 0} disabled={!canSave || !setting.enabled} onChange={(event) => updateNumberSetting(setting.usageCode, "rpmLimit", event.target.value)} className="rounded-xl border-[#dbe7f6]" />
                    </div>
                    <div className="space-y-1">
                      <div className="text-xs font-medium text-muted-foreground">TPM 限制</div>
                      <Input type="number" value={setting.tpmLimit || 0} disabled={!canSave || !setting.enabled} onChange={(event) => updateNumberSetting(setting.usageCode, "tpmLimit", event.target.value)} className="rounded-xl border-[#dbe7f6]" />
                    </div>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
        <DialogFooter>
          <Button type="button" variant="outline" className="rounded-xl" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button type="button" className="rounded-xl" disabled={!canSave || saving || loading || !company} onClick={onSubmit}>
            {saving ? "保存中..." : "保存公司设置"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
