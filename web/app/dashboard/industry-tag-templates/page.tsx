"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import { TagsIcon } from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import {
  createDashboardStatusColumn,
  DashboardCrudPage,
} from "@/components/dashboard/crud"
import { DashboardPage } from "@/components/dashboard-page"
import { OptionCombobox } from "@/components/option-combobox"
import { Badge } from "@/components/ui/badge"
import {
  createIndustryTagDefinition,
  fetchIndustryTagDefinition,
  fetchIndustryTagDefinitionPage,
  fetchIndustryTagDefinitions,
  fetchReplyIntentProfiles,
  updateIndustryTagDefinition,
  type CreateIndustryTagDefinitionPayload,
  type IndustryTagDefinition,
  type ReplyIntentProfile,
} from "@/lib/api/admin"
import { Status } from "@/lib/generated/enums"

const statusOptions = [
  { value: "all", label: "全部" },
  { value: String(Status.Ok), label: "启用" },
  { value: String(Status.Disabled), label: "停用" },
]

export default function IndustryTagTemplatesPage() {
  const { session } = useAuth()
  const permissions = useMemo(
    () => new Set(session?.permissions ?? []),
    [session?.permissions],
  )
  const canManage = Boolean(session?.isPlatformAccount) && permissions.has("aiConfig.update")
  const [profiles, setProfiles] = useState<ReplyIntentProfile[]>([])
  const [profileID, setProfileID] = useState(0)
  const [catalog, setCatalog] = useState<IndustryTagDefinition[]>([])
  const [loadingProfiles, setLoadingProfiles] = useState(true)

  useEffect(() => {
    let active = true
    void fetchReplyIntentProfiles({ page: 1, limit: 100 })
      .then((result) => {
        if (!active) return
        const items = result.results ?? []
        setProfiles(items)
        setProfileID((current) => current || items[0]?.id || 0)
      })
      .catch((error) => {
        if (active) toast.error(error instanceof Error ? error.message : "加载行业失败")
      })
      .finally(() => {
        if (active) setLoadingProfiles(false)
      })
    return () => {
      active = false
    }
  }, [])

  const loadPage = useCallback(
    async (query: Record<string, string | number | undefined>) => {
      const [page, all] = await Promise.all([
        fetchIndustryTagDefinitionPage({ ...query, intentProfileId: profileID }),
        fetchIndustryTagDefinitions(profileID),
      ])
      setCatalog(all)
      return page
    },
    [profileID],
  )

  const parentNames = useMemo(
    () => new Map(catalog.map((item) => [item.id, item.name])),
    [catalog],
  )
  const profileOptions = useMemo(
    () => profiles.map((item) => ({
      value: String(item.id),
      label: `${item.name} · Revision ${item.revision}`,
    })),
    [profiles],
  )

  if (!profileID) {
    return (
      <DashboardPage>
        <div className="flex min-h-48 items-center justify-center text-sm text-muted-foreground">
          {loadingProfiles ? "正在加载行业" : "暂无行业 Profile"}
        </div>
      </DashboardPage>
    )
  }

  return (
    <DashboardCrudPage<IndustryTagDefinition, CreateIndustryTagDefinitionPayload>
      key={profileID}
      filters={[
        { name: "name", label: "名称", placeholder: "标签名称", defaultValue: "", trim: true, className: "w-full sm:w-56" },
        { name: "status", label: "状态", placeholder: "全部", defaultValue: "all", allValue: "all", type: "select", options: statusOptions, className: "w-full sm:w-36" },
      ]}
      columns={[
        {
          key: "name",
          label: "模板",
          className: "min-w-56",
          render: (item) => (
            <div className="space-y-1">
              <div className="flex items-center gap-2 font-medium">
                <TagsIcon className="size-4 text-muted-foreground" />
                {item.name}
              </div>
              <code className="text-xs text-muted-foreground">{item.semanticKey}</code>
            </div>
          ),
        },
        {
          key: "parentId",
          label: "所属分类",
          render: (item) => item.parentId === 0
            ? <Badge variant="secondary">一级分类</Badge>
            : parentNames.get(item.parentId) ?? `#${item.parentId}`,
        },
        {
          key: "behavior",
          label: "使用范围",
          className: "min-w-52",
          render: (item) => (
            <div className="flex flex-wrap gap-1.5">
              {item.aiEnabled ? <Badge variant="outline">AI 识别</Badge> : null}
              {item.replyEnabled ? <Badge variant="outline">回复上下文</Badge> : null}
              {item.applicableScene ? <Badge variant="secondary">{item.applicableScene}</Badge> : null}
              {!item.aiEnabled && !item.replyEnabled && !item.applicableScene ? "-" : null}
            </div>
          ),
        },
        {
          key: "aliases",
          label: "标准别名",
          className: "max-w-72",
          render: (item) => item.aliases || "-",
        },
        {
          key: "definitionRevision",
          label: "Revision",
          render: (item) => item.definitionRevision,
        },
        createDashboardStatusColumn<IndustryTagDefinition, Status>({
          label: "状态",
          getStatus: (item) => item.status as Status,
          getLabel: (status) => status === Status.Ok ? "启用" : "停用",
        }),
        { key: "sortNo", label: "排序", render: (item) => item.sortNo },
      ]}
      fetchList={loadPage}
      getItemId={(item) => item.id}
      createItem={createIndustryTagDefinition}
      updateItem={(item, payload) => updateIndustryTagDefinition({ id: item.id, ...payload })}
      showCreate={canManage}
      showEdit={canManage}
      showActionsColumn={canManage}
      renderToolbarActions={() => (
        <div className="w-full sm:w-72">
          <OptionCombobox
            value={String(profileID)}
            options={profileOptions}
            placeholder="选择行业 Profile"
            searchPlaceholder="搜索行业"
            emptyText="暂无行业"
            onChange={(value) => setProfileID(Number(value ?? 0))}
          />
        </div>
      )}
      form={{
        fetchDetail: fetchIndustryTagDefinition,
        fields: [
          {
            name: "parentId",
            label: "所属分类",
            type: "select",
            valueType: "number",
            defaultValue: "0",
            required: true,
            loadOptions: async () => [
              { value: "0", label: "一级分类" },
              ...(await fetchIndustryTagDefinitions(profileID))
                .filter((item) => item.parentId === 0 && item.status !== Status.Deleted)
                .map((item) => ({ value: String(item.id), label: item.name })),
            ],
          },
          { name: "name", label: "名称", placeholder: "喜静", required: true, trim: true },
          {
            name: "semanticKey",
            label: "SemanticKey",
            placeholder: "room.quiet",
            required: true,
            trim: true,
            description: "创建后不可修改。",
          },
          { name: "aliases", label: "标准别名", type: "textarea", rows: 3, placeholder: "安静,怕吵,睡眠浅", trim: true },
          { name: "conflictGroup", label: "互斥组", placeholder: "room.smoking", trim: true },
          { name: "applicableScene", label: "适用场景", placeholder: "room_assignment", trim: true },
          { name: "aiEnabled", label: "允许 AI 识别", type: "switch", defaultValue: true, valueType: "boolean" },
          { name: "replyEnabled", label: "允许进入回复上下文", type: "switch", defaultValue: false, valueType: "boolean" },
          { name: "status", label: "状态", type: "select", defaultValue: String(Status.Ok), valueType: "number", options: statusOptions.filter((item) => item.value !== "all"), required: true, valueFromItem: (item) => String(item.status) },
          { name: "sortNo", label: "排序", type: "number", defaultValue: "0", min: 0, step: 1, valueType: "number" },
        ],
        transformSubmitValues: (values) => ({
          intentProfileId: profileID,
          parentId: Number(values.parentId ?? 0),
          name: String(values.name ?? ""),
          semanticKey: String(values.semanticKey ?? ""),
          aliases: String(values.aliases ?? ""),
          conflictGroup: String(values.conflictGroup ?? ""),
          applicableScene: String(values.applicableScene ?? ""),
          aiEnabled: Boolean(values.aiEnabled),
          replyEnabled: Boolean(values.replyEnabled),
          status: Number(values.status ?? Status.Ok),
          sortNo: Number(values.sortNo ?? 0),
        }),
        labels: {
          createTitle: "新增行业标签模板",
          editTitle: "编辑行业标签模板",
          create: "新增",
          save: "保存",
          saving: "保存中",
          cancel: "取消",
          loadingDetail: "正在加载标签模板",
          required: "必填",
          invalidNumber: "请输入数字",
          minValue: () => "数值过小",
          maxValue: () => "数值过大",
        },
      }}
      labels={{
        refresh: "刷新",
        create: "新增模板",
        query: "查询",
        loading: "正在加载行业标签",
        empty: "当前行业暂无标签模板",
        actions: "操作",
        edit: "编辑",
        delete: "删除",
        processing: "处理中",
        moreActions: (item) => `更多操作：${item.name}`,
        loadFailed: "加载行业标签失败",
        saveFailed: "保存行业标签失败",
        deleteFailed: "行业标签不支持物理删除",
        created: (payload) => `已新增模板：${payload.name}`,
        updated: (item) => `已更新模板：${item.name}`,
      }}
    />
  )
}
