import {
  BotIcon,
  Clock3Icon,
  MapPinIcon,
  MessageCircleMoreIcon,
  QrCodeIcon,
  UsersRoundIcon,
} from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"

const setupCards = [
  {
    title: "门店资料",
    description: "维护门店名称、地址、导航坐标和联系电话。客人问位置时，系统使用这里的变量发送定位。",
    icon: MapPinIcon,
    status: "待绑定",
  },
  {
    title: "企业微信员工号",
    description: "这里展示门店用于接待客人的企微员工号状态。更换员工号由总部发起重新扫码。",
    icon: QrCodeIcon,
    status: "由总部管理",
  },
  {
    title: "人工通知群",
    description: "从员工号可见群聊中选择门店服务群，再选择需要 @ 的值班成员。无需手填群 ID。",
    icon: UsersRoundIcon,
    status: "待选择",
  },
  {
    title: "服务时间",
    description: "配置门店值班时间。命中时间段优先发群提醒，非值班时间进入总部网页端兜底。",
    icon: Clock3Icon,
    status: "待配置",
  },
]

export default function StoreWorkbenchPage() {
  return (
    <main className="min-h-screen bg-[#f5f8fc] px-4 py-5 sm:px-6 lg:px-8">
      <div className="mx-auto flex max-w-6xl flex-col gap-5">
        <section className="overflow-hidden rounded-[28px] border border-[#dbe7f6] bg-white shadow-[0_20px_60px_rgba(35,74,122,0.08)]">
          <div className="flex flex-col gap-5 border-b border-[#e7eef7] bg-linear-to-br from-[#fafdff] to-[#eef5ff] p-5 sm:p-7 lg:flex-row lg:items-center lg:justify-between">
            <div className="min-w-0 space-y-3">
              <Badge className="rounded-full bg-[#2374e1] px-3 py-1 text-xs text-white">门店员工工作台</Badge>
              <div>
                <h1 className="text-2xl font-semibold tracking-normal text-[#172033] sm:text-3xl">门店配置和接待提醒</h1>
                <p className="mt-2 max-w-2xl text-sm leading-6 text-[#65758b]">
                  这里是门店同事登录后的独立页面，只维护门店能理解和负责的内容：门店资料、服务时间、通知群和接待提醒。
                </p>
              </div>
            </div>
            <div className="grid gap-2 sm:grid-cols-2 lg:w-[360px]">
              <div className="rounded-2xl border border-[#dbe7f6] bg-white/80 p-4">
                <div className="text-xs text-[#65758b]">当前门店</div>
                <div className="mt-1 truncate text-lg font-semibold text-[#172033]">待绑定门店</div>
              </div>
              <div className="rounded-2xl border border-[#dbe7f6] bg-white/80 p-4">
                <div className="text-xs text-[#65758b]">接待状态</div>
                <div className="mt-1 text-lg font-semibold text-[#172033]">待完成配置</div>
              </div>
            </div>
          </div>

          <div className="grid gap-4 p-5 sm:p-7 lg:grid-cols-[1.25fr_0.75fr]">
            <div className="grid gap-4 sm:grid-cols-2">
              {setupCards.map((card) => {
                const Icon = card.icon
                return (
                  <article key={card.title} className="rounded-3xl border border-[#dbe7f6] bg-[#fbfdff] p-5 shadow-[0_10px_30px_rgba(35,74,122,0.04)]">
                    <div className="flex items-start justify-between gap-4">
                      <div className="flex size-11 items-center justify-center rounded-2xl bg-[#eaf3ff] text-[#2374e1]">
                        <Icon className="size-5" />
                      </div>
                      <Badge variant="outline" className="rounded-full border-[#cddced] bg-white text-[#4b6078]">{card.status}</Badge>
                    </div>
                    <h2 className="mt-4 text-base font-semibold text-[#172033]">{card.title}</h2>
                    <p className="mt-2 text-sm leading-6 text-[#65758b]">{card.description}</p>
                  </article>
                )
              })}
            </div>

            <aside className="rounded-3xl border border-[#dbe7f6] bg-[#fbfdff] p-5 shadow-[0_10px_30px_rgba(35,74,122,0.04)]">
              <div className="flex items-center gap-3">
                <div className="flex size-11 items-center justify-center rounded-2xl bg-[#eaf3ff] text-[#2374e1]">
                  <BotIcon className="size-5" />
                </div>
                <div>
                  <h2 className="font-semibold text-[#172033]">智能客服状态</h2>
                  <p className="text-xs text-[#65758b]">模型、知识库和提示词由总部为每个员工号独立配置。</p>
                </div>
              </div>
              <div className="mt-5 space-y-3 rounded-2xl border border-[#dbe7f6] bg-white p-4 text-sm text-[#4b6078]">
                <div className="flex items-center justify-between gap-3">
                  <span>AI 托管</span>
                  <Badge className="rounded-full bg-[#2374e1] text-white">按账号开关</Badge>
                </div>
                <div className="flex items-center justify-between gap-3">
                  <span>知识库</span>
                  <span className="font-medium text-[#172033]">待绑定</span>
                </div>
                <div className="flex items-center justify-between gap-3">
                  <span>人工兜底</span>
                  <span className="font-medium text-[#172033]">按服务时间</span>
                </div>
              </div>
              <div className="mt-5 flex flex-col gap-2 sm:flex-row lg:flex-col">
                <Button className="rounded-xl bg-[#2374e1] hover:bg-[#1b63c4]">
                  <MessageCircleMoreIcon className="size-4" />
                  查看待处理提醒
                </Button>
                <Button variant="outline" className="rounded-xl border-[#cddced] bg-white">
                  <UsersRoundIcon className="size-4" />
                  配置门店通知群
                </Button>
              </div>
            </aside>
          </div>
        </section>
      </div>
    </main>
  )
}
