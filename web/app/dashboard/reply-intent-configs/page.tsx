"use client"

import { useEffect, useMemo, useState } from "react"
import { RefreshCwIcon } from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import {
  createDashboardStatusColumn,
  createDashboardStatusToggleAction,
  DashboardCrudPage,
} from "@/components/dashboard/crud"
import { Badge } from "@/components/ui/badge"
import {
  createReplyIntentConfig,
  deleteReplyIntentConfig,
  fetchReplyIntentConfig,
  fetchReplyIntentConfigs,
  fetchReplyIntentProfiles,
  updateReplyIntentConfig,
  type CreateReplyIntentConfigPayload,
  type ReplyIntentConfig,
  type ReplyIntentProfile,
} from "@/lib/api/admin"
import { getEnumOptions } from "@/lib/enums"
import { Status, StatusLabels } from "@/lib/generated/enums"

const statusOptions = [
  { value: "all", label: "全部" },
  ...getEnumOptions(StatusLabels)
    .filter((item) => Number(item.value) !== Status.Deleted)
    .map((item) => ({ value: String(item.value), label: item.label })),
]

const matchModeOptions = [
  { value: "hybrid", label: "混合识别" },
  { value: "keyword", label: "关键词" },
  { value: "example", label: "样例相似" },
  { value: "llm", label: "模型判断" },
]

const resourceTypeOptions = [
  { value: "", label: "不读取酒店变量" },
  { value: "store_variable", label: "自动判断变量" },
  { value: "mini_program", label: "入住小程序" },
  { value: "location", label: "定位/地址" },
  { value: "phone", label: "门店电话" },
]

const humanRoutePolicyOptions = [
  { value: "", label: "不进入人工路由" },
  { value: "managed_mode", label: "按门店托管模式" },
  { value: "force_hq", label: "总部网页端" },
  { value: "force_store_group", label: "门店群通知" },
]

function trimPreview(value: string, max = 56) {
  const text = value.trim().replace(/\s+/g, " ")
  return text.length > max ? `${text.slice(0, max)}...` : text || "-"
}

export default function ReplyIntentConfigsPage() {
  const { session } = useAuth()
  const permissions = useMemo(
    () => new Set(session?.permissions ?? []),
    [session?.permissions],
  )
  const isPlatformAccount = Boolean(session?.isPlatformAccount)
  const canCreate = isPlatformAccount && permissions.has("aiConfig.create")
  const canUpdate = isPlatformAccount && permissions.has("aiConfig.update")
  const canDelete = isPlatformAccount && permissions.has("aiConfig.delete")

  async function createIntentWithPermission(
    payload: CreateReplyIntentConfigPayload,
  ) {
    if (!canCreate) throw new Error("无权新增回复意图")
    return createReplyIntentConfig(payload)
  }

  async function updateIntentWithPermission(
    item: ReplyIntentConfig,
    payload: CreateReplyIntentConfigPayload,
  ) {
    if (!canUpdate) throw new Error("无权更新回复意图")
    return updateReplyIntentConfig({ id: item.id, ...payload })
  }

  async function deleteIntentWithPermission(item: ReplyIntentConfig) {
    if (!canDelete) throw new Error("无权删除回复意图")
    return deleteReplyIntentConfig(item.id)
  }

  async function updateIntentStatusWithPermission(
    item: ReplyIntentConfig,
    nextStatus: Status,
  ) {
    if (!canUpdate) throw new Error("无权更新回复意图状态")
    return updateReplyIntentConfig({ ...item, status: nextStatus })
  }

  const [profiles, setProfiles] = useState<ReplyIntentProfile[]>([])

  useEffect(() => {
    async function loadProfiles() {
      try {
        const page = await fetchReplyIntentProfiles({ limit: 200 })
        setProfiles(page.results.filter((item) => item.status !== Status.Deleted))
      } catch (error) {
        toast.error(error instanceof Error ? error.message : "加载意图行业失败")
      }
    }
    void loadProfiles()
  }, [])

  const profileOptions = useMemo(
    () => profiles.map((item) => ({ value: String(item.id), label: `${item.name}（${item.code}）` })),
    [profiles],
  )
  const profileOptionsWithAll = [{ value: "all", label: "全部行业" }, ...profileOptions]

  return (
    <DashboardCrudPage<ReplyIntentConfig, CreateReplyIntentConfigPayload>
      filters={[
        { name: "intentProfileId", label: "意图行业", placeholder: "全部", defaultValue: "all", allValue: "all", type: "select", options: profileOptionsWithAll, className: "w-full sm:w-48" },
        { name: "code", label: "意图编码", placeholder: "如 hotel_info", defaultValue: "", trim: true, className: "w-full sm:w-56" },
        { name: "name", label: "意图名称", placeholder: "搜索名称", defaultValue: "", trim: true, className: "w-full sm:w-56" },
        { name: "status", label: "状态", placeholder: "全部", defaultValue: "all", type: "select", options: statusOptions, className: "w-full sm:w-40" },
      ]}
      columns={[
        {
          key: "name",
          label: "意图",
          className: "min-w-56",
          render: (item) => (
            <div className="space-y-1">
              <div className="font-medium text-foreground">{item.name}</div>
              <div className="font-mono text-xs text-muted-foreground">{item.code}</div>
            </div>
          ),
        },
        {
          key: "intentProfileId",
          label: "意图行业",
          render: (item) => {
            const profile = profiles.find((option) => option.id === item.intentProfileId)
            return <Badge variant="outline">{profile ? profile.name : `行业 #${item.intentProfileId}`}</Badge>
          },
        },
        { key: "priority", label: "优先级", render: (item) => item.priority },
        {
          key: "matchMode",
          label: "识别方式",
          render: (item) => <Badge variant="outline">{matchModeOptions.find((option) => option.value === item.matchMode)?.label ?? item.matchMode}</Badge>,
        },
        {
          key: "keywords",
          label: "识别参数",
          className: "max-w-72",
          render: (item) => <span className="text-sm text-muted-foreground">{trimPreview(item.keywords || item.positiveExamples)}</span>,
        },
        {
          key: "runtime",
          label: "运行策略",
          className: "min-w-64",
          render: (item) => (
            <div className="flex flex-wrap gap-1.5 text-xs">
              {item.needsKnowledge ? <Badge variant="secondary">查知识库</Badge> : null}
              {item.needsResource ? <Badge variant="secondary">酒店变量:{item.resourceType || "自动判断"}</Badge> : null}
              {item.needsTool ? <Badge variant="secondary">工具</Badge> : null}
              {item.needsHumanRoute ? <Badge variant="secondary">人工路由</Badge> : null}
              {item.noReplyWhenMatched ? <Badge variant="outline">不主动回复</Badge> : null}
              {!item.needsKnowledge && !item.needsResource && !item.needsTool && !item.needsHumanRoute && !item.noReplyWhenMatched ? "-" : null}
            </div>
          ),
        },
        createDashboardStatusColumn<ReplyIntentConfig, Status>({
          label: "状态",
          getStatus: (item) => item.status as Status,
          getLabel: (status) => statusOptions.find((option) => option.value === String(status))?.label ?? String(status),
        }),
        { key: "sortNo", label: "排序", render: (item) => item.sortNo },
      ]}
      fetchList={fetchReplyIntentConfigs}
      getItemId={(item) => item.id}
      createItem={createIntentWithPermission}
      updateItem={updateIntentWithPermission}
      showCreate={canCreate}
      showEdit={canUpdate}
      deleteItem={canDelete ? deleteIntentWithPermission : undefined}
      showActionsColumn={canUpdate || canDelete}
      form={{
        fetchDetail: fetchReplyIntentConfig,
        fields: [
          { name: "code", label: "意图编码", placeholder: "hotel_info", required: true, trim: true, description: "同一行业内唯一，运行日志和规则引用都看它。" },
          { name: "name", label: "意图名称", placeholder: "酒店信息", required: true, trim: true },
          { name: "description", label: "意图说明", type: "textarea", rows: 3, placeholder: "这个意图负责哪些用户问题，不负责哪些问题。", trim: true },
          { name: "intentProfileId", label: "所属意图行业", type: "select", defaultValue: "", options: profileOptions, valueType: "number", required: true, description: "分类只属于一个行业；租户绑定行业后统一继承，门店和企微账号不能覆盖。" },
          { name: "priority", label: "优先级", type: "number", defaultValue: "100", min: 0, step: 1, required: true, valueType: "number", description: "数字越大越优先，用于解决多个意图同时命中。" },
          { name: "matchMode", label: "识别方式", type: "select", defaultValue: "hybrid", options: matchModeOptions, required: true, valueFromItem: (item) => item.matchMode || "hybrid" },
          { name: "keywords", label: "关键词 / 短语", type: "textarea", rows: 4, placeholder: "每行一个关键词，或用逗号分隔。", trim: true },
          { name: "positiveExamples", label: "正向样例", type: "textarea", rows: 5, placeholder: "用户可能怎么说会命中这个意图。", trim: true },
          { name: "negativeExamples", label: "反例 / 不应命中", type: "textarea", rows: 5, placeholder: "容易误判但不应归到这个意图的说法。", trim: true },
          { name: "requiredContext", label: "需要的上下文", type: "textarea", rows: 3, placeholder: "如：需要最近图片理解；需要门店绑定信息；需要当前账号知识库。", trim: true },
          { name: "needsKnowledge", label: "需要知识库", type: "checkbox", defaultValue: false, description: "命中后进入当前账号绑定知识库检索。" },
          { name: "needsResource", label: "需要酒店变量", type: "checkbox", defaultValue: false, description: "仅用于“酒店变量”大类，命中后读取当前门店配置的小程序、定位、电话等真实变量。" },
          { name: "resourceType", label: "酒店变量类型", type: "select", defaultValue: "", options: resourceTypeOptions, valueFromItem: (item) => item.resourceType || "" },
          { name: "needsTool", label: "需要工具", type: "checkbox", defaultValue: false, description: "命中后允许调用指定工具，不代表模型可自行承诺动作。" },
          { name: "toolCodes", label: "允许工具", type: "textarea", rows: 3, placeholder: "每行一个工具编码，例如 graph/handoff_to_human", trim: true },
          { name: "needsHumanRoute", label: "需要人工路由", type: "checkbox", defaultValue: false, description: "命中后根据托管模式和排班进入接待路由。" },
          { name: "humanRoutePolicy", label: "人工路由策略", type: "select", defaultValue: "", options: humanRoutePolicyOptions, valueFromItem: (item) => item.humanRoutePolicy || "" },
          { name: "promptPack", label: "专项提示词包", type: "textarea", rows: 7, placeholder: "只写这个意图需要披露给模型的规则。", trim: true },
          { name: "replyPlanTemplate", label: "回复计划模板", type: "textarea", rows: 5, placeholder: "回答目标、依据、禁止事项、语气长度。", trim: true },
          { name: "validationRules", label: "发送前校验规则", type: "textarea", rows: 4, placeholder: "如：不能追错房号；不能假承诺；不能复述OCR。", trim: true },
          { name: "noReplyWhenMatched", label: "命中后不主动回复", type: "checkbox", defaultValue: false, description: "谨慎使用；普通媒体不回复已由消息链路门控处理，不需要建成意图分类。" },
          { name: "status", label: "状态", type: "select", defaultValue: String(Status.Ok), valueType: "number", options: statusOptions.filter((item) => item.value !== "all"), required: true, valueFromItem: (item) => String(item.status) },
          { name: "sortNo", label: "排序", type: "number", defaultValue: "0", min: 0, step: 1, valueType: "number" },
          { name: "remark", label: "备注", type: "textarea", rows: 3, trim: true },
        ],
        transformSubmitValues: (values) => ({
          code: String(values.code ?? ""),
          name: String(values.name ?? ""),
          description: String(values.description ?? ""),
          intentProfileId: Number(values.intentProfileId ?? 0),
          priority: Number(values.priority ?? 100),
          matchMode: String(values.matchMode ?? "hybrid"),
          keywords: String(values.keywords ?? ""),
          positiveExamples: String(values.positiveExamples ?? ""),
          negativeExamples: String(values.negativeExamples ?? ""),
          requiredContext: String(values.requiredContext ?? ""),
          needsKnowledge: Boolean(values.needsKnowledge),
          needsResource: Boolean(values.needsResource),
          resourceType: String(values.resourceType ?? ""),
          needsTool: Boolean(values.needsTool),
          toolCodes: String(values.toolCodes ?? ""),
          needsHumanRoute: Boolean(values.needsHumanRoute),
          humanRoutePolicy: String(values.humanRoutePolicy ?? ""),
          promptPack: String(values.promptPack ?? ""),
          replyPlanTemplate: String(values.replyPlanTemplate ?? ""),
          validationRules: String(values.validationRules ?? ""),
          noReplyWhenMatched: Boolean(values.noReplyWhenMatched),
          status: Number(values.status ?? Status.Ok),
          sortNo: Number(values.sortNo ?? 0),
          remark: String(values.remark ?? ""),
        }),
        labels: {
          createTitle: "新增意图分类",
          editTitle: "编辑意图分类",
          create: "新增",
          save: "保存",
          saving: "保存中",
          cancel: "取消",
          loadingDetail: "正在加载意图配置",
          required: "必填",
          invalidNumber: "请输入数字",
          minValue: () => "数值过小",
          maxValue: () => "数值过大",
        },
      }}
      rowActions={
        canUpdate
          ? [
              createDashboardStatusToggleAction<ReplyIntentConfig, Status>({
                icon: <RefreshCwIcon />,
                label: (item) => (item.status === Status.Ok ? "停用" : "启用"),
                getNextStatus: (item) =>
                  item.status === Status.Ok ? Status.Disabled : Status.Ok,
                updateStatus: updateIntentStatusWithPermission,
                successMessage: () => "状态已更新",
                errorMessage: "状态更新失败",
              }),
            ]
          : []
      }
      labels={{
        refresh: "刷新",
        create: "新增意图",
        query: "查询",
        loading: "正在加载意图配置",
        empty: "暂无意图配置",
        actions: "操作",
        edit: "编辑",
        delete: "删除",
        processing: "处理中",
        moreActions: (item) => `更多操作：${item.name}`,
        loadFailed: "加载失败",
        saveFailed: "保存失败",
        deleteFailed: "删除失败",
        created: (payload) => `已新增意图：${payload.name}`,
        updated: (item) => `已更新意图：${item.name}`,
        deleted: (item) => `已删除意图：${item.name}`,
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
