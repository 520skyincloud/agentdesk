"use client"

import { useMemo } from "react"
import { FlaskConicalIcon, UploadIcon } from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import {
  createDashboardStatusColumn,
  DashboardCrudPage,
} from "@/components/dashboard/crud"
import { Badge } from "@/components/ui/badge"
import {
  createReplyIntentProfile,
  deleteReplyIntentProfile,
  fetchReplyIntentProfile,
  fetchReplyIntentProfiles,
  publishReplyIntentProfile,
  testReplyIntentProfile,
  updateReplyIntentProfile,
  type CreateReplyIntentProfilePayload,
  type ReplyIntentProfile,
} from "@/lib/api/admin"
import { Status } from "@/lib/generated/enums"
import { formatDateTime } from "@/lib/utils"

const statusOptions = [
  { value: "all", label: "全部" },
  { value: String(Status.Ok), label: "已发布" },
  { value: String(Status.Disabled), label: "草稿" },
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
  const canManage = isPlatformAccount && permissions.has("aiConfig.update")

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
      deleteItem={canManage ? (item) => deleteReplyIntentProfile(item.id) : undefined}
      showCreate={canManage}
      showEdit={canManage}
      showActionsColumn={canManage}
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
          { name: "sortNo", label: "排序", type: "number", defaultValue: "0", min: 0, step: 1, valueType: "number" },
          { name: "remark", label: "备注", type: "textarea", rows: 3, trim: true },
        ],
        transformSubmitValues: (values, context) => ({
          code: String(values.code ?? ""),
          name: String(values.name ?? ""),
          industryCode: String(values.industryCode ?? ""),
          description: String(values.description ?? ""),
          intentDetectPrompt: String(values.intentDetectPrompt ?? ""),
          intentJsonSchema: String(values.intentJsonSchema ?? ""),
          status: context.item?.status ?? Status.Disabled,
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
        canManage
          ? [
              {
                key: "test",
                icon: <FlaskConicalIcon />,
                label: "测试当前版本",
                run: async ({ item }) => {
                  const result = await testReplyIntentProfile(item.id)
                  if (!result.valid) {
                    toast.error(result.errors.join("；") || "行业测试未通过")
                    return
                  }
                  toast.success(
                    `Revision ${result.revision} 测试通过：${result.activeIntentCount} 个意图，${result.tagCategoryCount} 类 ${result.tagCount} 个标签`,
                  )
                  if (result.warnings.length > 0) {
                    toast.warning(result.warnings.join("；"))
                  }
                },
              },
              {
                key: "publish",
                icon: <UploadIcon />,
                label: "发布当前版本",
                visible: (item) => item.status === Status.Disabled,
                confirm: (item) => ({
                  title: `发布 ${item.name}`,
                  description: `确认发布 Revision ${item.revision}？发布前会重新执行完整结构校验。`,
                  confirmText: "确认发布",
                  cancelText: "取消",
                }),
                run: async ({ item, reload }) => {
                  await publishReplyIntentProfile(item.id, item.revision)
                  toast.success(`Revision ${item.revision} 已发布`)
                  await reload()
                },
              },
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
