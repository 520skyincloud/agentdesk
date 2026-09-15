"use client"

import { DashboardCrudFormDialog, type DashboardCrudFormField } from "@/components/dashboard/crud"
import type {
  SandboxGrade, SandboxMember, SandboxOrder, SandboxResource, SandboxRoom,
  SandboxRoomType, SandboxRule, SandboxSave, SandboxWorkspace,
} from "@/lib/api/pms-sandbox"

export type EditKind = keyof SandboxSave
export type EditItem = SandboxRoomType | SandboxRoom | SandboxOrder | SandboxGrade | SandboxMember | SandboxRule | SandboxResource
export type EditState = { kind: EditKind; item: EditItem | null }

const names: Record<EditKind, string> = {
  order: "测试订单", roomType: "房型", room: "房间", grade: "会员等级",
  member: "测试会员", rule: "场景规则", resource: "枕头资源",
}
const labels = {
  createTitle: "", editTitle: "", create: "创建", save: "保存", saving: "保存中",
  cancel: "取消", loadingDetail: "加载中", required: "请填写必填项",
  invalidNumber: "请输入有效数字", minValue: (n: number) => `不能小于 ${n}`, maxValue: (n: number) => `不能大于 ${n}`,
}
const enabled: DashboardCrudFormField<EditItem> = { name: "enabled", label: "启用", type: "switch", defaultValue: true }
const input = (name: string, label: string, required = true): DashboardCrudFormField<EditItem> =>
  ({ name, label, required, trim: true })
const number = (name: string, label: string, defaultValue = 0): DashboardCrudFormField<EditItem> =>
  ({ name, label, type: "number", valueType: "number", min: 0, step: 1, required: true, defaultValue })
const date = (name: string, label: string, required = true): DashboardCrudFormField<EditItem> => ({
  name, label, required, placeholder: "yyyy-MM-dd HH:mm:ss", trim: true,
  pattern: required ? /^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(:\d{2})?(Z|[+-]\d{2}:\d{2})?$/ : undefined,
  patternMessage: "请输入 yyyy-MM-dd HH:mm:ss",
})

function fields(kind: EditKind, data: SandboxWorkspace): DashboardCrudFormField<EditItem>[] {
  const select = (name: string, label: string, options: { value: string; label: string }[], optional = false): DashboardCrudFormField<EditItem> => ({
    name, label, type: "select", valueType: "number", required: !optional, defaultValue: optional ? "0" : undefined,
    options: optional ? [{ value: "0", label: "无" }, ...options] : options,
  })
  const types = data.roomTypes.map((x) => ({ value: String(x.id), label: x.name }))
  const rooms = data.rooms.map((x) => ({ value: String(x.id), label: `${x.number} · ${data.roomTypes.find(t => t.id === x.roomTypeId)?.name ?? ""}` }))
  const grades = data.grades.map((x) => ({ value: String(x.id), label: x.name }))
  switch (kind) {
    case "roomType": return [input("name", "房型名称"), number("rank", "房型档位", 1), number("priceCents", "每晚房价（分）"), enabled]
    case "room": return [
      input("number", "房号"), select("roomTypeId", "房型", types), input("floor", "楼层", false),
      { name: "cleanStatus", label: "净脏状态", type: "select", options: data.options.cleanStatus, required: true },
      { name: "description", label: "已确认的房间说明", type: "textarea", rows: 3 },
      enabled,
    ]
    case "order": return [
      input("number", "测试订单号"), input("guestName", "测试住客称呼"), input("phone", "测试手机号"),
      select("roomTypeId", "房型", types), select("roomId", "分配房间", rooms, true),
      date("checkIn", "入住时间"), date("checkOut", "离店时间"),
      number("payableCents", "应付金额（分）"),
      { name: "includesBreakfast", label: "订单含早餐", type: "switch", defaultValue: false },
      { name: "status", label: "订单状态", type: "select", options: data.options.orderStatus, required: true },
    ]
    case "grade": return [
      input("name", "等级名称"),
      { name: "benefits", label: "有效权益", type: "textarea", rows: 4, valueFromItem: x => (x as SandboxGrade).benefits.join("\n") },
      { name: "birthdayBenefits", label: "生日福利", type: "textarea", rows: 3, valueFromItem: x => (x as SandboxGrade).birthdayBenefits.join("\n") },
      number("freeUpgradeMaxRank", "可免费升至的最高房型档位"), enabled,
    ]
    case "member": return [
      input("name", "测试会员称呼"), input("phone", "测试手机号"), select("gradeId", "会员等级", grades),
      { ...input("birthday", "生日", false), placeholder: "MM-DD" }, date("validUntil", "会员有效期"), enabled,
    ]
    case "rule": return [
      { name: "code", label: "规则类型", type: "select", options: data.options.ruleCode, required: true },
      input("name", "规则名称"), { name: "text", label: "规则正文", type: "textarea", rows: 4, required: true },
      { name: "action", label: "授权动作", type: "select", options: data.options.ruleAction, required: true },
      number("amountCents", "测试金额（分）"),
      { ...input("checkoutTime", "延退时刻", false), placeholder: "14:00" },
      select("minimumGradeId", "适用会员等级", grades, true),
      date("validFrom", "生效时间", false), date("validUntil", "失效时间", false), enabled,
    ]
    case "resource": return [
      input("name", "商品名称"),
      { ...number("sourceMessageId", "原商品卡片消息 ID"), description: "从本店原卡片导入，不使用入住小程序。" },
      { name: "token", label: "固定口令", type: "text", defaultValue: "#微信小店://丽斯严选/NxS0zhyvZJmEDEe" },
      enabled,
    ]
  }
}

export function SandboxEdit({ state, data, saving, onClose, onSave }: {
  state: EditState; data: SandboxWorkspace; saving: boolean
  onClose: () => void; onSave: (value: SandboxSave) => Promise<void>
}) {
  return (
    <DashboardCrudFormDialog<EditItem, SandboxSave>
      open saving={saving} item={state.item} itemId={state.item?.id ?? null}
      fields={fields(state.kind, data)}
      labels={{ ...labels, createTitle: `新增${names[state.kind]}`, editTitle: `编辑${names[state.kind]}` }}
      onOpenChange={open => { if (!open && !saving) onClose() }}
      transformSubmitValues={values => {
        const value: Record<string, unknown> = { ...state.item, ...values, id: state.item?.id ?? 0 }
        for (const key of ["benefits", "birthdayBenefits"]) {
          if (typeof value[key] === "string") value[key] = value[key].split("\n").map(x => x.trim()).filter(Boolean)
        }
        for (const key of ["checkIn", "checkOut", "validFrom", "validUntil"]) {
          if (!(key in value)) continue
          const text = String(value[key] ?? "").trim()
          if (!text) { value[key] = null; continue }
          const parsed = new Date(text.replace(" ", "T"))
          if (Number.isNaN(parsed.getTime())) throw new Error("日期格式不正确")
          value[key] = parsed.toISOString()
        }
        if (state.kind === "resource") {
          value.code = "pillow"
          delete value.cardPayload
        }
        return { [state.kind]: value } as SandboxSave
      }}
      onSubmit={onSave}
    />
  )
}
