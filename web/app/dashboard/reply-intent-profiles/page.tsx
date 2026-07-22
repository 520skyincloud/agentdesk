"use client"

import { useMemo } from "react"
import { RefreshCwIcon } from "lucide-react"

import { useAuth } from "@/components/auth-provider"
import {
  createDashboardStatusColumn,
  createDashboardStatusToggleAction,
  DashboardCrudPage,
} from "@/components/dashboard/crud"
import { Badge } from "@/components/ui/badge"
import {
  createReplyIntentProfile,
  deleteReplyIntentProfile,
  fetchReplyIntentProfile,
  fetchReplyIntentProfiles,
  updateReplyIntentProfile,
  type CreateReplyIntentProfilePayload,
  type ReplyIntentProfile,
} from "@/lib/api/admin"
import { getEnumOptions } from "@/lib/enums"
import { Status, StatusLabels } from "@/lib/generated/enums"
import { formatDateTime } from "@/lib/utils"

const statusOptions = [
  { value: "all", label: "全部" },
  ...getEnumOptions(StatusLabels)
    .filter((item) => Number(item.value) !== Status.Deleted)
    .map((item) => ({ value: String(item.value), label: item.label })),
]

function trimPreview(value: string, max = 80) {
  const text = value.trim().replace(/\s+/g, " ")
  return text.length > max ? `${text.slice(0, max)}...` : text || "-"
}

export default function ReplyIntentProfilesPage() {
  const { session } = useAuth()
  const permissions = useMemo(
    () => new Set(session?.permissions ?? []),
    [session?.permissions],
  )
  const isPlatformAccount = Boolean(session?.isPlatformAccount)
  const canCreate = isPlatformAccount && permissions.has("aiConfig.create")
  const canUpdate = isPlatformAccount && permissions.has("aiConfig.update")
  const canDelete = isPlatformAccount && permissions.has("aiConfig.delete")

  return (
    <DashboardCrudPage<ReplyIntentProfile, CreateReplyIntentProfilePayload>
      filters={[
        { name: "code", label: "行业编码", placeholder: "hotel", defaultValue: "", trim: true, className: "w-full sm:w-48" },
        { name: "name", label: "名称", placeholder: "酒店行业", defaultValue: "", trim: true, className: "w-full sm:w-56" },
        { name: "industryCode", label: "业务行业", placeholder: "hotel", defaultValue: "", trim: true, className: "w-full sm:w-44" },
        { name: "status", label: "状态", placeholder: "全部", defaultValue: "all", allValue: "all", type: "select", options: statusOptions, className: "w-full sm:w-36" },
      ]}
      columns={[
        {
          key: "name",
          label: "意图行业",
          className: "min-w-56",
          render: (item) => (
            <div className="space-y-1">
              <div className="font-medium text-foreground">{item.name}</div>
              <div className="font-mono text-xs text-muted-foreground">{item.code}</div>
            </div>
          ),
        },
        {
          key: "industryCode",
          label: "业务行业",
          render: (item) => <Badge variant="outline">{item.industryCode || "-"}</Badge>,
        },
        {
          key: "description",
          label: "说明",
          className: "max-w-80",
          render: (item) => <span className="text-sm text-muted-foreground">{trimPreview(item.description)}</span>,
        },
        {
          key: "intentDetectPrompt",
          label: "IntentDetect 提示词",
          className: "max-w-96",
          render: (item) => <span className="text-sm text-muted-foreground">{trimPreview(item.intentDetectPrompt)}</span>,
        },
        {
          key: "revision",
          label: "发布版本",
          className: "min-w-36",
          render: (item) => (
            <div className="space-y-1">
              <Badge variant="outline">Revision {item.revision}</Badge>
              <div className="text-xs text-muted-foreground">
                {item.publishedAt ? formatDateTime(item.publishedAt) : "尚未发布"}
              </div>
            </div>
          ),
        },
        createDashboardStatusColumn<ReplyIntentProfile, Status>({
          label: "状态",
          getStatus: (item) => item.status as Status,
          getLabel: (status) => statusOptions.find((option) => option.value === String(status))?.label ?? String(status),
        }),
        { key: "sortNo", label: "排序", render: (item) => item.sortNo },
      ]}
      fetchList={fetchReplyIntentProfiles}
      getItemId={(item) => item.id}
      createItem={createReplyIntentProfile}
      updateItem={(item, payload) => updateReplyIntentProfile({ id: item.id, ...payload })}
      deleteItem={canDelete ? (item) => deleteReplyIntentProfile(item.id) : undefined}
      showCreate={canCreate}
      showEdit={canUpdate}
      showActionsColumn={canUpdate || canDelete}
      form={{
        fetchDetail: fetchReplyIntentProfile,
        fields: [
          { name: "code", label: "行业配置编码", placeholder: "hotel", required: true, trim: true, description: "稳定编码；租户通过这个配置决定 IntentDetect 总提示词、分类和固定标签目录。" },
          { name: "name", label: "名称", placeholder: "酒店行业", required: true, trim: true },
          { name: "industryCode", label: "业务行业编码", placeholder: "hotel / retail / education", required: true, trim: true, description: "用于标识业务行业，不直接参与模型判断；真正参与模型的是下面的提示词和 schema。" },
          { name: "description", label: "说明", type: "textarea", rows: 3, placeholder: "这个行业的客服场景、适用范围和边界。", trim: true },
          {
            name: "intentDetectPrompt",
            label: "IntentDetect 总提示词",
            type: "textarea",
            rows: 18,
            placeholder: "写这个行业的意图识别规则。这里只负责分类，不回复客户。",
            trim: true,
            description: "运行时真实传给意图识别模型的 system prompt 主体。不要写最终回复话术，不要写固定短答。",
          },
          {
            name: "intentJsonSchema",
            label: "IntentDetect JSON Schema",
            type: "textarea",
            rows: 14,
            placeholder: "写模型必须输出的 JSON 字段、枚举、约束。",
            trim: true,
            description: "运行时会追加到总提示词后。字段设计要和代码解析结构兼容。",
          },
          { name: "status", label: "状态", type: "select", defaultValue: String(Status.Disabled), valueType: "number", options: statusOptions.filter((item) => item.value !== "all"), required: true, valueFromItem: (item) => String(item.status), description: "新行业先保存为停用草稿；意图分类和固定标签目录完整后才能发布。" },
          { name: "sortNo", label: "排序", type: "number", defaultValue: "0", min: 0, step: 1, valueType: "number" },
          { name: "remark", label: "备注", type: "textarea", rows: 3, trim: true },
        ],
        transformSubmitValues: (values) => ({
          code: String(values.code ?? ""),
          name: String(values.name ?? ""),
          industryCode: String(values.industryCode ?? ""),
          description: String(values.description ?? ""),
          intentDetectPrompt: String(values.intentDetectPrompt ?? ""),
          intentJsonSchema: String(values.intentJsonSchema ?? ""),
          status: Number(values.status ?? Status.Ok),
          sortNo: Number(values.sortNo ?? 0),
          remark: String(values.remark ?? ""),
        }),
        labels: {
          createTitle: "新增意图行业",
          editTitle: "编辑意图行业",
          create: "新增",
          save: "保存",
          saving: "保存中",
          cancel: "取消",
          loadingDetail: "正在加载意图行业",
          required: "必填",
          invalidNumber: "请输入数字",
          minValue: () => "数值过小",
          maxValue: () => "数值过大",
        },
      }}
      rowActions={
        canUpdate
          ? [
              createDashboardStatusToggleAction<ReplyIntentProfile, Status>({
                icon: <RefreshCwIcon />,
                label: (item) => (item.status === Status.Ok ? "停用" : "发布"),
                getNextStatus: (item) => (item.status === Status.Ok ? Status.Disabled : Status.Ok),
                updateStatus: (item, nextStatus) => updateReplyIntentProfile({ ...item, status: nextStatus }),
                successMessage: () => "状态已更新",
                errorMessage: "状态更新失败",
              }),
            ]
          : []
      }
      labels={{
        refresh: "刷新",
        create: "新增行业",
        query: "查询",
        loading: "正在加载意图行业",
        empty: "暂无意图行业",
        actions: "操作",
        edit: "编辑",
        delete: "删除",
        processing: "处理中",
        moreActions: (item) => `更多操作：${item.name}`,
        loadFailed: "加载失败",
        saveFailed: "保存失败",
        deleteFailed: "删除失败",
        created: (payload) => `已新增意图行业：${payload.name}`,
        updated: (item) => `已更新意图行业：${item.name}`,
        deleted: (item) => `已删除意图行业：${item.name}`,
      }}
      deleteConfirm={(item) => ({
        title: `删除 ${item.name}`,
        description: "删除后不可恢复，确认继续？",
        confirmText: "确认删除",
        cancelText: "取消",
        variant: "destructive",
      })}
    />
  )
}
