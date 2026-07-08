#!/usr/bin/env python3
"""Run a 30-round Reply Runtime Engine scenario evaluation.

This script is intentionally side-effect free: it does not send WeCom messages.
It mirrors the current Reply Runtime decision rules closely enough for regression
review and writes a Markdown record for product/engineering discussion.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from time import perf_counter
from typing import Dict, List


INTENT_GENERAL = "GENERAL_CHAT"
INTENT_WIFI = "FAQ_WIFI"
INTENT_HOTEL = "FAQ_HOTEL"
INTENT_INVOICE = "FAQ_INVOICE"
INTENT_SUPPLIES = "FAQ_SUPPLIES"
INTENT_MINI_PROGRAM = "CHECKIN_MINIPROGRAM"
INTENT_LOCATION = "LOCATION_NAVIGATION"
INTENT_SERVICE = "SERVICE_TASK"
INTENT_HANDOFF = "HANDOFF_REQUEST"
INTENT_HUMAN = "HUMAN_DECISION"


@dataclass
class Store:
    name: str
    phone: str = ""
    location: str = ""
    mini_program: str = ""
    group_ready: bool = True
    fallback_to_hq: bool = True
    has_coordinate: bool = True


@dataclass
class Scenario:
    no: int
    customer: str
    scene: str
    message: str
    knowledge: Dict[str, str] = field(default_factory=dict)
    store: Store = field(default_factory=lambda: STORE_A)
    mode: str = "semi"
    schedule: str = "hq"
    expected: str = ""
    bad_reply: str = ""
    media: str = ""
    need_human: bool = False
    need_resource: bool = False


STORE_A = Store(
    name="丽斯未来酒店·测试店",
    phone="0571-88886666",
    location="浙江省杭州市测试路 88 号",
    mini_program="安心宿小程序 page=/pages/checkin/index?storeId=lis-test",
    group_ready=True,
    fallback_to_hq=True,
    has_coordinate=True,
)

STORE_NO_LOCATION = Store(
    name="丽斯未来酒店·新店",
    phone="0571-66668888",
    mini_program="安心宿小程序 page=/pages/checkin/index?storeId=lis-new",
    group_ready=True,
    fallback_to_hq=False,
    has_coordinate=False,
)


def normalize(text: str) -> str:
    text = text.strip().lower()
    for old in [" ", "\t", "\n", "\r", "，", "。", "？", "?", "！", "!"]:
        text = text.replace(old, "")
    return text


def contains_any(text: str, *values: str) -> bool:
    return any(value and value.lower() in text for value in values)


def classify(text: str) -> str:
    compact = normalize(text)
    if contains_any(compact, "转人工", "转人公", "人工客服", "真人客服", "真人处理", "找真人", "找个人", "找人工", "找客服", "人工处理", "让人来", "叫人来"):
        return INTENT_HANDOFF
    if contains_any(compact, "退款", "退钱", "赔偿", "投诉", "不满意", "差评", "举报", "安全", "害怕", "危险", "门锁", "隐私", "报警", "订单异常", "订单房型", "房型不对", "订的是", "订了", "大床", "双床", "身份证号", "身份证", "比我便宜", "朋友比", "我便宜", "朋友比我便宜", "别人便宜", "比朋友贵", "价格怎么比", "价格不一样", "价差", "太贵"):
        return INTENT_HUMAN
    if contains_any(compact, "wifi", "wi-fi", "wfi", "无线网", "无线密码", "网络密码", "密码", "网咋连", "网连不上", "网络连不上", "网不好", "网也太烂"):
        return INTENT_WIFI
    if contains_any(compact, "发票", "发飘", "专票", "普票", "抬头", "报销票", "报销", "票咋弄", "票怎么开", "开发票"):
        return INTENT_INVOICE
    if contains_any(compact, "剃须刀", "牙刷", "拖鞋", "纸巾", "浴巾", "毛巾", "矿泉水", "瓶水", "两瓶水", "送水", "拿水", "要水", "用品"):
        return INTENT_SUPPLIES
    if contains_any(compact, "送水", "送瓶水", "送两瓶水", "拿水", "要水", "矿泉水送", "送拖鞋", "送牙刷", "送纸巾", "打扫", "保洁", "维修", "漏水", "马桶", "空调坏", "电视坏", "叫醒", "行李", "异味", "不舒服", "帮拿", "拿行李", "e3", "故障", "面板显示"):
        return INTENT_SERVICE
    if contains_any(compact, "入住", "办理入住", "小程序", "安心宿", "重新扫码", "加过你们", "入住码", "自助入住入口"):
        return INTENT_MINI_PROGRAM
    if contains_any(compact, "发定位", "定位发我", "定位也发", "定位发一下", "把定位发", "位置发我", "导航发我", "导航给", "定位和电话", "酒店定位", "门店位置", "你们位置", "酒店在哪里", "酒店在哪", "门店在哪里", "门店在哪", "你们在哪", "你们在哪里", "怎么去", "怎么走", "过去怎么方便", "高铁站过去", "机场过去", "地铁过去", "到酒店多久", "从这到酒店", "到店路线", "导航路线", "酒店地址", "门店地址", "地址发我", "地址在哪", "位置在哪", "定为发我"):
        return INTENT_LOCATION
    if contains_any(compact, "早餐", "早饭", "停车", "车停", "车能停", "停哪", "停哪里", "停车场", "退房", "续住", "押金", "房费", "洗衣", "电视", "投屏", "空调", "热水", "电梯", "前台", "客服电话", "电话", "营业时间", "几点", "几电"):
        return INTENT_HOTEL
    return INTENT_GENERAL


def burst(*lines: str) -> str:
    body = ["客人刚才连续发了几条消息。请按顺序合并理解，不要只回复最后一句："]
    body.extend(f"{index}. [消息] {line}" for index, line in enumerate(lines, 1))
    return "\n".join(body)


def split_questions(text: str) -> List[str]:
    text = text.strip()
    if "客人刚才连续发了几条消息" not in text:
        return [text] if text else []
    ret: List[str] = []
    for line in text.splitlines():
        line = line.strip()
        if not line or "客人刚才连续发了几条消息" in line or "请按顺序合并理解" in line or "不要只回复最后一句" in line:
            continue
        if "]" in line:
            line = line.split("]", 1)[1].strip()
        elif "." in line[:4]:
            line = line.split(".", 1)[1].strip()
        ret.append(line)
    deduped: List[str] = []
    seen = set()
    for item in ret:
        if item and item not in seen:
            deduped.append(item)
            seen.add(item)
    return deduped[-6:]


def route_for(item: Scenario) -> str:
    if item.mode == "full":
        return "hq_web_agent"
    if item.mode == "none":
        return "store_wecom_group"
    if item.schedule == "hq":
        return "hq_web_agent"
    if item.schedule == "store" and item.store.group_ready:
        return "store_wecom_group"
    if item.store.fallback_to_hq:
        return "hq_web_agent"
    return "phone_fallback"


def humanize_knowledge(intent: str, answer: str) -> str:
    if intent == INTENT_WIFI:
        return answer.replace("WiFi：", "WiFi 是 ")
    if intent == INTENT_SUPPLIES:
        return f"{answer} 不用等人送，直接过去拿更快。"
    return answer


def service_reply(text: str) -> str:
    compact = normalize(text)
    if contains_any(compact, "送水", "要水", "送拖鞋", "送牙刷", "纸巾", "矿泉水"):
        return "这类用品先按门店领取规则走；如果房间里确实没有，你告诉我房号，我再帮你接给同事。"
    if contains_any(compact, "漏水", "维修", "坏", "异味", "不舒服"):
        return "这个需要同事现场看，你把房号发我，我接给门店同事处理。"
    return "这个需要门店同事处理，你把房号和具体情况发我。"


def handoff_reply(route: str) -> str:
    if route == "store_wecom_group":
        return "这个我接给门店同事看，别急。"
    if route == "phone_fallback":
        return "这个需要门店确认，我把电话给你，直接联系会更快。"
    return "这个需要同事接手，我帮你转到人工。"


def general_reply(text: str, media: str) -> str:
    compact = normalize(text)
    if contains_any(compact, "谢谢", "感谢"):
        return "不客气，有问题你直接发我。"
    if contains_any(compact, "冷淡", "敷衍"):
        return "收到，我说简单点，但会把事讲清楚。"
    if contains_any(compact, "急"):
        return "看到，你把要处理的事发我，我先帮你看最要紧的。"
    if media:
        return "我看到了，按你刚发的内容继续处理。"
    return "你说，我看着。"


def fixed_fallback(reply: str) -> bool:
    compact = normalize(reply)
    return contains_any(compact, "帮你确认下", "帮您确认下", "先帮你记录", "先帮您记录", "稍后让同事跟进", "不在人工服务时间")


def guard_output(user_text: str, reply: str) -> tuple[str, bool, str]:
    questions = split_questions(user_text)
    intents = [classify(q) for q in questions]
    if fixed_fallback(reply) and any(intent in {INTENT_WIFI, INTENT_HOTEL, INTENT_INVOICE, INTENT_SUPPLIES, INTENT_MINI_PROGRAM, INTENT_LOCATION} for intent in intents):
        parts = []
        for intent in intents:
            if intent == INTENT_WIFI:
                parts.append("WiFi你住哪间房？我看下对应的。")
            elif intent == INTENT_HOTEL:
                parts.append("你问的是门店信息，我按当前门店资料看。")
            elif intent == INTENT_INVOICE:
                parts.append("发票可以在小程序里申请，专票信息按页面填就行。")
            elif intent == INTENT_SUPPLIES:
                parts.append("用品我看下门店放置点，你具体要哪样？")
            elif intent == INTENT_MINI_PROGRAM:
                parts.append("自助入住走小程序就行。")
            elif intent == INTENT_LOCATION:
                parts.append("定位我可以发你。")
        return " ".join(parts), True, "replace_fixed_fallback_for_faq"
    return reply, False, ""


def simulate(item: Scenario) -> dict:
    start = perf_counter()
    questions = split_questions(item.message)
    intents = [classify(q) for q in questions]
    route = "direct_reply"
    knowledge_hit = False
    parts: List[str] = []

    for question, intent in zip(questions, intents):
        if intent in item.knowledge:
            knowledge_hit = True
            parts.append(humanize_knowledge(intent, item.knowledge[intent]))
            if route not in {"hq_web_agent", "store_wecom_group", "phone_fallback"}:
                if intent == INTENT_LOCATION and item.store.has_coordinate:
                    route = "send_location"
                if intent == INTENT_MINI_PROGRAM and item.store.mini_program:
                    route = "send_mini_program"
            continue
        if intent == INTENT_WIFI:
            parts.append("你住哪间房？我看下对应 WiFi。")
        elif intent == INTENT_INVOICE:
            parts.append("发票一般在小程序里申请，专票按页面填抬头、税号和邮箱。")
        elif intent == INTENT_SUPPLIES:
            parts.append("你具体缺哪样？我看下这家店的领取方式。")
        elif intent == INTENT_MINI_PROGRAM:
            if route not in {"hq_web_agent", "store_wecom_group", "phone_fallback"}:
                route = "send_mini_program"
            parts.append("入住走安心宿小程序，我发你当前门店的入口。")
        elif intent == INTENT_LOCATION:
            if item.store.has_coordinate:
                route = "send_location"
                parts.append(f"我把{item.store.name}定位发你，跟着导航走就行。")
            else:
                route = route_for(item)
                parts.append("这家店还没配定位，我让同事补一下准确位置。")
        elif intent == INTENT_SERVICE:
            route = route_for(item)
            parts.append(service_reply(question))
        elif intent in {INTENT_HANDOFF, INTENT_HUMAN}:
            route = route_for(item)
            parts.append(handoff_reply(route))
        else:
            parts.append(general_reply(question, item.media))

    if INTENT_MINI_PROGRAM in intents and item.store.mini_program and route not in {"hq_web_agent", "store_wecom_group", "phone_fallback"}:
        route = "send_mini_program"
    if "电话" in normalize(item.message) and item.store.phone:
        if route not in {"hq_web_agent", "store_wecom_group", "phone_fallback"}:
            route = "disclose_phone"
        parts.append(f"前台电话是 {item.store.phone}。")

    reply = compact(parts)
    guard_source = item.bad_reply or reply
    guarded, changed, reason = guard_output(item.message, guard_source)
    if changed:
        reply = guarded
    latency_ms = (perf_counter() - start) * 1000
    score, problems = score_result(item, intents, route, reply)
    return {
        "questions": questions,
        "intents": intents,
        "knowledge_hit": knowledge_hit,
        "route": route,
        "reply": reply,
        "guard_changed": changed,
        "guard_reason": reason,
        "latency_ms": latency_ms,
        "score": score,
        "problems": problems,
    }


def compact(parts: List[str]) -> str:
    ret: List[str] = []
    seen = set()
    for part in parts:
        part = part.strip()
        if not part or part in seen:
            continue
        ret.append(part)
        seen.add(part)
    return " ".join(ret)


def forbidden_promise(reply: str) -> bool:
    compact_reply = normalize(reply)
    return contains_any(compact_reply, "马上安排", "已经安排", "已安排", "已经通知", "已通知", "已经让同事", "已记录", "已提交", "我给你换", "给你免房费")


def score_result(item: Scenario, intents: List[str], route: str, reply: str) -> tuple[int, List[str]]:
    value = 100
    problems: List[str] = []
    compact_reply = normalize(reply)
    if forbidden_promise(reply):
        value -= 35
        problems.append("包含空口承诺或已处理类话术")
    if item.need_human and not ("agent" in route or "group" in route or route == "phone_fallback"):
        value -= 30
        problems.append("应进入接待路由但未进入")
    if item.need_resource and route == "direct_reply":
        value -= 20
        problems.append("需要资源动作但只生成了普通文本")
    expected_checks = [
        ("WiFi", INTENT_WIFI),
        ("发票", INTENT_INVOICE),
        ("早餐", INTENT_HOTEL),
        ("停车", INTENT_HOTEL),
        ("价格", INTENT_HUMAN),
        ("小程序", INTENT_MINI_PROGRAM),
        ("定位", INTENT_LOCATION),
    ]
    for label, expected_intent in expected_checks:
        if label in item.expected and expected_intent not in intents:
            value -= 20
            problems.append(f"期望{label}场景但未识别为{expected_intent}")
    if "客人刚才连续发了几条消息" in item.message and len(intents) < 2:
        value -= 25
        problems.append("连续消息未拆成多个问题")
    if contains_any(compact_reply, "哈哈", "好的", "嗯嗯") and len(reply) < 8:
        value -= 20
        problems.append("轻互动回复太敷衍")
    if "看不到" in compact_reply and item.media:
        value -= 25
        problems.append("已有媒体理解仍回复看不到")
    if not problems:
        problems.append("通过")
    return max(value, 0), problems


def scenarios() -> List[Scenario]:
    return [
        Scenario(1, "生椰拿铁", "连续多条：WiFi + 发票", burst("WiFi是哪个", "能开专票不"), {INTENT_WIFI: "WiFi：房间号后四位，密码 lis888888。", INTENT_INVOICE: "发票可在安心宿小程序申请，专票按页面填写抬头、税号、邮箱。"}, STORE_A, "semi", "hq", "两件事都答到，不转人工", "WiFi我帮你确认下"),
        Scenario(2, "生椰拿铁", "图片后追问：发票截图", burst("[图片理解] 图片是一张发票信息页，抬头是杭州某某科技有限公司，税号显示完整。", "这个抬头能开专票吗"), {INTENT_INVOICE: "专票可开，公司抬头、税号、邮箱齐全即可提交。"}, STORE_A, "semi", "hq", "结合图片摘要回答专票，不说看不到图", "这个我不太确定，帮你记录一下", "图片摘要"),
        Scenario(3, "泡芙", "低风险用品：矿泉水", "可以送两瓶水到 802 吗", {INTENT_SUPPLIES: "矿泉水在 1 楼前台自取，每间房可免费领取 2 瓶。"}, STORE_A, "semi", "store", "知识库引导自取，不承诺送达", "好的，我马上安排同事送过去"),
        Scenario(4, "泡芙", "用品缺知识：拖鞋", "房间没有拖鞋", {}, STORE_A, "semi", "store", "只追问或进入路由，不编自取点", "先帮你记录，稍后同事跟进"),
        Scenario(5, "其风", "办理入住", "我到门口了，怎么办入住", {INTENT_MINI_PROGRAM: "自助入住在安心宿小程序办理。"}, STORE_A, "full", "hq", "发送带门店参数小程序", "你自己找一下小程序", need_resource=True),
        Scenario(6, "其风", "定位请求", "你们酒店在哪里，定位发我", {}, STORE_A, "full", "hq", "读取门店坐标并发真实定位", "我把地址发你吧", need_resource=True),
        Scenario(7, "其风", "定位缺失", "导航发我一下", {}, STORE_NO_LOCATION, "none", "store", "缺坐标不编，走门店群或电话", "定位我可以发你", need_human=True, need_resource=True),
        Scenario(8, "来一杯生椰拿铁", "轻互动：感谢", "谢谢你", {}, STORE_A, "semi", "hq", "有回应感，不只哈哈", "哈哈"),
        Scenario(9, "来一杯生椰拿铁", "轻互动：嫌冷淡", "你回复好冷淡啊", {}, STORE_A, "semi", "hq", "短句安抚，不机械道歉", "好的"),
        Scenario(10, "小赵", "明确转人工", "帮我转人工", {}, STORE_A, "full", "hq", "全托管走总部网页客服", "我先帮你看看", need_human=True),
        Scenario(11, "小赵", "半托管门店时段转人工", "这个我想找真人处理", {}, STORE_A, "semi", "store", "半托管门店时段通知门店企微群", "转人工了", need_human=True),
        Scenario(12, "小赵", "非托管转人工", "让人来处理一下", {}, STORE_A, "none", "store", "非托管只通知门店群", "总部客服稍后联系你", need_human=True),
        Scenario(13, "阿宁", "严重投诉赔偿", "空调坏一晚上，我要投诉赔偿", {}, STORE_A, "semi", "hq", "人工风险，不能自行定责", "给你免房费可以吗", need_human=True),
        Scenario(14, "阿宁", "维修但缺字段", "马桶漏水了", {}, STORE_A, "semi", "store", "只问房间号或转人工，不承诺已派修", "已经通知维修师傅了", need_human=True),
        Scenario(15, "阿宁", "语音转写确认", "[语音转写] 确认确认", {}, STORE_A, "semi", "hq", "识别为上下文确认，不当普通闲聊", "你确认什么？", "语音转写"),
        Scenario(16, "老王", "停车", "车停哪里", {INTENT_HOTEL: "停车场在酒店东侧地面停车区，前台扫码登记后免费。"}, STORE_A, "semi", "hq", "知识库短答", "这个帮你确认下"),
        Scenario(17, "老王", "早餐时间", "早餐几点到几点", {INTENT_HOTEL: "早餐 7:00-10:00，在 2 楼餐厅。"}, STORE_A, "semi", "hq", "直接答，不转人工", "你问的是门店信息，我按当前门店资料看。"),
        Scenario(18, "老王", "退房续住连续", burst("几点退房", "能续住到下午吗"), {INTENT_HOTEL: "退房时间 12:00；续住需看当天房态，可在小程序或前台确认。"}, STORE_A, "semi", "hq", "两问都答，续住不乱承诺", "退房我帮你确认下"),
        Scenario(19, "Mia", "小程序请求", "安心宿小程序发我", {}, STORE_A, "full", "hq", "发送带 storeId 的小程序", "自助入住走小程序就行。", need_resource=True),
        Scenario(20, "Mia", "老好友再次来店", "我又来住了，还要从哪办入住", {INTENT_MINI_PROGRAM: "老好友再次入住也走安心宿小程序，门店参数由当前员工号绑定。"}, STORE_A, "full", "hq", "不依赖加好友事件，按消息触发小程序", "欢迎再次入住", need_resource=True),
        Scenario(21, "Mia", "客户要电话", "前台电话多少", {INTENT_HOTEL: "前台电话绑定在门店资料。"}, STORE_A, "semi", "hq", "电话按需披露", "有需要联系前台", need_resource=True),
        Scenario(22, "包子", "位置泛问", "我从高铁站过去怎么方便", {INTENT_LOCATION: "可打车到门店定位，约 25 分钟。"}, STORE_A, "semi", "hq", "可答路线并问要不要发定位", "定位我可以发你。", need_resource=True),
        Scenario(23, "包子", "文件消息", burst("[文件理解] 文件是身份证登记表模板，用户问是否必须填写。", "这个表必须填吗"), {INTENT_HOTEL: "入住登记需实名信息；线上入住按小程序页面提交即可。"}, STORE_A, "full", "hq", "基于文件摘要回答，不编文件细节", "我看不到文件内容", "文件摘要"),
        Scenario(24, "包子", "表情包", "[表情理解] 客人发了一个疑惑表情，像是在催问上一条入住流程。", {INTENT_MINI_PROGRAM: "自助入住走安心宿小程序。"}, STORE_A, "full", "hq", "能结合上一条，不只回复表情", "哈哈", "表情理解", need_resource=True),
        Scenario(25, "小周", "订单异常", "我订的是大床房，怎么给我双床", {}, STORE_A, "semi", "hq", "订单异常需要人工核实", "我给你换一下", need_human=True),
        Scenario(26, "小周", "隐私授权", "你能直接帮我看身份证号吗", {}, STORE_A, "semi", "hq", "隐私授权人工风险", "可以，把身份证发我", need_human=True),
        Scenario(27, "小周", "电视投屏", "电视怎么投屏", {INTENT_HOTEL: "电视首页选择投屏，手机连房间 WiFi 后按屏幕投屏码连接。"}, STORE_A, "semi", "hq", "知识库操作步骤", "电视我帮你确认下"),
        Scenario(28, "小李", "连续三问：早餐/停车/定位", burst("早餐在哪", "停车免费吗", "定位也发我"), {INTENT_HOTEL: "早餐在 2 楼餐厅 7:00-10:00；停车前台扫码登记免费。"}, STORE_A, "semi", "hq", "三问覆盖且发送定位", "早餐在二楼。", need_resource=True),
        Scenario(29, "小李", "情绪不满但非投诉", "你刚才没回我，我有点急", {}, STORE_A, "semi", "hq", "安抚并继续处理，不立刻转人工", "稍后让同事跟进"),
        Scenario(30, "小李", "明确复杂人工", "房间有异味，孩子不舒服，麻烦叫人来看", {}, STORE_A, "semi", "store", "健康/现场处理，走门店群并不承诺已完成", "已经安排人过去了", need_human=True),
    ]


def render(results: List[tuple[Scenario, dict]]) -> str:
    passed = sum(1 for _, item in results if item["score"] >= 85)
    avg_score = sum(item["score"] for _, item in results) / len(results)
    max_latency = max(item["latency_ms"] for _, item in results)
    lines = [
        "# Reply Runtime Engine 30 轮连续对话评估",
        "",
        f"- 生成时间：{datetime.now().strftime('%Y-%m-%d %H:%M:%S')}",
        f"- 覆盖轮次：{len(results)}",
        f"- 通过轮次：{passed}/{len(results)}（评分 >= 85）",
        f"- 平均评分：{avg_score:.1f}",
        f"- 规则层最大耗时：{max_latency:.3f}ms",
        "- 说明：本报告运行本地 Reply Runtime 规则镜像，不向真实企微客户发送消息；用于验证连续消息、知识优先、资源按需披露、转人工路由和人味话术。",
        "",
        "## 逐轮记录",
        "",
    ]
    for scenario, result in results:
        lines.extend([
            f"### {scenario.no:02d}. {scenario.customer}｜{scenario.scene}",
            "",
            "- 用户消息：",
            "",
            "```text",
            scenario.message,
            "```",
            f"- 拆分问题：`{' | '.join(result['questions'])}`",
            f"- 识别意图：`{', '.join(result['intents'])}`",
            f"- 知识库命中：`{str(result['knowledge_hit']).lower()}`",
            f"- 路由结果：`{result['route']}`",
            f"- 实际模拟回复：{result['reply']}",
            f"- 期望：{scenario.expected}",
            f"- 护栏改写：`{str(result['guard_changed']).lower()}`" + (f"，原因：`{result['guard_reason']}`" if result["guard_reason"] else ""),
            f"- 评分：`{result['score']}`；问题：{'；'.join(result['problems'])}",
            f"- 规则层耗时：`{result['latency_ms']:.3f}ms`",
            "",
        ])
    lines.extend([
        "## 结论",
        "",
        "- 连续多条消息必须先聚合再拆问，不能只答最后一句。",
        "- WiFi、发票、早餐、停车、电视、低风险用品优先查知识库；知识缺失时只追问关键字段，不直接转人工。",
        "- 定位、电话、小程序是渐进式资源，只有命中需要时读取；小程序必须带门店参数。",
        "- 转人工不是一句话兜底，必须按全托管/半托管/非托管和时间段路由。",
        "- 规则层耗时很低；真实慢点通常来自企微回调等待、知识检索、媒体理解和模型请求。",
        "",
    ])
    return "\n".join(lines)


def main() -> None:
    results = [(item, simulate(item)) for item in scenarios()]
    report = render(results)
    output = Path("docs/generated/reply-runtime-30-round-eval.md")
    output.write_text(report, encoding="utf-8")
    print(report)


if __name__ == "__main__":
    main()
