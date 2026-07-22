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

const surface = {
  shell:
    "border border-white/80 bg-white/70 shadow-[0_28px_90px_rgba(70,92,146,0.13)] backdrop-blur-2xl",
  panel:
    "border border-white/75 bg-white/76 shadow-[0_16px_44px_rgba(70,92,146,0.08)]",
  subtle:
    "border border-[#dfe8f7]/90 bg-white/66 shadow-[0_12px_30px_rgba(70,92,146,0.06)]",
}

const navItems = [
  { label: "产品功能", href: "#features" },
  { label: "解决方案", href: "#solution" },
  { label: "客户案例", href: "#customers" },
  { label: "价格方案", href: "#pricing" },
  { label: "资源中心", href: "#resources" },
]

const heroPoints = ["专为微信和企业微信打造", "一客一群专属服务", "重塑企业客户运营"]

const heroStats = [
  { value: "7x24h", label: "持续接待" },
  { value: "1客1群", label: "专属运营" },
  { value: "98.6%", label: "问题解决率" },
]

const heroProofs = ["微信/企微专属", "AI接待中枢", "客户运营闭环"]

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
  { label: "智能回复", icon: MessageCircle, tone: "from-[#5c7cff] to-[#7b66e8]" },
  { label: "智能标签", icon: Tag, tone: "from-[#52d0c8] to-[#35abc8]" },
  { label: "人工转接", icon: CircleUserRound, tone: "from-[#6d86f2] to-[#8c73db]" },
  { label: "语音回复", icon: Mic, tone: "from-[#59c9f6] to-[#5d82e8]" },
  { label: "朋友圈发布", icon: CalendarDays, tone: "from-[#f5a86a] to-[#ee7e86]" },
  { label: "自动加好友", icon: UserPlus, tone: "from-[#4bd7b8] to-[#5aaee8]" },
  { label: "智能欢迎语", icon: Bot, tone: "from-[#8f73e8] to-[#5f77e8]" },
]

const featureGroups = [
  { key: "service", label: "智能接待", summary: "自动回复、语音理解、人工转接，保证一线服务不断线。" },
  { key: "marketing", label: "营销自动化", summary: "朋友圈发布、自动加好友、欢迎语触达，让运营动作持续发生。" },
  { key: "growth", label: "客户运营", summary: "标签沉淀、画像分层、跟进建议，把客户关系经营成资产。" },
]

const features = [
  {
    title: "智能回复",
    desc: "理解客户问题，拟人回复，7x24小时在线服务。",
    icon: MessageCircle,
    tone: "from-[#eef3ff] to-[#f6f1ff]",
    iconTone: "from-[#5c7cff] to-[#7b66e8]",
    tags: ["多平台集成", "智能学习"],
  },
  {
    title: "智能标签",
    desc: "自动识别客户意图，精准打标，补全用户画像。",
    icon: Tag,
    tone: "from-[#effdf7] to-[#f4f6ff]",
    iconTone: "from-[#4bd7b8] to-[#35abc8]",
    tags: ["自动分类", "精准画像", "智能推荐"],
  },
  {
    title: "人工转接",
    desc: "复杂问题无缝接入人工，无缝衔接，消息不断线。",
    icon: UserPlus,
    tone: "from-[#f2f7ff] to-[#f7f1ff]",
    iconTone: "from-[#6b88ef] to-[#8c73db]",
    tags: ["智能判断", "优先分配", "流畅接入"],
  },
  {
    title: "语音回复",
    desc: "语音消息自动转文字，并智能组织自然回复。",
    icon: Mic,
    tone: "from-[#f7f2ff] to-[#f6fbff]",
    iconTone: "from-[#59c9f6] to-[#5d82e8]",
    tags: ["多种音色", "语音识别", "自然流畅"],
  },
  {
    title: "朋友圈发布",
    desc: "批量发布朋友圈内容，定时发布，营销不停歇。",
    icon: Megaphone,
    tone: "from-[#f1fff9] to-[#f8fbff]",
    iconTone: "from-[#59d7ad] to-[#77c977]",
    tags: ["批量发布", "定时发布", "素材库"],
  },
  {
    title: "自动加好友",
    desc: "智能自动添加好友，拓展客户池，安全合规操作。",
    icon: UserPlus,
    tone: "from-[#effcf9] to-[#f5f4ff]",
    iconTone: "from-[#4bd7b8] to-[#5aaee8]",
    tags: ["批量导入", "自动验证", "安全合规"],
  },
  {
    title: "智能欢迎语",
    desc: "新好友自动发送欢迎语，第一时间建立联系。",
    icon: Bot,
    tone: "from-[#f4f1ff] to-[#f8fbff]",
    iconTone: "from-[#8f73e8] to-[#5f77e8]",
    tags: ["100%自动", "个性化内容", "延迟发送"],
  },
]

const featureSets = {
  service: [features[0], features[2], features[3], features[1]],
  marketing: [features[4], features[5], features[6], features[1]],
  growth: [features[1], features[6], features[5], features[0]],
}

const marketingPoints: Array<{ title: string; desc: string; icon: ComponentType<{ className?: string }> }> = [
  { title: "营销自动化", desc: "全链路营销流程自动化，提升转化效率", icon: MousePointer2 },
  { title: "数据分析", desc: "多维度数据洞察，优化运营策略", icon: DatabaseZap },
  { title: "效果追踪", desc: "实时追踪运营效果，驱动持续增长", icon: LineChart },
]

const journeySteps = [
  {
    title: "接待",
    desc: "统一承接微信与企微消息，自动识别客户意图。",
    icon: MessageCircle,
  },
  {
    title: "分层",
    desc: "按咨询内容、群关系与历史行为沉淀客户画像。",
    icon: Tag,
  },
  {
    title: "运营",
    desc: "围绕一客一群生成跟进动作、欢迎语与朋友圈内容。",
    icon: UsersRound,
  },
  {
    title: "复盘",
    desc: "把会话质量、转化和满意度变成可追踪数据。",
    icon: LineChart,
  },
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
  const visibleFeatures = useMemo(
    () => featureSets[activeGroup as keyof typeof featureSets] ?? featureSets.service,
    [activeGroup]
  )

  return (
    <main className="min-h-screen overflow-hidden bg-[#f8fbff] text-[#182033] selection:bg-[#7b8cff]/20">
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
        @keyframes wb-reveal {
          from { transform: translateY(18px); opacity: 0; }
          to { transform: translateY(0); opacity: 1; }
        }
        @keyframes wb-sheen {
          0% { transform: translateX(-140%) skewX(-12deg); opacity: 0; }
          18% { opacity: .55; }
          46%, 100% { transform: translateX(160%) skewX(-12deg); opacity: 0; }
        }
        @keyframes wb-draw {
          from { stroke-dashoffset: 860; }
          to { stroke-dashoffset: 0; }
        }
        @keyframes wb-fade-up {
          from { transform: translateY(8px); opacity: 0; }
          to { transform: translateY(0); opacity: 1; }
        }
        html { scroll-behavior: smooth; }
        .wb-float { animation: wb-float 8s ease-in-out infinite; }
        .wb-rise { animation: wb-rise 7s ease-in-out infinite; }
        .wb-glow { animation: wb-glow 5s ease-in-out infinite; }
        .wb-slide { animation: wb-slide 12s ease-in-out infinite alternate; }
        .wb-reveal { animation: wb-reveal .8s cubic-bezier(.2,.8,.2,1) both; }
        .wb-chart-line {
          stroke-dasharray: 860;
          stroke-dashoffset: 860;
          animation: wb-draw 1.25s cubic-bezier(.2,.8,.2,1) .18s both;
        }
        .wb-chart-point { animation: wb-fade-up .55s cubic-bezier(.2,.8,.2,1) .55s both; }
        .wb-delay-1 { animation-delay: .12s; }
        .wb-delay-2 { animation-delay: .22s; }
        .wb-sheen::after {
          content: "";
          position: absolute;
          inset: 0;
          width: 38%;
          background: linear-gradient(90deg, transparent, rgba(255,255,255,.72), transparent);
          animation: wb-sheen 7s cubic-bezier(.2,.8,.2,1) infinite;
          pointer-events: none;
        }
        @media (prefers-reduced-motion: reduce) {
          .wb-float, .wb-rise, .wb-glow, .wb-slide, .wb-reveal, .wb-sheen::after {
            animation: none !important;
          }
          html { scroll-behavior: auto; }
          .wb-chart-line, .wb-chart-point { animation: none !important; stroke-dashoffset: 0; }
        }
      `}</style>

      <section className="relative isolate min-h-[740px] px-5 pb-4 pt-6 sm:min-h-[790px] sm:px-8 lg:min-h-[820px] xl:px-10">
        <PageAura />
        <Header />

        <div className="relative mx-auto grid max-w-[1400px] gap-10 pb-3 pt-10 lg:grid-cols-[0.82fr_1.18fr] lg:items-center lg:pt-14">
          <div className="relative z-10 wb-reveal">
            <div className="mb-6 inline-flex items-center gap-2 rounded-full border border-white/85 bg-white/68 px-4 py-2 text-sm font-bold text-[#5764ce] shadow-[0_14px_42px_rgba(99,112,180,0.1)] backdrop-blur-2xl">
              <span className="size-2 rounded-full bg-[#62dec9] shadow-[0_0_20px_rgba(98,222,201,0.8)]" />
              微信 / 企微数字员工平台
            </div>

            <h1 className="max-w-[650px] text-balance text-[2.9rem] font-black leading-[1.04] tracking-normal text-[#101827] sm:text-[4.1rem] xl:text-[4.55rem]">
              微信/企微
              <span className="block">AI数字员工</span>
              <span className="mt-2 block text-[#101827]">7x24小时接管</span>
              <span className="block bg-gradient-to-r from-[#23b8bd] via-[#5d7af0] to-[#8a69dc] bg-clip-text text-transparent drop-shadow-[0_18px_26px_rgba(100,116,220,0.16)]">
                微信客服
              </span>
            </h1>

            <div className="mt-6 flex flex-wrap gap-2.5">
              {heroProofs.map((proof) => (
                <span key={proof} className="rounded-full border border-white/80 bg-white/60 px-3.5 py-2 text-xs font-black text-[#606b80] shadow-[0_10px_26px_rgba(70,92,146,0.07)] backdrop-blur-xl">
                  {proof}
                </span>
              ))}
            </div>

            <div className="mt-7 space-y-3.5">
              {heroPoints.map((point) => (
                <div key={point} className="flex items-center gap-3 text-[1.05rem] font-semibold text-[#606a7d]">
                  <span className="flex size-6 items-center justify-center rounded-full border border-[#9fb0ff]/70 bg-white/74 text-[#657dff] shadow-[0_10px_24px_rgba(101,125,255,0.12)]">
                    <CheckCircle2 className="size-4" />
                  </span>
                  {point}
                </div>
              ))}
            </div>

            <div className="mt-9 flex flex-col gap-4 sm:flex-row">
              <Link
                href="/dashboard/login"
                className="group inline-flex h-14 items-center justify-center gap-3 rounded-full bg-gradient-to-r from-[#25c9bd] via-[#5e7ce8] to-[#8569d8] px-8 text-base font-black text-white shadow-[0_20px_46px_rgba(77,109,210,0.28)] transition duration-300 hover:-translate-y-1 hover:shadow-[0_26px_60px_rgba(77,109,210,0.38)] focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-[#90a2ff]/35"
              >
                免费试用
                <ArrowRight className="size-5 transition group-hover:translate-x-1" />
              </Link>
              <a
                href="#features"
                className="inline-flex h-14 items-center justify-center rounded-full border border-[#ccd8ff] bg-white/68 px-8 text-base font-black text-[#6174ee] shadow-[0_16px_40px_rgba(108,123,210,0.1)] backdrop-blur-2xl transition duration-300 hover:-translate-y-1 hover:border-[#9a8cff] hover:bg-white/88 focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-[#90a2ff]/25"
              >
                预约演示
              </a>
            </div>

            <div className="mt-8 grid max-w-md grid-cols-3 gap-3">
              {heroStats.map((item) => (
                <div key={item.label} className={`rounded-2xl px-4 py-3 ${surface.subtle}`}>
                  <div className="text-lg font-black text-[#172033]">{item.value}</div>
                  <div className="mt-1 text-[11px] font-bold text-[#7a8498]">{item.label}</div>
                </div>
              ))}
            </div>
          </div>

          <HeroDashboard />
        </div>
      </section>

      <section id="features" className="relative px-5 py-20 sm:px-8 xl:px-10">
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
                aria-pressed={currentGroup.key === group.key}
                onClick={() => setActiveGroup(group.key)}
                className={`inline-flex items-center gap-2 rounded-full border px-8 py-3 text-sm font-black transition duration-300 focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-[#90a2ff]/25 ${
                  currentGroup.key === group.key
                    ? "border-white bg-white text-[#576cef] shadow-[0_18px_42px_rgba(92,112,220,0.15)]"
                    : "border-[#e5ebff] bg-white/50 text-[#737a8c] hover:border-[#b9c5ff] hover:bg-white/76"
                }`}
              >
                <span className="size-2 rounded-full bg-current opacity-50" />
                {group.label}
              </button>
            ))}
          </div>
          <p className="mx-auto mt-5 max-w-2xl text-center text-base font-medium leading-7 text-[#667085]">{currentGroup.summary}</p>

          <div className="mt-12 grid gap-6 md:grid-cols-2 xl:grid-cols-4">
            {visibleFeatures.map((feature, index) => (
              <FeatureCard key={`${activeGroup}-${feature.title}`} feature={feature} delay={index} />
            ))}
          </div>
        </div>
      </section>

      <section className="relative px-5 py-16 sm:px-8 xl:px-10">
        <div className="mx-auto max-w-[1320px]">
          <div className={`relative overflow-hidden rounded-[2rem] px-6 py-8 sm:px-9 lg:px-10 ${surface.shell}`}>
            <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_18%_8%,rgba(255,255,255,0.92),transparent_25%),radial-gradient(circle_at_86%_78%,rgba(87,214,198,0.10),transparent_28%)]" />
            <div className="pointer-events-none absolute inset-x-8 top-1/2 hidden h-px bg-gradient-to-r from-transparent via-[#9aa8ee]/50 to-transparent lg:block" />
            <div className="relative grid gap-5 lg:grid-cols-4">
              {journeySteps.map((step, index) => (
                <div key={step.title} className="group relative rounded-[1.4rem] border border-white/70 bg-white/56 p-5 shadow-[0_12px_36px_rgba(83,104,158,0.07)] transition duration-500 hover:-translate-y-1 hover:bg-white/74 wb-reveal">
                  <div className="mb-5 flex items-center justify-between">
                    <span className="flex size-12 items-center justify-center rounded-2xl bg-gradient-to-br from-[#f4fbfb] to-[#eef2ff] text-[#586fe3] shadow-[inset_0_1px_0_rgba(255,255,255,0.9),0_12px_24px_rgba(95,116,210,0.1)]">
                      <step.icon className="size-5" />
                    </span>
                    <span className="font-mono text-xs font-black text-[#a0a9bc]">0{index + 1}</span>
                  </div>
                  <h3 className="text-xl font-black text-[#172033]">{step.title}</h3>
                  <p className="mt-2 min-h-14 text-sm font-medium leading-6 text-[#717b8d]">{step.desc}</p>
                </div>
              ))}
            </div>
          </div>
        </div>
      </section>

      <section id="solution" className="relative px-5 py-20 sm:px-8 xl:px-10">
        <SoftBlob className="-left-40 top-14 h-[560px] w-[560px] bg-[#dbcaff]" />
        <SoftBlob className="right-0 top-24 h-[520px] w-[620px] bg-[#c8fff1]" />
        <div className="relative mx-auto grid max-w-[1320px] gap-14 lg:grid-cols-[0.72fr_1.28fr] lg:items-center">
          <div>
            <p className="text-sm font-black uppercase tracking-normal text-[#6877ef]">Marketing & Data</p>
            <h2 className="mt-5 text-4xl font-black leading-tight tracking-normal text-[#111827] sm:text-5xl">
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
          <p className="text-sm font-black uppercase tracking-normal text-[#6877ef]">Trusted by Teams</p>
          <h2 className="mt-4 text-3xl font-black tracking-normal text-[#111827]">众多企业信赖的数字员工平台</h2>
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
    <header className="relative z-20 mx-auto flex max-w-[1320px] items-center justify-between rounded-full border border-white/80 bg-white/55 px-4 py-3 shadow-[0_18px_60px_rgba(97,116,180,0.12)] backdrop-blur-2xl wb-reveal">
      <Link href="/" className="flex items-center gap-3">
        <LogoMark />
        <span className="leading-tight">
          <span className="block text-lg font-black tracking-normal text-[#111827]">知悉微宝</span>
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
          className="rounded-full bg-gradient-to-r from-[#25c9bd] via-[#5e7ce8] to-[#8569d8] px-5 py-2.5 text-sm font-black text-white shadow-[0_14px_34px_rgba(77,109,210,0.24)] transition hover:-translate-y-0.5"
        >
          免费试用
        </Link>
      </div>
    </header>
  )
}

function HeroDashboard() {
  return (
    <div className="relative mx-auto w-full max-w-[835px] wb-float wb-reveal wb-delay-1">
      <div className="absolute -inset-7 rounded-[3.4rem] bg-[linear-gradient(135deg,rgba(255,255,255,0.65),rgba(133,123,255,0.18),rgba(80,218,220,0.24))] blur-2xl wb-glow" />
      <div className="absolute -bottom-16 -left-20 h-40 w-80 rotate-[-12deg] rounded-full bg-[linear-gradient(90deg,rgba(97,222,224,0.28),rgba(149,112,255,0.22),transparent)] blur-xl wb-slide" />
      <div className="relative overflow-hidden rounded-[2.25rem] border border-white/90 bg-white/62 p-5 shadow-[0_32px_110px_rgba(93,113,180,0.22)] backdrop-blur-2xl wb-sheen">
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
                  <div className="mt-2 text-2xl font-black tracking-normal text-[#172033]">{metric.value}</div>
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
              <div className="grid grid-cols-3 gap-3 sm:grid-cols-7">
                {quickActions.map((action) => (
                  <div key={action.label} className="text-center">
                    <span className={`mx-auto flex size-11 items-center justify-center rounded-[1.05rem] bg-gradient-to-br ${action.tone} text-white shadow-[0_12px_24px_rgba(93,113,255,0.16)]`}>
                      <action.icon className="size-5" />
                    </span>
                    <span className="mt-2 block whitespace-nowrap text-[10px] font-black leading-tight text-[#667085] sm:text-[11px]">
                      {action.label}
                    </span>
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

function FeatureCard({ feature, delay = 0 }: { feature: (typeof features)[number]; delay?: number }) {
  return (
    <article
      className={`group relative overflow-hidden rounded-[2rem] border border-white/85 bg-gradient-to-br ${feature.tone} p-6 shadow-[0_22px_64px_rgba(93,113,255,0.09)] backdrop-blur-2xl transition duration-500 hover:-translate-y-2 hover:shadow-[0_34px_90px_rgba(93,113,255,0.17)] wb-reveal`}
      style={{ animationDelay: `${delay * 0.06}s` }}
    >
      <div className="absolute right-[-30px] top-[-30px] size-32 rounded-full bg-white/50 blur-2xl transition group-hover:scale-125" />
      <span className={`relative mb-8 flex size-16 items-center justify-center rounded-[1.45rem] bg-gradient-to-br ${feature.iconTone} text-white shadow-[0_16px_34px_rgba(93,113,255,0.22)]`}>
        <feature.icon className="size-8" />
      </span>
      <h3 className="relative text-xl font-black tracking-normal text-[#172033]">{feature.title}</h3>
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
    <div className="relative rounded-[2.35rem] border border-white/90 bg-white/62 p-5 shadow-[0_32px_110px_rgba(93,113,180,0.18)] backdrop-blur-2xl wb-reveal">
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
            <div className="mt-2 text-2xl font-black tracking-normal text-[#172033]">{stat.value}</div>
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
    <div className="relative mx-auto max-w-[1320px] overflow-hidden rounded-[2.75rem] border border-white/85 bg-white/58 p-8 shadow-[0_34px_110px_rgba(93,113,180,0.18)] backdrop-blur-2xl sm:p-12 wb-sheen">
      <div className="absolute inset-0 bg-[radial-gradient(circle_at_82%_45%,rgba(132,105,255,0.18),transparent_26%),radial-gradient(circle_at_30%_80%,rgba(75,219,221,0.16),transparent_34%),linear-gradient(135deg,rgba(255,255,255,0.72),rgba(248,250,255,0.22))]" />
      <div className="absolute -bottom-28 -left-24 h-80 w-[960px] rounded-full bg-[conic-gradient(from_180deg,rgba(96,219,222,0.28),rgba(152,115,255,0.28),rgba(255,255,255,0),rgba(96,219,222,0.28))] blur-2xl wb-slide" />
      <div className="relative grid gap-10 lg:grid-cols-[1fr_0.78fr] lg:items-center">
        <div>
          <h2 className="text-4xl font-black tracking-normal text-[#111827] sm:text-5xl">开启您的客户运营新纪元</h2>
          <p className="mt-4 text-lg font-medium text-[#727b8d]">知悉微宝，让数字员工成为您最可靠的一线伙伴。</p>
          <div className="mt-8 flex flex-col gap-4 sm:flex-row">
            <Link
              href="/dashboard/login"
              className="inline-flex h-14 items-center justify-center gap-3 rounded-full bg-gradient-to-r from-[#25c9bd] via-[#5e7ce8] to-[#8569d8] px-8 font-black text-white shadow-[0_20px_46px_rgba(77,109,210,0.28)]"
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
      <p className="text-sm font-black uppercase tracking-normal text-[#667cff]">{kicker}</p>
      <h2 className="mt-4 whitespace-pre-line text-balance text-4xl font-black leading-tight tracking-normal text-[#111827] sm:text-5xl">{title}</h2>
      <p className="mt-4 text-lg font-medium text-[#757c8d]">{desc}</p>
    </div>
  )
}

function LogoMark({ small = false }: { small?: boolean }) {
  return (
    <span className={`${small ? "size-9 rounded-xl" : "size-11 rounded-2xl"} relative flex items-center justify-center bg-gradient-to-br from-[#58dcc8] via-[#5d82e8] to-[#8b6ee6] shadow-[0_14px_34px_rgba(91,116,210,0.24)]`}>
      <span className={`${small ? "size-4" : "size-5"} absolute rounded-md bg-white/85 blur-[1px]`} />
      <Sparkles className={`${small ? "size-4" : "size-5"} relative text-[#5e6ee0]`} />
    </span>
  )
}

function PageAura() {
  return (
    <>
      <div className="absolute inset-0 -z-30 bg-[radial-gradient(circle_at_8%_8%,rgba(178,164,232,0.24),transparent_29%),radial-gradient(circle_at_86%_14%,rgba(135,212,232,0.24),transparent_30%),radial-gradient(circle_at_72%_78%,rgba(244,178,154,0.12),transparent_26%),linear-gradient(135deg,#fffdfb_0%,#f8faff_42%,#edf7fb_100%)]" />
      <div className="absolute bottom-[-40px] left-[-8%] -z-20 h-[430px] w-[920px] rotate-[-7deg] rounded-full bg-[conic-gradient(from_210deg,rgba(77,205,202,0.3),rgba(126,112,214,0.22),rgba(244,182,154,0.08),rgba(255,255,255,0),rgba(77,205,202,0.3))] blur-2xl" />
      <div className="absolute bottom-[-18px] left-0 -z-10 h-[320px] w-[1060px] rounded-[50%] bg-[linear-gradient(105deg,rgba(75,213,198,0.22),rgba(255,255,255,0.04),rgba(118,108,215,0.22),transparent)] blur-xl wb-slide" />
      <div className="absolute -right-24 top-20 -z-20 h-[420px] w-[720px] rotate-[-8deg] rounded-[50%] bg-[linear-gradient(120deg,rgba(255,255,255,0.18),rgba(126,112,214,0.12),rgba(118,205,224,0.18))] blur-3xl" />
      <div className="absolute bottom-[70px] left-[-120px] -z-10 h-44 w-[760px] rotate-[-13deg] rounded-[100%] border border-white/35 bg-[linear-gradient(100deg,transparent,rgba(75,213,198,0.2),rgba(126,112,214,0.16),transparent)] blur-sm wb-slide" />
      <div className="absolute bottom-[28px] left-[40px] -z-10 h-28 w-[620px] rotate-[-16deg] rounded-[100%] border border-white/40 bg-[linear-gradient(100deg,transparent,rgba(255,255,255,0.52),rgba(244,182,154,0.12),rgba(118,108,215,0.14),transparent)] blur-[2px]" />
      <div className="absolute bottom-[115px] left-[160px] -z-10 h-16 w-[420px] rotate-[-18deg] rounded-[100%] bg-[linear-gradient(90deg,transparent,rgba(95,218,204,0.22),rgba(255,255,255,0.3),transparent)] blur-[1px]" />
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
      <polyline className="wb-chart-line" points={pointsA} fill="none" stroke="#6e8bff" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" />
      <polyline className="wb-chart-line" points={pointsB} fill="none" stroke="#22c7a8" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" />
      {[0, 45, 90, 135, 180, 225, 270, 315, 360, 405].map((x, index) => (
        <circle className="wb-chart-point" key={x} cx={x} cy={[150, 128, 116, 84, 104, 52, 120, 92, 132, 72][index]} r="4" fill="#6e8bff" />
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
      <path className="wb-chart-line" d="M0 244 C80 210 110 230 160 190 C230 132 260 194 320 142 C390 82 420 160 470 102 C530 36 570 50 620 24" fill="none" stroke="#25c7aa" strokeWidth="4" strokeLinecap="round" />
      <circle className="wb-chart-point" cx="470" cy="102" r="6" fill="#25c7aa" />
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
