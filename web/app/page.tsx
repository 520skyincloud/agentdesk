"use client"

import Link from "next/link"
import { useMemo, useState, type ComponentType } from "react"
import {
  ArrowRight,
  BarChart3,
  Bell,
  Bot,
  CalendarDays,
  CheckCircle2,
  CircleUserRound,
  DatabaseZap,
  Headphones,
  LineChart,
  Megaphone,
  MessageCircle,
  Mic,
  MousePointer2,
  PanelLeft,
  Settings,
  ShieldCheck,
  Sparkles,
  Tag,
  UserPlus,
  UsersRound,
  WandSparkles,
  Zap,
} from "lucide-react"

const navItems = [
  { label: "产品功能", href: "#features" },
  { label: "解决方案", href: "#solution" },
  { label: "客户案例", href: "#customers" },
  { label: "价格方案", href: "#pricing" },
  { label: "资源中心", href: "#resources" },
]

const heroPoints = ["专为微信和企业微信打造", "一客一群专属服务", "重塑企业客户运营"]

const dashboardMetrics = [
  { label: "今日接待客户", value: "2,892", delta: "+12.5%" },
  { label: "自动回复率", value: "92.1%", delta: "+8.3%" },
  { label: "解决率", value: "98.6%", delta: "+3.7%" },
  { label: "新增客户", value: "1,247", delta: "+11.2%" },
]

const workspaceItems = [
  { label: "工作台", icon: PanelLeft },
  { label: "消息中心", icon: MessageCircle },
  { label: "客户管理", icon: UsersRound },
  { label: "智能客服", icon: Headphones },
  { label: "营销自动化", icon: MousePointer2 },
  { label: "数据分析", icon: BarChart3 },
  { label: "设置中心", icon: Settings },
]

const quickActions = [
  { label: "智能回复", icon: MessageCircle, tone: "from-[#627cff] to-[#8e6fff]" },
  { label: "智能标签", icon: Tag, tone: "from-[#9581ff] to-[#b877ff]" },
  { label: "人工转接", icon: CircleUserRound, tone: "from-[#56dfc4] to-[#46bce9]" },
  { label: "语音回复", icon: Mic, tone: "from-[#63d6ff] to-[#6c8cff]" },
  { label: "朋友圈发布", icon: CalendarDays, tone: "from-[#ffb36b] to-[#ff8ea5]" },
  { label: "自动加好友", icon: UserPlus, tone: "from-[#71dbff] to-[#758bff]" },
  { label: "智能欢迎语", icon: Bot, tone: "from-[#9b79ff] to-[#6f7dff]" },
]

const featureGroups = [
  { key: "service", label: "智能接待" },
  { key: "marketing", label: "营销自动化" },
  { key: "growth", label: "客户运营" },
]

const features = [
  {
    title: "智能回复",
    desc: "理解客户问题，拟人回复，7x24小时在线服务。",
    icon: MessageCircle,
    tone: "from-[#eef3ff] to-[#f6f1ff]",
    iconTone: "from-[#607dff] to-[#8f6fff]",
    tags: ["多平台集成", "智能学习"],
  },
  {
    title: "智能标签",
    desc: "自动识别客户意图，精准打标，补全用户画像。",
    icon: Tag,
    tone: "from-[#effdf7] to-[#f4f6ff]",
    iconTone: "from-[#56dfc4] to-[#47bce8]",
    tags: ["自动分类", "精准画像", "智能推荐"],
  },
  {
    title: "人工转接",
    desc: "复杂问题无缝接入人工，无缝衔接，消息不断线。",
    icon: UserPlus,
    tone: "from-[#f2f7ff] to-[#f7f1ff]",
    iconTone: "from-[#6a8cff] to-[#9c79ff]",
    tags: ["智能判断", "优先分配", "流畅接入"],
  },
  {
    title: "语音回复",
    desc: "语音消息自动转文字，并智能组织自然回复。",
    icon: Mic,
    tone: "from-[#f7f2ff] to-[#f6fbff]",
    iconTone: "from-[#9b79ff] to-[#6ecfff]",
    tags: ["多种音色", "语音识别", "自然流畅"],
  },
  {
    title: "朋友圈发布",
    desc: "批量发布朋友圈内容，定时发布，营销不停歇。",
    icon: Megaphone,
    tone: "from-[#f1fff9] to-[#f8fbff]",
    iconTone: "from-[#5ce1bd] to-[#7bdc89]",
    tags: ["批量发布", "定时发布", "素材库"],
  },
  {
    title: "自动加好友",
    desc: "智能自动添加好友，拓展客户池，安全合规操作。",
    icon: UserPlus,
    tone: "from-[#effcf9] to-[#f5f4ff]",
    iconTone: "from-[#52d9c2] to-[#54b7ff]",
    tags: ["批量导入", "自动验证", "安全合规"],
  },
  {
    title: "智能欢迎语",
    desc: "新好友自动发送欢迎语，第一时间建立联系。",
    icon: Bot,
    tone: "from-[#f4f1ff] to-[#f8fbff]",
    iconTone: "from-[#9d78ff] to-[#677fff]",
    tags: ["100%自动", "个性化内容", "延迟发送"],
  },
]

const marketingPoints: Array<{ title: string; desc: string; icon: ComponentType<{ className?: string }> }> = [
  { title: "营销自动化", desc: "全链路营销流程自动化，提升转化效率", icon: MousePointer2 },
  { title: "数据分析", desc: "多维度数据洞察，优化运营策略", icon: DatabaseZap },
  { title: "效果追踪", desc: "实时追踪运营效果，驱动持续增长", icon: LineChart },
]

const analysisStats = [
  { label: "消息总数", value: "56,892", delta: "+15.2%" },
  { label: "新增客户", value: "1,247", delta: "+11.2%" },
  { label: "转化客户", value: "328", delta: "+9.3%" },
  { label: "客户满意度", value: "98.6%", delta: "+3.7%" },
]

const partners = ["元气森林", "usmile", "CHANDO", "babemax", "DJI", "周大福"]

const assurances = [
  { title: "安全稳定", desc: "企业级安全保障", icon: ShieldCheck },
  { title: "快速部署", desc: "即开即用", icon: Zap },
  { title: "专业服务", desc: "1对1专属支持", icon: Headphones },
  { title: "持续迭代", desc: "功能持续更新", icon: WandSparkles },
]

export default function HomePage() {
  const [activeGroup, setActiveGroup] = useState(featureGroups[0].key)
  const currentGroup = useMemo(
    () => featureGroups.find((item) => item.key === activeGroup) ?? featureGroups[0],
    [activeGroup]
  )

  return (
    <main className="min-h-screen overflow-hidden bg-[#f8fbff] text-[#182033] selection:bg-[#9178ff]/20">
      <style jsx global>{`
        @keyframes wb-float {
          0%, 100% { transform: translate3d(0, 0, 0) rotate(0deg); }
          50% { transform: translate3d(0, -18px, 0) rotate(1deg); }
        }
        @keyframes wb-rise {
          0%, 100% { transform: translateY(0) scale(1); opacity: .72; }
          50% { transform: translateY(-24px) scale(1.04); opacity: 1; }
        }
        @keyframes wb-glow {
          0%, 100% { opacity: .45; filter: blur(18px); }
          50% { opacity: .82; filter: blur(24px); }
        }
        @keyframes wb-slide {
          0% { transform: translateX(-10%); }
          100% { transform: translateX(10%); }
        }
        .wb-float { animation: wb-float 8s ease-in-out infinite; }
        .wb-rise { animation: wb-rise 7s ease-in-out infinite; }
        .wb-glow { animation: wb-glow 5s ease-in-out infinite; }
        .wb-slide { animation: wb-slide 12s ease-in-out infinite alternate; }
      `}</style>

      <section className="relative isolate min-h-[920px] px-5 pb-14 pt-6 sm:px-8 xl:px-10">
        <PageAura />
        <Header />

        <div className="relative mx-auto grid max-w-[1400px] gap-12 pb-4 pt-16 lg:grid-cols-[0.72fr_1.28fr] lg:items-center lg:pt-20">
          <div className="relative z-10">
            <div className="mb-7 inline-flex items-center gap-2 rounded-full border border-white/80 bg-white/55 px-4 py-2 text-sm font-bold text-[#6877ef] shadow-[0_18px_50px_rgba(108,123,210,0.12)] backdrop-blur-2xl">
              <span className="size-2 rounded-full bg-[#62dec9] shadow-[0_0_20px_rgba(98,222,201,0.8)]" />
              微信 / 企微数字员工平台
            </div>

            <h1 className="text-balance text-[3.05rem] font-black leading-[1.06] tracking-[-0.065em] text-[#111827] sm:text-6xl xl:text-[5.15rem]">
              微信/企微数字员工
              <span className="mt-3 block text-[#111827]">7x24小时自动接管</span>
              <span className="block bg-gradient-to-r from-[#34cfc9] via-[#6386ff] to-[#8f69ff] bg-clip-text text-transparent drop-shadow-[0_18px_26px_rgba(126,111,255,0.18)]">
                微信客服
              </span>
            </h1>

            <div className="mt-8 space-y-4">
              {heroPoints.map((point) => (
                <div key={point} className="flex items-center gap-3 text-lg font-semibold text-[#6a7185]">
                  <span className="flex size-6 items-center justify-center rounded-full border border-[#9fb0ff]/60 bg-white/70 text-[#657dff] shadow-[0_10px_24px_rgba(101,125,255,0.12)]">
                    <CheckCircle2 className="size-4" />
                  </span>
                  {point}
                </div>
              ))}
            </div>

            <div className="mt-9 flex flex-col gap-4 sm:flex-row">
              <Link
                href="/dashboard/login"
                className="group inline-flex h-14 items-center justify-center gap-3 rounded-full bg-gradient-to-r from-[#30d5d0] via-[#6284ff] to-[#8a62ff] px-8 text-base font-black text-white shadow-[0_20px_46px_rgba(99,122,255,0.32)] transition duration-300 hover:-translate-y-1 hover:shadow-[0_26px_60px_rgba(99,122,255,0.42)]"
              >
                免费试用
                <ArrowRight className="size-5 transition group-hover:translate-x-1" />
              </Link>
              <a
                href="#features"
                className="inline-flex h-14 items-center justify-center rounded-full border border-[#ccd8ff] bg-white/62 px-8 text-base font-black text-[#6174ee] shadow-[0_16px_40px_rgba(108,123,210,0.1)] backdrop-blur-2xl transition duration-300 hover:-translate-y-1 hover:border-[#9a8cff] hover:bg-white/85"
              >
                预约演示
              </a>
            </div>
          </div>

          <HeroDashboard />
        </div>
      </section>

      <section id="features" className="relative px-5 py-24 sm:px-8 xl:px-10">
        <SoftBlob className="left-1/2 top-8 h-[480px] w-[920px] -translate-x-1/2 bg-[#c7edff]" />
        <div className="relative mx-auto max-w-[1320px]">
          <SectionHeading
            kicker="Digital Employee Platform"
            title="全链路数字员工能力
驱动客户运营增长"
            desc="覆盖客户服务与营销全链路，让每一次互动都更有价值。"
          />

          <div className="mx-auto mt-10 flex max-w-3xl flex-wrap justify-center gap-3">
            {featureGroups.map((group) => (
              <button
                key={group.key}
                type="button"
                onClick={() => setActiveGroup(group.key)}
                className={`inline-flex items-center gap-2 rounded-full border px-8 py-3 text-sm font-black transition duration-300 ${
                  currentGroup.key === group.key
                    ? "border-white bg-white text-[#6478ff] shadow-[0_18px_42px_rgba(101,125,255,0.16)]"
                    : "border-[#e5ebff] bg-white/45 text-[#737a8c] hover:border-[#b9c5ff] hover:bg-white/70"
                }`}
              >
                <span className="size-2 rounded-full bg-current opacity-50" />
                {group.label}
              </button>
            ))}
          </div>

          <div className="mt-14 grid gap-6 md:grid-cols-2 xl:grid-cols-4">
            {features.slice(0, 4).map((feature) => (
              <FeatureCard key={feature.title} feature={feature} />
            ))}
          </div>
          <div className="mx-auto mt-6 grid max-w-5xl gap-6 md:grid-cols-3">
            {features.slice(4).map((feature) => (
              <FeatureCard key={feature.title} feature={feature} />
            ))}
          </div>
        </div>
      </section>

      <section id="solution" className="relative px-5 py-24 sm:px-8 xl:px-10">
        <SoftBlob className="-left-40 top-14 h-[560px] w-[560px] bg-[#dbcaff]" />
        <SoftBlob className="right-0 top-24 h-[520px] w-[620px] bg-[#c8fff1]" />
        <div className="relative mx-auto grid max-w-[1320px] gap-14 lg:grid-cols-[0.72fr_1.28fr] lg:items-center">
          <div>
            <p className="text-sm font-black uppercase tracking-[0.22em] text-[#6877ef]">Marketing & Data</p>
            <h2 className="mt-5 text-4xl font-black leading-tight tracking-[-0.055em] text-[#111827] sm:text-5xl">
              营销自动化 + 数据分析
              <br />
              让增长有据可依
            </h2>
            <p className="mt-5 max-w-md text-lg leading-8 text-[#757c8d]">
              不再靠人工翻聊天记录判断客户价值，把接待、跟进、复盘变成可追踪的增长系统。
            </p>
            <div className="mt-10 space-y-7">
              {marketingPoints.map((point) => (
                <div key={point.title} className="group flex gap-4">
                  <span className="flex size-12 shrink-0 items-center justify-center rounded-[1.25rem] border border-white/80 bg-white/70 text-[#667cff] shadow-[0_16px_36px_rgba(98,124,255,0.13)] backdrop-blur-2xl transition group-hover:-translate-y-1">
                    <point.icon className="size-5" />
                  </span>
                  <span>
                    <span className="block text-lg font-black text-[#172033]">{point.title}</span>
                    <span className="mt-1 block leading-7 text-[#757c8d]">{point.desc}</span>
                  </span>
                </div>
              ))}
            </div>
            <a
              href="#customers"
              className="mt-10 inline-flex items-center gap-2 rounded-full border border-[#d7e0ff] bg-white/65 px-6 py-3 font-black text-[#6074ee] shadow-[0_14px_34px_rgba(108,123,210,0.1)] backdrop-blur-2xl transition hover:-translate-y-1 hover:bg-white/90"
            >
              了解更多
              <ArrowRight className="size-4" />
            </a>
          </div>

          <AnalyticsShowcase />
        </div>
      </section>

      <section id="customers" className="px-5 py-20 sm:px-8 xl:px-10">
        <div className="mx-auto max-w-[1320px] text-center">
          <p className="text-sm font-black uppercase tracking-[0.22em] text-[#6877ef]">Trusted by Teams</p>
          <h2 className="mt-4 text-3xl font-black tracking-[-0.045em] text-[#111827]">众多企业信赖的数字员工平台</h2>
          <div className="mt-10 grid gap-4 sm:grid-cols-2 lg:grid-cols-6">
            {partners.map((partner) => (
              <div
                key={partner}
                className="flex min-h-24 items-center justify-center rounded-[1.75rem] border border-white/85 bg-white/55 px-5 text-2xl font-black text-[#9aa4b8] shadow-[0_18px_50px_rgba(108,123,210,0.08)] backdrop-blur-2xl transition hover:-translate-y-1 hover:text-[#7583ff]"
              >
                {partner}
              </div>
            ))}
          </div>
        </div>
      </section>

      <section id="pricing" className="px-5 pb-12 pt-14 sm:px-8 xl:px-10">
        <FinalCta />
      </section>

      <Footer />
    </main>
  )
}

function Header() {
  return (
    <header className="relative z-20 mx-auto flex max-w-[1320px] items-center justify-between rounded-full border border-white/80 bg-white/55 px-4 py-3 shadow-[0_18px_60px_rgba(97,116,180,0.12)] backdrop-blur-2xl">
      <Link href="/" className="flex items-center gap-3">
        <LogoMark />
        <span className="leading-tight">
          <span className="block text-lg font-black tracking-tight text-[#111827]">知悉微宝</span>
          <span className="block text-xs font-semibold text-[#687084]">Agent Desk</span>
        </span>
      </Link>

      <nav className="hidden items-center gap-8 text-sm font-bold text-[#343b4c] lg:flex">
        {navItems.map((item) => (
          <a key={item.href} href={item.href} className="transition hover:text-[#667cff]">
            {item.label}
          </a>
        ))}
      </nav>

      <div className="flex items-center gap-3">
        <Link
          href="/dashboard/login"
          className="hidden rounded-full border border-[#d9e2ff] bg-white/70 px-5 py-2.5 text-sm font-black text-[#4b5568] shadow-sm transition hover:border-[#a99cff] hover:text-[#667cff] sm:inline-flex"
        >
          登录
        </Link>
        <Link
          href="/dashboard/login"
          className="rounded-full bg-gradient-to-r from-[#42d4d0] to-[#8966ff] px-5 py-2.5 text-sm font-black text-white shadow-[0_14px_34px_rgba(93,113,255,0.28)] transition hover:-translate-y-0.5"
        >
          免费试用
        </Link>
      </div>
    </header>
  )
}

function HeroDashboard() {
  return (
    <div className="relative mx-auto w-full max-w-[835px] wb-float">
      <div className="absolute -inset-7 rounded-[3.4rem] bg-[linear-gradient(135deg,rgba(255,255,255,0.65),rgba(133,123,255,0.18),rgba(80,218,220,0.24))] blur-2xl wb-glow" />
      <div className="absolute -bottom-16 -left-20 h-40 w-80 rotate-[-12deg] rounded-full bg-[linear-gradient(90deg,rgba(97,222,224,0.28),rgba(149,112,255,0.22),transparent)] blur-xl wb-slide" />
      <div className="relative overflow-hidden rounded-[2.25rem] border border-white/90 bg-white/62 p-5 shadow-[0_32px_110px_rgba(93,113,180,0.22)] backdrop-blur-2xl">
        <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_16%_8%,rgba(255,255,255,0.92),transparent_26%),radial-gradient(circle_at_80%_100%,rgba(128,112,255,0.12),transparent_32%)]" />
        <div className="relative grid gap-5 lg:grid-cols-[150px_1fr]">
          <aside className="hidden rounded-[1.7rem] border border-white/70 bg-white/46 p-4 shadow-[inset_0_1px_0_rgba(255,255,255,0.9)] lg:block">
            <div className="mb-8 flex items-center gap-2 text-sm font-black">
              <LogoMark small />
              知悉微宝
            </div>
            <div className="space-y-2">
              {workspaceItems.map((item, index) => (
                <div
                  key={item.label}
                  className={`flex items-center gap-2 rounded-2xl px-3 py-3 text-xs font-black ${
                    index === 0 ? "bg-[#eef3ff] text-[#627cff]" : "text-[#6f778a]"
                  }`}
                >
                  <item.icon className="size-4" />
                  {item.label}
                </div>
              ))}
            </div>
          </aside>

          <div className="relative space-y-5">
            <div className="flex items-center justify-between">
              <h3 className="text-xl font-black text-[#172033]">工作台</h3>
              <div className="flex items-center gap-3 text-sm font-bold text-[#737b8d]">
                <Bell className="size-4 text-[#697dff]" />
                <span className="size-8 rounded-full bg-gradient-to-br from-[#5ce0c8] to-[#8d70ff]" />
                管理员
              </div>
            </div>

            <div className="grid gap-3 sm:grid-cols-4">
              {dashboardMetrics.map((metric) => (
                <div key={metric.label} className="rounded-[1.35rem] border border-white/70 bg-white/64 p-4 shadow-[0_12px_34px_rgba(93,113,255,0.08)]">
                  <div className="text-xs font-bold text-[#858ca0]">{metric.label}</div>
                  <div className="mt-2 text-2xl font-black tracking-[-0.04em] text-[#172033]">{metric.value}</div>
                  <div className="mt-1 text-xs font-black text-[#29c4a8]">{metric.delta}</div>
                </div>
              ))}
            </div>

            <div className="grid gap-4 lg:grid-cols-[1fr_0.42fr]">
              <div className="rounded-[1.6rem] border border-white/70 bg-white/64 p-5 shadow-[0_12px_34px_rgba(93,113,255,0.08)]">
                <div className="mb-5 flex items-center justify-between">
                  <h4 className="font-black text-[#172033]">客户咨询趋势</h4>
                  <div className="flex items-center gap-3 text-xs font-bold text-[#858ca0]">
                    <span>人工咨询</span>
                    <span className="text-[#23c5a8]">自动回复</span>
                  </div>
                </div>
                <MiniLineChart />
              </div>
              <div className="rounded-[1.6rem] border border-white/70 bg-white/64 p-5 shadow-[0_12px_34px_rgba(93,113,255,0.08)]">
                <h4 className="mb-4 font-black text-[#172033]">实时会话</h4>
                <div className="space-y-4">
                  {["广州 · 申先生", "上海 · 客户A", "北京 · 王先生", "深圳 · 小M"].map((item, index) => (
                    <div key={item} className="flex items-center gap-3">
                      <span className={`size-9 rounded-full bg-gradient-to-br ${index % 2 ? "from-[#8b68ff] to-[#62cfff]" : "from-[#5de5c5] to-[#6689ff]"}`} />
                      <span className="min-w-0">
                        <span className="block truncate text-sm font-black text-[#30384b]">{item}</span>
                        <span className="block truncate text-xs font-medium text-[#99a1b3]">正在咨询产品功能...</span>
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            </div>

            <div className="rounded-[1.6rem] border border-white/70 bg-white/64 p-4 shadow-[0_12px_34px_rgba(93,113,255,0.08)]">
              <h4 className="mb-4 font-black text-[#172033]">快捷功能</h4>
              <div className="grid grid-cols-4 gap-3 sm:grid-cols-7">
                {quickActions.map((action) => (
                  <div key={action.label} className="text-center">
                    <span className={`mx-auto flex size-11 items-center justify-center rounded-[1.05rem] bg-gradient-to-br ${action.tone} text-white shadow-[0_12px_24px_rgba(93,113,255,0.16)]`}>
                      <action.icon className="size-5" />
                    </span>
                    <span className="mt-2 block text-xs font-black text-[#667085]">{action.label}</span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        </div>
      </div>
      <div className="absolute -left-12 bottom-28 hidden size-16 items-center justify-center rounded-[1.4rem] bg-gradient-to-br from-[#8b68ff] to-[#62cfff] text-white shadow-[0_18px_42px_rgba(93,113,255,0.28)] lg:flex wb-rise">
        <MessageCircle className="size-8" />
      </div>
    </div>
  )
}

function FeatureCard({ feature }: { feature: (typeof features)[number] }) {
  return (
    <article className={`group relative overflow-hidden rounded-[2rem] border border-white/85 bg-gradient-to-br ${feature.tone} p-6 shadow-[0_22px_64px_rgba(93,113,255,0.09)] backdrop-blur-2xl transition duration-500 hover:-translate-y-2 hover:shadow-[0_34px_90px_rgba(93,113,255,0.17)]`}>
      <div className="absolute right-[-30px] top-[-30px] size-32 rounded-full bg-white/50 blur-2xl transition group-hover:scale-125" />
      <span className={`relative mb-8 flex size-16 items-center justify-center rounded-[1.45rem] bg-gradient-to-br ${feature.iconTone} text-white shadow-[0_16px_34px_rgba(93,113,255,0.22)]`}>
        <feature.icon className="size-8" />
      </span>
      <h3 className="relative text-xl font-black tracking-[-0.035em] text-[#172033]">{feature.title}</h3>
      <p className="relative mt-3 min-h-14 leading-7 text-[#70798b]">{feature.desc}</p>
      <div className="relative mt-5 flex flex-wrap gap-2">
        {feature.tags.map((tag) => (
          <span key={tag} className="rounded-xl bg-white/60 px-3 py-1.5 text-xs font-black text-[#38ad9c] shadow-sm backdrop-blur-xl">
            {tag}
          </span>
        ))}
      </div>
    </article>
  )
}

function AnalyticsShowcase() {
  return (
    <div className="relative rounded-[2.35rem] border border-white/90 bg-white/62 p-5 shadow-[0_32px_110px_rgba(93,113,180,0.18)] backdrop-blur-2xl">
      <div className="absolute -inset-5 -z-10 rounded-[3rem] bg-[linear-gradient(135deg,rgba(255,255,255,0.4),rgba(111,139,255,0.14),rgba(80,218,188,0.18))] blur-2xl" />
      <div className="mb-5 flex flex-wrap items-center justify-between gap-4">
        <h3 className="text-xl font-black text-[#172033]">数据分析</h3>
        <div className="flex flex-wrap gap-2 text-xs font-black text-[#737b8d]">
          {["今日", "近7天", "近30天", "自定义"].map((item, index) => (
            <span key={item} className={`rounded-xl px-3 py-2 ${index === 0 ? "bg-[#edf3ff] text-[#627cff]" : "bg-white/58"}`}>
              {item}
            </span>
          ))}
        </div>
      </div>
      <div className="grid gap-3 sm:grid-cols-4">
        {analysisStats.map((stat) => (
          <div key={stat.label} className="rounded-[1.35rem] border border-white/70 bg-white/64 p-4 shadow-[0_12px_34px_rgba(93,113,255,0.08)]">
            <div className="text-xs font-bold text-[#858ca0]">{stat.label}</div>
            <div className="mt-2 text-2xl font-black tracking-[-0.04em] text-[#172033]">{stat.value}</div>
            <div className="mt-1 text-xs font-black text-[#29c4a8]">{stat.delta}</div>
          </div>
        ))}
      </div>
      <div className="mt-4 grid gap-4 lg:grid-cols-[1fr_0.42fr]">
        <div className="rounded-[1.6rem] border border-white/70 bg-white/64 p-5 shadow-[0_12px_34px_rgba(93,113,255,0.08)]">
          <h4 className="mb-4 font-black text-[#172033]">客户增长趋势</h4>
          <LargeLineChart />
        </div>
        <div className="rounded-[1.6rem] border border-white/70 bg-white/64 p-5 shadow-[0_12px_34px_rgba(93,113,255,0.08)]">
          <h4 className="mb-5 font-black text-[#172033]">渠道分布</h4>
          <div className="mx-auto mb-6 flex size-32 items-center justify-center rounded-full bg-[conic-gradient(#7d6bff_0_45%,#52cfe0_45%_70%,#55d6a8_70%_90%,#dfe7ff_90%)] shadow-[0_16px_34px_rgba(93,113,255,0.16)]">
            <div className="size-20 rounded-full bg-white/95" />
          </div>
          <div className="space-y-3 text-sm font-semibold text-[#727b8d]">
            {[["微信", "45%"], ["企微", "25%"], ["企业微信", "20%"], ["个人微信", "10%"]].map(([name, value]) => (
              <div key={name} className="flex justify-between">
                <span>{name}</span>
                <span className="font-black text-[#172033]">{value}</span>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}

function FinalCta() {
  return (
    <div className="relative mx-auto max-w-[1320px] overflow-hidden rounded-[2.75rem] border border-white/85 bg-white/58 p-8 shadow-[0_34px_110px_rgba(93,113,180,0.18)] backdrop-blur-2xl sm:p-12">
      <div className="absolute inset-0 bg-[radial-gradient(circle_at_82%_45%,rgba(132,105,255,0.22),transparent_26%),radial-gradient(circle_at_30%_80%,rgba(75,219,221,0.18),transparent_34%)]" />
      <div className="absolute -bottom-28 -left-24 h-80 w-[960px] rounded-full bg-[conic-gradient(from_180deg,rgba(96,219,222,0.28),rgba(152,115,255,0.28),rgba(255,255,255,0),rgba(96,219,222,0.28))] blur-2xl wb-slide" />
      <div className="relative grid gap-10 lg:grid-cols-[1fr_0.78fr] lg:items-center">
        <div>
          <h2 className="text-4xl font-black tracking-[-0.055em] text-[#111827] sm:text-5xl">开启您的客户运营新纪元</h2>
          <p className="mt-4 text-lg font-medium text-[#727b8d]">知悉微宝，让数字员工成为您最可靠的一线伙伴。</p>
          <div className="mt-8 flex flex-col gap-4 sm:flex-row">
            <Link
              href="/dashboard/login"
              className="inline-flex h-14 items-center justify-center gap-3 rounded-full bg-gradient-to-r from-[#30d5d0] via-[#6284ff] to-[#8a62ff] px-8 font-black text-white shadow-[0_20px_46px_rgba(99,122,255,0.32)]"
            >
              免费试用
              <ArrowRight className="size-5" />
            </Link>
            <a
              href="#features"
              className="inline-flex h-14 items-center justify-center rounded-full border border-[#ccd8ff] bg-white/62 px-8 font-black text-[#6174ee] backdrop-blur-2xl"
            >
              预约演示
            </a>
          </div>
        </div>
        <div className="relative min-h-60">
          <div className="absolute right-8 top-4 flex size-44 items-center justify-center rounded-full border border-white/80 bg-white/55 shadow-[inset_0_1px_0_rgba(255,255,255,0.85),0_30px_70px_rgba(92,113,255,0.2)] backdrop-blur-xl wb-rise">
            <div className="flex size-28 items-center justify-center rounded-[2.2rem] bg-gradient-to-br from-[#8e72ff] to-[#5f7dff] text-white shadow-[0_18px_42px_rgba(92,113,255,0.28)]">
              <MessageCircle className="size-12" />
            </div>
          </div>
          <span className="absolute left-16 top-12 size-6 rounded-full bg-white/75 shadow-[0_10px_30px_rgba(92,113,255,0.18)]" />
          <span className="absolute bottom-8 right-2 size-8 rounded-full bg-white/70 shadow-[0_10px_30px_rgba(92,113,255,0.18)]" />
        </div>
      </div>
    </div>
  )
}

function Footer() {
  return (
    <footer id="resources" className="px-5 pb-8 sm:px-8 xl:px-10">
      <div className="mx-auto grid max-w-[1320px] gap-6 rounded-[2rem] border border-white/75 bg-white/42 px-6 py-6 text-sm text-[#7c8496] shadow-sm backdrop-blur-2xl lg:grid-cols-[1fr_2fr_1fr] lg:items-center">
        <div className="flex items-center gap-3">
          <LogoMark small />
          <span>
            <span className="block font-black text-[#172033]">知悉微宝</span>
            <span className="text-xs font-semibold">Agent Desk</span>
          </span>
        </div>
        <div className="grid gap-4 sm:grid-cols-4">
          {assurances.map((item) => (
            <div key={item.title} className="flex items-center gap-3">
              <span className="flex size-10 items-center justify-center rounded-2xl bg-white/70 text-[#667cff]">
                <item.icon className="size-5" />
              </span>
              <span>
                <span className="block font-black text-[#172033]">{item.title}</span>
                <span className="text-xs font-medium">{item.desc}</span>
              </span>
            </div>
          ))}
        </div>
        <div className="flex gap-5 lg:justify-end">
          <Link href="/legal/privacy" className="hover:text-[#667cff]">隐私政策</Link>
          <Link href="/legal/terms" className="hover:text-[#667cff]">服务条款</Link>
        </div>
      </div>
    </footer>
  )
}

function SectionHeading({ kicker, title, desc }: { kicker: string; title: string; desc: string }) {
  return (
    <div className="mx-auto max-w-4xl text-center">
      <p className="text-sm font-black uppercase tracking-[0.24em] text-[#667cff]">{kicker}</p>
      <h2 className="mt-4 whitespace-pre-line text-balance text-4xl font-black leading-tight tracking-[-0.055em] text-[#111827] sm:text-5xl">{title}</h2>
      <p className="mt-4 text-lg font-medium text-[#757c8d]">{desc}</p>
    </div>
  )
}

function LogoMark({ small = false }: { small?: boolean }) {
  return (
    <span className={`${small ? "size-9 rounded-xl" : "size-11 rounded-2xl"} relative flex items-center justify-center bg-gradient-to-br from-[#5df0c6] via-[#5f8cff] to-[#9c6dff] shadow-[0_14px_34px_rgba(105,119,255,0.26)]`}>
      <span className={`${small ? "size-4" : "size-5"} absolute rounded-md bg-white/85 blur-[1px]`} />
      <Sparkles className={`${small ? "size-4" : "size-5"} relative text-[#6b63ff]`} />
    </span>
  )
}

function PageAura() {
  return (
    <>
      <div className="absolute inset-0 -z-30 bg-[radial-gradient(circle_at_8%_8%,rgba(176,154,255,0.33),transparent_28%),radial-gradient(circle_at_88%_14%,rgba(121,220,255,0.28),transparent_31%),linear-gradient(135deg,#ffffff_0%,#f8faff_42%,#eef7ff_100%)]" />
      <div className="absolute bottom-[-40px] left-[-8%] -z-20 h-[430px] w-[920px] rotate-[-7deg] rounded-full bg-[conic-gradient(from_210deg,rgba(87,216,228,0.34),rgba(145,113,255,0.27),rgba(255,255,255,0),rgba(87,216,228,0.34))] blur-2xl" />
      <div className="absolute bottom-[-18px] left-0 -z-10 h-[320px] w-[1060px] rounded-[50%] bg-[linear-gradient(105deg,rgba(95,225,222,0.25),rgba(255,255,255,0.02),rgba(145,113,255,0.28),transparent)] blur-xl wb-slide" />
      <div className="absolute -right-24 top-20 -z-20 h-[420px] w-[720px] rotate-[-8deg] rounded-[50%] bg-[linear-gradient(120deg,rgba(255,255,255,0.16),rgba(144,128,255,0.14),rgba(84,216,224,0.2))] blur-3xl" />
      <div className="absolute bottom-[70px] left-[-120px] -z-10 h-44 w-[760px] rotate-[-13deg] rounded-[100%] border border-white/35 bg-[linear-gradient(100deg,transparent,rgba(86,218,224,0.22),rgba(144,112,255,0.18),transparent)] blur-sm wb-slide" />
      <div className="absolute bottom-[28px] left-[40px] -z-10 h-28 w-[620px] rotate-[-16deg] rounded-[100%] border border-white/40 bg-[linear-gradient(100deg,transparent,rgba(255,255,255,0.5),rgba(123,110,255,0.18),transparent)] blur-[2px]" />
      <div className="absolute bottom-[115px] left-[160px] -z-10 h-16 w-[420px] rotate-[-18deg] rounded-[100%] bg-[linear-gradient(90deg,transparent,rgba(104,230,218,0.24),rgba(255,255,255,0.28),transparent)] blur-[1px]" />
    </>
  )
}

function MiniLineChart() {
  const pointsA = "0,150 45,128 90,116 135,84 180,104 225,52 270,120 315,92 360,132 405,72"
  const pointsB = "0,172 45,158 90,148 135,132 180,138 225,112 270,124 315,88 360,96 405,42"
  return (
    <svg viewBox="0 0 420 190" className="h-56 w-full overflow-visible">
      {[40, 80, 120, 160].map((y) => (
        <line key={y} x1="0" x2="420" y1={y} y2={y} stroke="#e7ecff" strokeWidth="1" />
      ))}
      <polyline points={pointsA} fill="none" stroke="#6e8bff" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" />
      <polyline points={pointsB} fill="none" stroke="#22c7a8" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" />
      {[0, 45, 90, 135, 180, 225, 270, 315, 360, 405].map((x, index) => (
        <circle key={x} cx={x} cy={[150, 128, 116, 84, 104, 52, 120, 92, 132, 72][index]} r="4" fill="#6e8bff" />
      ))}
    </svg>
  )
}

function LargeLineChart() {
  return (
    <svg viewBox="0 0 620 300" className="h-80 w-full overflow-visible">
      <defs>
        <linearGradient id="homepage-area" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="#25c7aa" stopOpacity="0.22" />
          <stop offset="100%" stopColor="#25c7aa" stopOpacity="0" />
        </linearGradient>
      </defs>
      {[50, 100, 150, 200, 250].map((y) => (
        <line key={y} x1="0" x2="620" y1={y} y2={y} stroke="#e7ecff" strokeWidth="1" />
      ))}
      <path d="M0 244 C80 210 110 230 160 190 C230 132 260 194 320 142 C390 82 420 160 470 102 C530 36 570 50 620 24 L620 300 L0 300 Z" fill="url(#homepage-area)" />
      <path d="M0 244 C80 210 110 230 160 190 C230 132 260 194 320 142 C390 82 420 160 470 102 C530 36 570 50 620 24" fill="none" stroke="#25c7aa" strokeWidth="4" strokeLinecap="round" />
      <circle cx="470" cy="102" r="6" fill="#25c7aa" />
      <g transform="translate(340 72)">
        <rect width="130" height="72" rx="18" fill="white" opacity="0.92" />
        <text x="18" y="30" fill="#64748b" fontSize="14">05-18</text>
        <text x="18" y="54" fill="#172033" fontSize="14" fontWeight="700">新增客户：1,247</text>
      </g>
    </svg>
  )
}

function SoftBlob({ className }: { className: string }) {
  return <div className={`pointer-events-none absolute -z-10 rounded-full opacity-40 blur-3xl ${className}`} />
}
