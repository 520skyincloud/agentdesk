"use client"

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react"
import {
  BedDoubleIcon, CheckCircle2Icon, ClipboardListIcon, DatabaseIcon,
  EyeIcon, LinkIcon, PencilIcon, PlusIcon, RefreshCwIcon, RotateCcwIcon, Settings2Icon,
  ShieldCheckIcon, UsersIcon, XIcon,
} from "lucide-react"
import { toast } from "sonner"

import { useAuth } from "@/components/auth-provider"
import { DashboardCrudFormDialog } from "@/components/dashboard/crud"
import { OptionCombobox } from "@/components/option-combobox"
import { ProjectDialog } from "@/components/project-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  bindSandboxCustomer, cancelSandboxOperation, fetchSandboxCustomers, fetchSandboxStores,
  fetchSandboxWorkspace, initializeSandbox, resetSandbox, saveSandbox,
  type SandboxBinding, type SandboxCustomerOption, type SandboxOperation, type SandboxOrder,
  type SandboxSave, type SandboxWorkspace,
} from "@/lib/api/pms-sandbox"
import { formatDateTime } from "@/lib/utils"
import { SandboxEdit, type EditItem, type EditKind, type EditState } from "./_components/edit"

const money = (cents: number) => `¥${(cents / 100).toFixed(2)}`
const label = (data: SandboxWorkspace, kind: string, value: string) =>
  data.options[kind]?.find(x => x.value === value)?.label ?? value

function IconAction({ title, icon, onClick, disabled }: { title: string; icon: ReactNode; onClick: () => void; disabled?: boolean }) {
  return <Tooltip><TooltipTrigger render={<Button size="icon-sm" variant="ghost" aria-label={title} disabled={disabled} onClick={onClick} />}>{icon}</TooltipTrigger><TooltipContent>{title}</TooltipContent></Tooltip>
}
function DataTable({ columns, rows, empty = "暂无记录" }: { columns: string[]; rows: { id: number; cells: ReactNode[] }[]; empty?: string }) {
  return <div className="min-w-0 overflow-x-auto border-y">
    <Table><TableHeader><TableRow>{columns.map((col, i) => <TableHead className="whitespace-nowrap" key={`${col}-${i}`}>{col}</TableHead>)}</TableRow></TableHeader>
      <TableBody>{rows.length ? rows.map(row => <TableRow key={row.id}>{row.cells.map((cell, index) => <TableCell className="align-top text-sm" key={index}>{cell}</TableCell>)}</TableRow>) :
        <TableRow><TableCell colSpan={columns.length} className="h-24 text-center text-muted-foreground">{empty}</TableCell></TableRow>}</TableBody>
    </Table>
  </div>
}
function Section({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
  return <section className="min-w-0 space-y-3"><div className="flex min-h-9 items-center justify-between gap-3"><h2 className="text-sm font-semibold">{title}</h2>{action}</div>{children}</section>
}
function OrderDiff({ before, after }: { before: SandboxOrder; after: SandboxOrder }) {
  const rows: [string, string, string][] = [
    ["房型", before.roomTypeName, after.roomTypeName],
    ["房号", before.roomNumber || "未排房", after.roomNumber || "未排房"],
    ["入住", formatDateTime(before.checkIn), formatDateTime(after.checkIn)],
    ["离店", formatDateTime(before.checkOut), formatDateTime(after.checkOut)],
    ["应付", money(before.payableCents), money(after.payableCents)],
    ["状态", before.status, after.status],
  ]
  return <DataTable columns={["字段", "办理前", "办理后"]} rows={rows.map((cells, id) => ({ id, cells }))} />
}

export default function PMSSandboxPage() {
  const { session, ready } = useAuth()
  const superAdmin = session?.roles.includes("super_admin") ?? false
  const canView = superAdmin || (session?.permissions.includes("pmsSandbox.view") ?? false)
  const canManage = superAdmin || (session?.permissions.includes("pmsSandbox.manage") ?? false)
  const canExecute = superAdmin || (session?.permissions.includes("pmsSandbox.execute") ?? false)
  const [stores, setStores] = useState<{ id: number; name: string }[]>([])
  const [storeId, setStoreId] = useState("")
  const [data, setData] = useState<SandboxWorkspace | null>(null)
  const [customers, setCustomers] = useState<SandboxCustomerOption[]>([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")
  const [edit, setEdit] = useState<EditState | null>(null)
  const [bindOpen, setBindOpen] = useState(false)
  const [resetOpen, setResetOpen] = useState(false)
  const [operation, setOperation] = useState<SandboxOperation | null>(null)
  const requestNo = useRef(0)

  useEffect(() => {
    if (!ready || !canView) return
    let active = true
    fetchSandboxStores().then(result => {
      if (!active) return
      setStores(result)
      setStoreId(current => current || String(result[0]?.id ?? ""))
    }).catch(err => { if (active) setError(err.message) })
    return () => { active = false }
  }, [ready, canView])

  const reload = useCallback(async () => {
    if (!storeId || !canView) return
    const request = ++requestNo.current
    setLoading(true)
    setError("")
    try {
      const [workspace, customerOptions] = await Promise.all([fetchSandboxWorkspace(Number(storeId)), fetchSandboxCustomers(Number(storeId))])
      if (request !== requestNo.current) return
      setData(workspace)
      setCustomers(customerOptions)
    } catch (err) {
      if (request === requestNo.current) setError(err instanceof Error ? err.message : "加载失败")
    } finally {
      if (request === requestNo.current) setLoading(false)
    }
  }, [storeId, canView])
  useEffect(() => {
    setData(null)
    setEdit(null)
    setOperation(null)
    const requests = requestNo
    void reload()
    return () => { requests.current++ }
  }, [reload])

  async function mutate(action: () => Promise<unknown>, message: string) {
    setSaving(true)
    try {
      await action()
      await reload()
      toast.success(message)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "操作未完成")
      throw err
    } finally { setSaving(false) }
  }
  async function save(value: SandboxSave) {
    if (!data?.dataset) return
    await mutate(() => saveSandbox(Number(storeId), data.dataset!, value), "测试数据已保存")
    setEdit(null)
  }
  const add = (kind: EditKind) => canManage && (kind !== "order" || canExecute) ?
    <Button size="sm" variant="outline" disabled={saving} onClick={() => setEdit({ kind, item: null })}><PlusIcon className="size-4" />新增</Button> : undefined
  const editButton = (kind: EditKind, item: EditItem) => canManage && (kind !== "order" || canExecute) ?
    <IconAction title="编辑" icon={<PencilIcon className="size-4" />} disabled={saving} onClick={() => setEdit({ kind, item })} /> : null
  const enabled = (value: boolean) => <Badge variant={value ? "outline" : "secondary"}>{value ? "启用" : "停用"}</Badge>

  if (!ready) return <div className="p-6 text-sm text-muted-foreground">加载中</div>
  if (!canView) return <div className="p-6 text-sm">无测试 PMS 查看权限</div>
  return <main className="mx-auto flex w-full max-w-[1600px] min-w-0 flex-col gap-5 p-4 lg:p-6">
    <header className="flex flex-wrap items-center justify-between gap-4 border-b pb-4">
      <div className="flex min-w-0 items-center gap-3"><DatabaseIcon className="size-5 text-emerald-700" /><h1 className="text-xl font-semibold">测试 PMS</h1>
        <Badge variant="outline">{data?.active ? "会话已接入" : "未切换会话数据源"}</Badge></div>
      <div className="flex w-full items-center gap-2 sm:w-80"><OptionCombobox value={storeId} onChange={setStoreId} disabled={saving}
        options={stores.map(x => ({ value: String(x.id), label: x.name }))} placeholder="选择门店" />
        <IconAction title="刷新" icon={<RefreshCwIcon className={`size-4 ${loading ? "animate-spin" : ""}`} />} disabled={loading || saving || !storeId} onClick={() => { void reload() }} /></div>
    </header>
    <div className="flex items-start gap-2 text-xs text-muted-foreground"><ShieldCheckIcon className="mt-0.5 size-4 shrink-0" />
      <p>独立测试数据，不修改外部 HPMS，不发生真实扣款、退款或发券。</p></div>
    {error && <div role="alert" className="border-l-2 border-destructive bg-destructive/5 p-3 text-sm text-destructive">{error}</div>}
    {loading && !data ? <div className="py-16 text-center text-sm text-muted-foreground">正在查询测试数据</div> : !data?.dataset ? <section className="space-y-4 py-16 text-center">
      <DatabaseIcon className="mx-auto size-8 text-muted-foreground" /><h2 className="text-base font-medium">尚未初始化测试数据</h2>
      {canManage && <Button disabled={!storeId || saving} onClick={() => { void mutate(() => initializeSandbox(Number(storeId)), "演示数据已初始化").catch(() => {}) }}><PlusIcon className="size-4" />初始化演示数据</Button>}
    </section> : <>
      <div className="flex flex-wrap gap-x-6 gap-y-2 text-xs text-muted-foreground">
        <span>批次 {data.dataset.id}</span><span>版本 {data.dataset.version}</span><span>{data.orders.length} 笔测试订单</span>
        <span>{data.rooms.filter(x => x.enabled).length} 间启用房间</span><span>{data.bindings.length} 位绑定客户</span>
      </div>
      <Tabs defaultValue="orders" className="min-w-0">
        <div className="overflow-x-auto border-b pb-3"><TabsList className="w-max">
          <TabsTrigger value="orders"><BedDoubleIcon className="size-4" />订单与房态</TabsTrigger>
          <TabsTrigger value="members"><UsersIcon className="size-4" />会员与权益</TabsTrigger>
          <TabsTrigger value="rules"><Settings2Icon className="size-4" />规则与资源</TabsTrigger>
          <TabsTrigger value="operations"><ClipboardListIcon className="size-4" />办理记录</TabsTrigger>
          <TabsTrigger value="data"><DatabaseIcon className="size-4" />测试数据</TabsTrigger>
        </TabsList></div>
        <TabsContent value="orders" className="min-w-0 space-y-7 pt-4">
          <Section title="订单" action={add("order")}><DataTable columns={["订单 / 测试住客", "房型 / 房号", "入住 / 离店", "应付", "早餐", "状态", ""]}
            rows={data.orders.map(x => ({ id: x.id, cells: [
              <div key="order" className="min-w-36"><div className="font-medium">{x.number}</div><div className="mt-1 text-xs text-muted-foreground">{x.guestName} · {x.phone}</div></div>,
              <div key="room" className="min-w-28">{x.roomTypeName}<div className="text-xs text-muted-foreground">{x.roomNumber || "未排房"}</div></div>,
              <div key="dates" className="whitespace-nowrap text-xs leading-6">{formatDateTime(x.checkIn)}<br />{formatDateTime(x.checkOut)}</div>,
              <span key="price" className="whitespace-nowrap tabular-nums">{money(x.payableCents)}</span>, x.includesBreakfast ? "含早" : "不含",
              label(data, "orderStatus", x.status), editButton("order", x),
            ] }))} /></Section>
          <Section title="房间" action={add("room")}><DataTable columns={["房号", "房型", "楼层", "净脏状态", "已录入说明", "状态", ""]}
            rows={data.rooms.map(x => ({ id: x.id, cells: [x.number, data.roomTypes.find(t => t.id === x.roomTypeId)?.name,
              x.floor || "-", label(data, "cleanStatus", x.cleanStatus), <p key="desc" className="min-w-48 max-w-md whitespace-pre-wrap break-words">{x.description || "-"}</p>,
              enabled(x.enabled), editButton("room", x)] }))} /></Section>
          <Section title="房型" action={add("roomType")}><DataTable columns={["房型", "档位", "每晚价格", "状态", ""]}
            rows={data.roomTypes.map(x => ({ id: x.id, cells: [x.name, x.rank, money(x.priceCents), enabled(x.enabled), editButton("roomType", x)] }))} /></Section>
        </TabsContent>
        <TabsContent value="members" className="min-w-0 space-y-7 pt-4">
          <Section title="测试会员" action={add("member")}><DataTable columns={["会员", "测试手机号", "等级", "生日", "有效期", "状态", ""]}
            rows={data.members.map(x => ({ id: x.id, cells: [x.name, x.phone, x.gradeName, x.birthday || "-", formatDateTime(x.validUntil), enabled(x.enabled), editButton("member", x)] }))} /></Section>
          <Section title="会员等级与权益" action={add("grade")}><DataTable columns={["等级", "有效权益", "生日福利", "免费升房最高档位", "状态", ""]}
            rows={data.grades.map(x => ({ id: x.id, cells: [x.name,
              <p key="rights" className="min-w-48 max-w-md whitespace-pre-wrap">{x.benefits.join("\n") || "-"}</p>,
              <p key="birthday" className="min-w-40 max-w-sm whitespace-pre-wrap">{x.birthdayBenefits.join("\n") || "-"}</p>,
              x.freeUpgradeMaxRank || "无", enabled(x.enabled), editButton("grade", x)] }))} /></Section>
        </TabsContent>
        <TabsContent value="rules" className="min-w-0 space-y-7 pt-4">
          <Section title="场景规则" action={add("rule")}><DataTable columns={["规则", "正文", "授权动作", "金额 / 延退时刻", "有效期", "状态", ""]}
            rows={data.rules.map(x => ({ id: x.id, cells: [
              <div key="name" className="min-w-28">{x.name}<div className="text-xs text-muted-foreground">{label(data, "ruleCode", x.code)}</div></div>,
              <p key="text" className="min-w-64 max-w-lg whitespace-pre-wrap break-words">{x.text}</p>,
              label(data, "ruleAction", x.action), <div key="amount" className="whitespace-nowrap">{money(x.amountCents)}<div>{x.checkoutTime}</div></div>,
              <div key="valid" className="whitespace-nowrap text-xs">{x.validFrom ? formatDateTime(x.validFrom) : "即刻"}<br />{x.validUntil ? formatDateTime(x.validUntil) : "不限"}</div>,
              enabled(x.enabled), editButton("rule", x),
            ] }))} /></Section>
          <Section title="枕头商品资源" action={!data.resources.length ? add("resource") : undefined}>
            <DataTable columns={["商品", "原卡片消息", "口令", "卡片状态", ""]} rows={data.resources.map(x => ({ id: x.id, cells: [
              x.name, x.sourceMessageId || "未绑定", <p key="token" className="max-w-xl break-all text-xs">{x.token}</p>,
              <Badge key="ready" variant="outline">{x.sourceMessageId && x.messageType ? "已绑定原卡片" : "缺少原商品卡片"}</Badge>, editButton("resource", x),
            ] }))} /></Section>
        </TabsContent>
        <TabsContent value="operations" className="min-w-0 space-y-4 pt-4">
          <Section title="办理与测试数据审计"><DataTable columns={["记录", "会话 / 来源消息", "状态", "投递", "时间", ""]}
            rows={data.operations.map(x => ({ id: x.id, cells: [
              <div key="op" className="min-w-28">#{x.id}<div className="text-xs text-muted-foreground">{x.operationType}</div></div>,
              <div key="source">{x.conversationId || "后台"}<div className="text-xs text-muted-foreground">{x.sourceMessageId || "-"}</div></div>,
              label(data, "operationStatus", x.status), label(data, "deliveryStatus", x.deliveryStatus), <span key="time" className="whitespace-nowrap text-xs">{formatDateTime(x.createdAt)}</span>,
              <div key="actions" className="flex"><IconAction title="查看方案与回读" icon={<EyeIcon className="size-4" />} onClick={() => setOperation(x)} />
                {canExecute && x.status === "pending" && <IconAction title="取消方案" icon={<XIcon className="size-4" />} disabled={saving}
                  onClick={() => { void mutate(() => cancelSandboxOperation(Number(storeId), x.id), "方案已取消").catch(() => {}) }} />}</div>,
            ] }))} /></Section>
        </TabsContent>
        <TabsContent value="data" className="min-w-0 space-y-7 pt-4">
          <Section title="客户与测试数据绑定" action={canManage ? <Button size="sm" variant="outline" onClick={() => setBindOpen(true)}><LinkIcon className="size-4" />绑定客户</Button> : undefined}>
            <DataTable columns={["客户", "测试订单", "测试会员"]} rows={data.bindings.map(x => ({ id: x.id, cells: [
              customers.find(c => c.id === x.customerId)?.name ?? `客户 #${x.customerId}`,
              data.orders.find(o => o.id === x.orderId)?.number ?? "-", data.members.find(m => m.id === x.memberId)?.name ?? "-",
            ] }))} />
          </Section>
          <Section title="数据批次"><div className="flex flex-wrap items-center justify-between gap-4 border-y py-4">
            <div className="space-y-1 text-sm"><div>当前批次 #{data.dataset.id}</div><div className="text-xs text-muted-foreground">{formatDateTime(data.dataset.createdAt)}</div></div>
            {canManage && <Button variant="outline" disabled={saving} onClick={() => setResetOpen(true)}><RotateCcwIcon className="size-4" />重置演示数据</Button>}
          </div></Section>
        </TabsContent>
      </Tabs>
      {edit && <SandboxEdit state={edit} data={data} saving={saving} onClose={() => setEdit(null)} onSave={save} />}
      {bindOpen && <DashboardCrudFormDialog<SandboxBinding, Omit<SandboxBinding, "id">> open item={null} itemId={null} saving={saving}
        fields={[
          { name: "customerId", label: "当前门店客户", type: "select", valueType: "number", required: true, options: customers.map(x => ({ value: String(x.id), label: `${x.name} (#${x.id})` })) },
          { name: "orderId", label: "测试订单", type: "select", valueType: "number", required: true, options: data.orders.map(x => ({ value: String(x.id), label: `${x.number} · ${x.guestName}` })) },
          { name: "memberId", label: "测试会员", type: "select", valueType: "number", defaultValue: "0", options: [{ value: "0", label: "无" }, ...data.members.map(x => ({ value: String(x.id), label: x.name }))] },
        ]}
        labels={{ createTitle: "绑定测试客户", editTitle: "修改绑定", create: "绑定", save: "保存", saving: "保存中", cancel: "取消", loadingDetail: "加载中", required: "请选择", invalidNumber: "选择无效", minValue: () => "选择无效", maxValue: () => "选择无效" }}
        onOpenChange={open => { if (!open && !saving) setBindOpen(false) }}
        transformSubmitValues={v => ({ customerId: Number(v.customerId), orderId: Number(v.orderId), memberId: Number(v.memberId || 0) })}
        onSubmit={async value => { await mutate(() => bindSandboxCustomer(Number(storeId), data.dataset!, value), "测试数据绑定已保存"); setBindOpen(false) }}
      />}
    </>}
    <ProjectDialog open={resetOpen} onOpenChange={value => { if (!saving) setResetOpen(value) }} title="重置测试数据" size="sm"
      footer={<><Button variant="outline" disabled={saving} onClick={() => setResetOpen(false)}>取消</Button><Button variant="destructive" disabled={saving} onClick={() => {
        if (!data?.dataset) return
        void mutate(() => resetSandbox(Number(storeId), data.dataset!), "已建立新的测试批次").then(() => setResetOpen(false)).catch(() => {})
      }}><RotateCcwIcon className="size-4" />确认重置</Button></>}>
      <p className="text-sm">会创建新批次，并使旧办理方案失效。聊天记录与历史操作审计保留，客户需要重新绑定测试订单。</p>
    </ProjectDialog>
    <ProjectDialog open={!!operation} onOpenChange={value => { if (!value) setOperation(null) }} title={`测试办理 #${operation?.id ?? ""}`} size="xl">
      {operation && <div className="space-y-5">
        <div className="flex flex-wrap gap-3 text-xs"><Badge variant="outline">{operation.status}</Badge><span>数据批次 {operation.datasetId}</span><span>预览消息 {operation.previewMessageId || "未投递"}</span><span>确认消息 {operation.confirmationMessageId || "未确认"}</span></div>
        {operation.previewText && <p className="whitespace-pre-wrap break-words text-sm leading-6">{operation.previewText}</p>}
        {operation.plan && <OrderDiff before={operation.plan.before} after={operation.plan.after} />}
        {operation.plan?.commitment && <p className="text-sm">{operation.plan.commitment}</p>}
        {operation.resultText && <div className="flex gap-2 text-sm"><CheckCircle2Icon className="size-4 shrink-0 text-emerald-700" />{operation.resultText}</div>}
        {operation.errorMessage && <p className="text-sm text-destructive">{operation.errorMessage}</p>}
        {operation.expiresAt && <p className="text-xs text-muted-foreground">方案有效期至 {formatDateTime(operation.expiresAt)}</p>}
      </div>}
    </ProjectDialog>
  </main>
}
