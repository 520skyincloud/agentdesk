#!/usr/bin/env python3
"""Run 5 batches of 30 Reply Runtime flowchart compliance conversations.

The report verifies the key flowchart stages:
message aggregation -> question split -> media context -> intent -> knowledge first
-> progressive resource disclosure -> managed-mode handoff routing -> concise reply.
"""

from __future__ import annotations

import sys
from datetime import datetime
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

from reply_runtime_30_round_eval import (  # noqa: E402
    INTENT_HOTEL,
    INTENT_INVOICE,
    INTENT_LOCATION,
    INTENT_MINI_PROGRAM,
    INTENT_SUPPLIES,
    INTENT_WIFI,
    STORE_A,
    STORE_NO_LOCATION,
    Scenario,
    Store,
    burst,
    simulate,
)


STORE_NO_GROUP = Store(
    name="合成验收酒店·半托管无群店",
    phone="0571-12345678",
    location="浙江省杭州市无群路 9 号",
    mini_program="安心宿小程序 page=/pages/checkin/index?storeId=no-group",
    group_ready=False,
    fallback_to_hq=False,
    has_coordinate=True,
)


def build_batches() -> list[list[Scenario]]:
    return [build_batch_1(), build_batch_2(), build_batch_3(), build_batch_4(), build_batch_5()]


def build_batch_1() -> list[Scenario]:
    kb_hotel = "早餐 7:00-10:00 在 2 楼；停车前台登记免费；退房 12:00。"
    return [
        Scenario(1, "A01", "WiFi 单问", "WiFi密码多少", {INTENT_WIFI: "WiFi：房间号后四位，密码 lis888888。"}, STORE_A, expected="知识库回答 WiFi"),
        Scenario(2, "A02", "WiFi 口语", "房间网连不上", {INTENT_WIFI: "房间 WiFi 名称为 LIS-房间号，密码 lis888888。"}, STORE_A, expected="WiFi 口语识别"),
        Scenario(3, "A03", "发票", "发票怎么开", {INTENT_INVOICE: "发票在安心宿小程序申请，专票按页面填资料。"}, STORE_A, expected="发票知识库"),
        Scenario(4, "A04", "报销票", "报销票咋开", {INTENT_INVOICE: "报销发票在安心宿小程序申请。"}, STORE_A, expected="发票口语"),
        Scenario(5, "A05", "早餐", "早餐几点", {INTENT_HOTEL: kb_hotel}, STORE_A, expected="早餐知识库"),
        Scenario(6, "A06", "停车", "车能停哪", {INTENT_HOTEL: kb_hotel}, STORE_A, expected="停车知识库"),
        Scenario(7, "A07", "电视", "电视怎么投屏", {INTENT_HOTEL: "电视首页选投屏，手机连房间 WiFi 后扫屏幕码。"}, STORE_A, expected="电视知识库"),
        Scenario(8, "A08", "用品自取", "有剃须刀吗", {INTENT_SUPPLIES: "剃须刀在前台自取。"}, STORE_A, expected="用品知识库自取"),
        Scenario(9, "A09", "送水转知识", "送两瓶水可以吗", {INTENT_SUPPLIES: "矿泉水在 1 楼前台自取，每间房可免费领取 2 瓶。"}, STORE_A, expected="低风险用品先知识库"),
        Scenario(10, "A10", "拖鞋缺失", "房间没有拖鞋", {}, STORE_A, expected="缺知识只追问"),
        Scenario(11, "A11", "连续 WiFi 发票", burst("WiFi是哪个", "能开专票吗"), {INTENT_WIFI: "WiFi：房间号后四位，密码 lis888888。", INTENT_INVOICE: "专票可在小程序申请。"}, STORE_A, expected="连续多问覆盖"),
        Scenario(12, "A12", "连续 早餐 停车", burst("早餐在哪", "停车免费吗"), {INTENT_HOTEL: kb_hotel}, STORE_A, expected="连续酒店 FAQ"),
        Scenario(13, "A13", "一句两问", "早餐几点，停车收费吗", {INTENT_HOTEL: kb_hotel}, STORE_A, expected="一句多问酒店 FAQ"),
        Scenario(14, "A14", "退房续住", burst("几点退房", "能续住到下午吗"), {INTENT_HOTEL: "退房 12:00；续住看当天房态。"}, STORE_A, expected="退房续住不承诺"),
        Scenario(15, "A15", "洗衣", "有洗衣房吗", {INTENT_HOTEL: "洗衣房在 3 楼，扫码自助使用。"}, STORE_A, expected="设施 FAQ"),
        Scenario(16, "A16", "热水", "热水怎么没有", {INTENT_HOTEL: "热水需放 1-2 分钟；仍无热水再联系前台。"}, STORE_A, expected="设施 FAQ"),
        Scenario(17, "A17", "押金", "押金什么时候退", {INTENT_HOTEL: "押金退房查房后原路退回。"}, STORE_A, expected="押金 FAQ"),
        Scenario(18, "A18", "房费", "房费包含早餐吗", {INTENT_HOTEL: "是否含早以订单页面为准。"}, STORE_A, expected="房费 FAQ"),
        Scenario(19, "A19", "客服电话", "前台电话多少", {INTENT_HOTEL: "电话从门店资料读取。"}, STORE_A, expected="电话按需披露", need_resource=True),
        Scenario(20, "A20", "营业时间", "前台几点有人", {INTENT_HOTEL: "前台 24 小时有人值班。"}, STORE_A, expected="营业时间 FAQ"),
        Scenario(21, "A21", "轻互动感谢", "谢谢", {}, STORE_A, expected="轻互动不敷衍"),
        Scenario(22, "A22", "嫌冷淡", "你说话好冷淡", {}, STORE_A, expected="人味回应"),
        Scenario(23, "A23", "催促", "咋还没回我，有点急", {}, STORE_A, expected="安抚继续处理"),
        Scenario(24, "A24", "确认", "嗯可以", {}, STORE_A, expected="上下文确认"),
        Scenario(25, "A25", "小程序", "安心宿小程序发我", {}, STORE_A, expected="发送小程序", need_resource=True),
        Scenario(26, "A26", "入住", "怎么自助入住", {INTENT_MINI_PROGRAM: "自助入住走安心宿小程序。"}, STORE_A, expected="入住小程序", need_resource=True),
        Scenario(27, "A27", "定位", "定位发我", {}, STORE_A, expected="发送定位", need_resource=True),
        Scenario(28, "A28", "地址", "地址在哪", {}, STORE_A, expected="定位/地址资源", need_resource=True),
        Scenario(29, "A29", "路线", "高铁站过去怎么方便", {INTENT_LOCATION: "打车约 25 分钟，可按门店定位导航。"}, STORE_A, expected="路线+定位", need_resource=True),
        Scenario(30, "A30", "缺定位", "导航发我", {}, STORE_NO_LOCATION, "semi", "store", "缺定位走路由", need_human=True, need_resource=True),
    ]


def build_batch_2() -> list[Scenario]:
    return [
        Scenario(31, "B01", "图片开票", burst("[图片理解] 图片是发票抬头和税号", "这个能开专票吗"), {INTENT_INVOICE: "抬头和税号齐全可开专票。"}, STORE_A, media="图片摘要", expected="图片摘要进入上下文"),
        Scenario(32, "B02", "图片设施", burst("[图片理解] 图片是空调面板显示 E3", "这个咋弄"), {}, STORE_A, "semi", "store", "设备异常接门店", media="图片摘要", need_human=True),
        Scenario(33, "B03", "图片定位", burst("[图片理解] 图片是用户在酒店附近路口", "怎么走到酒店"), {INTENT_LOCATION: "按门店定位导航即可。"}, STORE_A, media="图片摘要", expected="图片+路线", need_resource=True),
        Scenario(34, "B04", "语音 WiFi", "[语音转写] WiFi密码多少", {INTENT_WIFI: "WiFi：房间号后四位，密码 lis888888。"}, STORE_A, media="语音转写", expected="语音转写 FAQ"),
        Scenario(35, "B05", "语音确认", "[语音转写] 确认确认", {}, STORE_A, media="语音转写", expected="模糊确认"),
        Scenario(36, "B06", "语音投诉", "[语音转写] 我要投诉", {}, STORE_A, "semi", "hq", "投诉人工", media="语音转写", need_human=True),
        Scenario(37, "B07", "文件开票", burst("[文件理解] 文件包含公司抬头税号邮箱", "这样能开票吗"), {INTENT_INVOICE: "资料齐全，在小程序提交即可。"}, STORE_A, media="文件摘要", expected="文件摘要发票"),
        Scenario(38, "B08", "文件入住", burst("[文件理解] 文件是入住登记模板", "这个必须填吗"), {INTENT_HOTEL: "入住需实名登记；线上入住按小程序提交。"}, STORE_A, media="文件摘要", expected="文件摘要入住"),
        Scenario(39, "B09", "表情催促", burst("[表情理解] 皱眉催促", "还没好吗"), {}, STORE_A, media="表情理解", expected="表情上下文"),
        Scenario(40, "B10", "表情疑问入住", "[表情理解] 疑惑表情，上一条在问入住小程序", {INTENT_MINI_PROGRAM: "自助入住走安心宿小程序。"}, STORE_A, media="表情理解", expected="表情+小程序", need_resource=True),
        Scenario(41, "B11", "定位消息", burst("[定位消息] 客人发来的定位在高铁站", "从这到酒店多久"), {INTENT_LOCATION: "高铁站到酒店打车约 25 分钟。"}, STORE_A, media="定位", expected="定位消息理解", need_resource=True),
        Scenario(42, "B12", "小程序卡片", burst("[小程序消息] 客人发来安心宿页面", "这个页面能办理入住吗"), {INTENT_MINI_PROGRAM: "安心宿小程序可办理自助入住。"}, STORE_A, media="小程序", expected="小程序上下文", need_resource=True),
        Scenario(43, "B13", "图片+用品", burst("[图片理解] 图片里房间纸巾盒空了", "纸巾哪里拿"), {INTENT_SUPPLIES: "纸巾在前台自取。"}, STORE_A, media="图片摘要", expected="图片+用品知识"),
        Scenario(44, "B14", "图片无法理解", burst("[图片理解失败] 图片下载失败", "这个是什么"), {}, STORE_A, media="图片理解失败", expected="失败不编造"),
        Scenario(45, "B15", "语音多问", burst("[语音转写] 早餐几点", "[语音转写] 停车免费吗"), {INTENT_HOTEL: "早餐 7:00-10:00；停车前台登记免费。"}, STORE_A, media="语音转写", expected="语音连续多问"),
        Scenario(46, "B16", "文件隐私", burst("[文件理解] 文件疑似身份证照片", "你帮我看身份证号"), {}, STORE_A, "semi", "hq", "隐私人工", media="文件摘要", need_human=True),
        Scenario(47, "B17", "图片投诉", burst("[图片理解] 图片显示床单有污渍", "这也太脏了我要投诉"), {}, STORE_A, "semi", "hq", "投诉人工", media="图片摘要", need_human=True),
        Scenario(48, "B18", "图片维修", burst("[图片理解] 图片显示马桶漏水", "帮我看看"), {}, STORE_A, "semi", "store", "维修门店", media="图片摘要", need_human=True),
        Scenario(49, "B19", "语音送水", "[语音转写] 能送两瓶水吗", {INTENT_SUPPLIES: "矿泉水前台自取，每间房 2 瓶。"}, STORE_A, media="语音转写", expected="语音用品知识"),
        Scenario(50, "B20", "表情感谢", "[表情理解] 客人发感谢表情", {}, STORE_A, media="表情理解", expected="表情轻互动"),
        Scenario(51, "B21", "文件停车", burst("[文件理解] 订单备注自驾", "停车怎么弄"), {INTENT_HOTEL: "停车前台扫码登记免费。"}, STORE_A, media="文件摘要", expected="文件+停车"),
        Scenario(52, "B22", "图片小程序", burst("[图片理解] 图片是入住二维码", "直接发我小程序"), {}, STORE_A, media="图片摘要", expected="图片+小程序", need_resource=True),
        Scenario(53, "B23", "位置缺配置", burst("[定位消息] 客人在商场", "酒店地址在哪"), {}, STORE_NO_LOCATION, "semi", "store", "缺地址路由", media="定位", need_human=True, need_resource=True),
        Scenario(54, "B24", "语音价格", "[语音转写] 为什么我朋友比我便宜", {}, STORE_A, "semi", "hq", "价格人工", media="语音转写", need_human=True),
        Scenario(55, "B25", "图片订单异常", burst("[图片理解] 图片是大床房订单", "为什么给我双床"), {}, STORE_A, "semi", "hq", "订单人工", media="图片摘要", need_human=True),
        Scenario(56, "B26", "文件发票连续", burst("[文件理解] 开票资料", "能开专票吗", "邮箱等下给"), {INTENT_INVOICE: "专票资料齐全后可在小程序提交。"}, STORE_A, media="文件摘要", expected="文件连续发票"),
        Scenario(57, "B27", "表情不满", "[表情理解] 生气表情，用户嫌回复慢", {}, STORE_A, media="表情理解", expected="情绪安抚"),
        Scenario(58, "B28", "语音路线", "[语音转写] 从机场过去怎么走", {INTENT_LOCATION: "机场到酒店可打车，按门店定位导航。"}, STORE_A, media="语音转写", expected="语音路线", need_resource=True),
        Scenario(59, "B29", "文件无法解析", burst("[文件理解失败] 不支持的压缩包", "这里面是开票资料"), {}, STORE_A, media="文件理解失败", expected="不编文件内容"),
        Scenario(60, "B30", "多媒体混合", burst("[图片理解] 电视投屏码", "[语音转写] WiFi密码", "车能停哪"), {INTENT_WIFI: "WiFi：房间号后四位。", INTENT_HOTEL: "停车前台登记免费。"}, STORE_A, media="混合媒体", expected="混合多问"),
    ]


def build_batch_3() -> list[Scenario]:
    return [
        Scenario(61, "C01", "全托管人工", "帮我转人工", {}, STORE_A, "full", "hq", "全托管总部", need_human=True),
        Scenario(62, "C02", "半托管总部时段", "我要人工处理", {}, STORE_A, "semi", "hq", "总部时段网页客服", need_human=True),
        Scenario(63, "C03", "半托管门店时段", "找真人处理", {}, STORE_A, "semi", "store", "门店时段群", need_human=True),
        Scenario(64, "C04", "非托管", "让人来看看", {}, STORE_A, "none", "store", "非托管门店群", need_human=True),
        Scenario(65, "C05", "半托管无群无HQ", "找个人说下", {}, STORE_NO_GROUP, "semi", "store", "电话兜底", need_human=True),
        Scenario(66, "C06", "投诉赔偿", "我要投诉赔偿", {}, STORE_A, "semi", "hq", "人工风险", need_human=True),
        Scenario(67, "C07", "退款", "我不住了退款", {}, STORE_A, "full", "hq", "退款人工", need_human=True),
        Scenario(68, "C08", "安全", "有人敲门我害怕", {}, STORE_A, "semi", "store", "安全人工", need_human=True),
        Scenario(69, "C09", "隐私", "你帮我查身份证号", {}, STORE_A, "semi", "hq", "隐私人工", need_human=True),
        Scenario(70, "C10", "订单异常", "我订的是大床怎么给双床", {}, STORE_A, "semi", "hq", "订单人工", need_human=True),
        Scenario(71, "C11", "价格争议", "价格怎么比朋友贵", {}, STORE_A, "semi", "hq", "价格人工", need_human=True),
        Scenario(72, "C12", "维修", "空调坏了", {}, STORE_A, "semi", "store", "维修门店", need_human=True),
        Scenario(73, "C13", "漏水", "马桶漏水", {}, STORE_A, "semi", "store", "维修门店", need_human=True),
        Scenario(74, "C14", "保洁", "明早打扫一下", {}, STORE_A, "semi", "store", "保洁门店", need_human=True),
        Scenario(75, "C15", "叫醒", "明早七点叫醒", {}, STORE_A, "semi", "store", "叫醒门店", need_human=True),
        Scenario(76, "C16", "行李", "帮我拿行李", {}, STORE_A, "semi", "store", "行李门店", need_human=True),
        Scenario(77, "C17", "用品不是人工", "送两瓶水", {INTENT_SUPPLIES: "矿泉水前台自取。"}, STORE_A, "semi", "store", "低风险用品不直接人工"),
        Scenario(78, "C18", "WiFi不是人工", "WiFi密码", {INTENT_WIFI: "WiFi：房间号后四位。"}, STORE_A, "semi", "hq", "FAQ 不人工"),
        Scenario(79, "C19", "发票不是人工", "发票怎么开", {INTENT_INVOICE: "发票小程序申请。"}, STORE_A, "semi", "hq", "FAQ 不人工"),
        Scenario(80, "C20", "定位不是人工", "定位发我", {}, STORE_A, "semi", "hq", "资源直接发", need_resource=True),
        Scenario(81, "C21", "小程序不是人工", "小程序发我", {}, STORE_A, "semi", "hq", "资源直接发", need_resource=True),
        Scenario(82, "C22", "缺定位门店", "地址在哪", {}, STORE_NO_LOCATION, "none", "store", "缺资源路由", need_human=True, need_resource=True),
        Scenario(83, "C23", "缺群电话", "找真人", {}, STORE_NO_GROUP, "semi", "store", "电话兜底", need_human=True),
        Scenario(84, "C24", "投诉非托管", "我要投诉", {}, STORE_A, "none", "store", "非托管群", need_human=True),
        Scenario(85, "C25", "退款非托管", "退钱", {}, STORE_A, "none", "store", "非托管群", need_human=True),
        Scenario(86, "C26", "严重订单", "订单房型不对", {}, STORE_A, "semi", "hq", "订单人工", need_human=True),
        Scenario(87, "C27", "安全总部时段", "房间门锁打不开很危险", {}, STORE_A, "semi", "hq", "安全总部", need_human=True),
        Scenario(88, "C28", "明确不要机器人", "别机器人了找人工", {}, STORE_A, "semi", "hq", "人工", need_human=True),
        Scenario(89, "C29", "催但不是人工", "你快点回我 WiFi", {INTENT_WIFI: "WiFi：房间号后四位。"}, STORE_A, "semi", "hq", "催促但答 WiFi"),
        Scenario(90, "C30", "复杂人工", burst("空调坏", "孩子不舒服", "找人来"), {}, STORE_A, "semi", "store", "复杂人工", need_human=True),
    ]


def build_batch_4() -> list[Scenario]:
    phrases = [
        ("D01", "网咋连", INTENT_WIFI, "WiFi：房间号后四位。"),
        ("D02", "无线密码", INTENT_WIFI, "WiFi：房间号后四位。"),
        ("D03", "票咋弄", INTENT_INVOICE, "发票小程序申请。"),
        ("D04", "开发票", INTENT_INVOICE, "发票小程序申请。"),
        ("D05", "早饭在哪", INTENT_HOTEL, "早餐在 2 楼。"),
        ("D06", "车停哪儿", INTENT_HOTEL, "停车前台登记免费。"),
        ("D07", "纸在哪拿", INTENT_SUPPLIES, "纸巾前台自取。"),
        ("D08", "牙刷有不", INTENT_SUPPLIES, "牙刷前台自取。"),
        ("D09", "地铁过去咋走", INTENT_LOCATION, "按定位导航即可。"),
        ("D10", "你们位置", INTENT_LOCATION, "按定位导航即可。"),
    ]
    scenarios: list[Scenario] = []
    no = 91
    for code, text, intent, answer in phrases:
        scenarios.append(Scenario(no, code, "口语识别", text, {intent: answer}, STORE_A, expected=f"口语识别 {intent}", need_resource=intent == INTENT_LOCATION))
        no += 1
    scenarios.extend([
        Scenario(101, "D11", "冷淡反馈", "你这回答像机器人", {}, STORE_A, expected="人味回应"),
        Scenario(102, "D12", "别敷衍", "别敷衍我", {}, STORE_A, expected="人味回应"),
        Scenario(103, "D13", "感谢", "谢啦", {}, STORE_A, expected="轻互动"),
        Scenario(104, "D14", "确认", "行", {}, STORE_A, expected="确认"),
        Scenario(105, "D15", "否定", "不是这个意思", {}, STORE_A, expected="追问澄清"),
        Scenario(106, "D16", "连续无编号", burst("票咋弄", "无线密码", "车停哪儿"), {INTENT_INVOICE: "发票小程序申请。", INTENT_WIFI: "WiFi：房间号后四位。", INTENT_HOTEL: "停车前台登记免费。"}, STORE_A, expected="连续口语"),
        Scenario(107, "D17", "错字定位", "定为发我", {}, STORE_A, expected="错字定位", need_resource=True),
        Scenario(108, "D18", "错字早餐", "早餐几典", {INTENT_HOTEL: "早餐 7:00-10:00。"}, STORE_A, expected="错字早餐"),
        Scenario(109, "D19", "错字人工", "转人公", {}, STORE_A, "semi", "hq", "错字人工", need_human=True),
        Scenario(110, "D20", "错字发票", "发飘怎么开", {INTENT_INVOICE: "发票小程序申请。"}, STORE_A, expected="错字发票"),
        Scenario(111, "D21", "同义小程序", "入住码给我", {}, STORE_A, expected="小程序", need_resource=True),
        Scenario(112, "D22", "同义小程序2", "自助入住入口", {}, STORE_A, expected="小程序", need_resource=True),
        Scenario(113, "D23", "同义位置", "导航给一个", {}, STORE_A, expected="定位", need_resource=True),
        Scenario(114, "D24", "同义电话", "电话给一个", {}, STORE_A, expected="电话", need_resource=True),
        Scenario(115, "D25", "模糊服务", "这个房间不太对", {}, STORE_A, expected="追问"),
        Scenario(116, "D26", "模糊投诉", "这事我不满意", {}, STORE_A, "semi", "hq", "投诉倾向人工", need_human=True),
        Scenario(117, "D27", "多问混合", burst("网咋连", "地铁过去咋走", "入住码给我"), {INTENT_WIFI: "WiFi：房间号后四位。", INTENT_LOCATION: "按定位导航即可。"}, STORE_A, expected="混合资源", need_resource=True),
        Scenario(118, "D28", "资源缺失", "定位和电话都给我", {}, STORE_NO_LOCATION, "semi", "store", "定位缺失但电话可披露/路由", need_human=True, need_resource=True),
        Scenario(119, "D29", "客户生气但 FAQ", "你们太慢了，WiFi呢", {INTENT_WIFI: "WiFi：房间号后四位。"}, STORE_A, expected="情绪+FAQ"),
        Scenario(120, "D30", "客户生气且人工", "你们太慢了，找人工", {}, STORE_A, "semi", "hq", "情绪+人工", need_human=True),
    ])
    return scenarios


def build_batch_5() -> list[Scenario]:
    return [
        Scenario(121, "E01", "长连续 1", burst("WiFi密码", "早餐几点", "发票咋开", "定位发我", "小程序发我"), {INTENT_WIFI: "WiFi：房间号后四位。", INTENT_HOTEL: "早餐 7:00-10:00。", INTENT_INVOICE: "发票小程序申请。"}, STORE_A, expected="五问覆盖", need_resource=True),
        Scenario(122, "E02", "长连续 2", burst("停车收费吗", "电视投屏", "纸巾哪拿", "谢谢"), {INTENT_HOTEL: "停车免费；电视首页选投屏。", INTENT_SUPPLIES: "纸巾前台自取。"}, STORE_A, expected="FAQ+轻互动"),
        Scenario(123, "E03", "长连续 3", burst("空调坏了", "孩子不舒服", "找人来"), {}, STORE_A, "semi", "store", "人工风险", need_human=True),
        Scenario(124, "E04", "长连续 4", burst("[图片理解] 发票资料", "能开专票吗", "邮箱我发你"), {INTENT_INVOICE: "资料齐全可开专票。"}, STORE_A, media="图片摘要", expected="媒体+发票"),
        Scenario(125, "E05", "长连续 5", burst("[语音转写] WiFi", "[语音转写] 停车", "[语音转写] 早餐"), {INTENT_WIFI: "WiFi：房间号后四位。", INTENT_HOTEL: "停车免费；早餐 7:00-10:00。"}, STORE_A, media="语音", expected="语音多问"),
        Scenario(126, "E06", "全托管资源", burst("小程序发我", "定位发我"), {}, STORE_A, "full", "hq", "资源直接发", need_resource=True),
        Scenario(127, "E07", "半托管资源+人工", burst("定位发我", "找人工"), {}, STORE_A, "semi", "store", "资源+人工", need_human=True, need_resource=True),
        Scenario(128, "E08", "非托管投诉", burst("房间很脏", "我要投诉"), {}, STORE_A, "none", "store", "非托管群", need_human=True),
        Scenario(129, "E09", "无定位复杂", burst("地址在哪", "我到了附近"), {}, STORE_NO_LOCATION, "semi", "store", "缺定位路由", need_human=True, need_resource=True),
        Scenario(130, "E10", "无群复杂", burst("找个人", "电话给我"), {}, STORE_NO_GROUP, "semi", "store", "电话兜底", need_human=True, need_resource=True),
        Scenario(131, "E11", "知识不命中 WiFi", "WiFi", {}, STORE_A, expected="追问房号"),
        Scenario(132, "E12", "知识不命中用品", "用品在哪", {}, STORE_A, expected="追问具体用品"),
        Scenario(133, "E13", "知识不命中发票", "票怎么开", {}, STORE_A, expected="通用发票入口"),
        Scenario(134, "E14", "知识命中路线", "机场过去怎么走", {INTENT_LOCATION: "机场打车约 40 分钟。"}, STORE_A, expected="路线资源", need_resource=True),
        Scenario(135, "E15", "电话", "前台电话", {}, STORE_A, expected="电话披露", need_resource=True),
        Scenario(136, "E16", "隐私+发票", burst("发票怎么开", "你能看我身份证吗"), {INTENT_INVOICE: "发票小程序申请。"}, STORE_A, "semi", "hq", "FAQ+隐私人工", need_human=True),
        Scenario(137, "E17", "退款+小程序", burst("小程序发我", "我要退款"), {}, STORE_A, "semi", "hq", "资源+退款人工", need_human=True, need_resource=True),
        Scenario(138, "E18", "低风险用品+感谢", burst("水哪里拿", "谢谢"), {INTENT_SUPPLIES: "矿泉水前台自取。"}, STORE_A, expected="用品+轻互动"),
        Scenario(139, "E19", "定位+停车", burst("定位发我", "停车免费吗"), {INTENT_HOTEL: "停车前台登记免费。"}, STORE_A, expected="定位+停车", need_resource=True),
        Scenario(140, "E20", "小程序+早餐", burst("办理入住", "早餐几点"), {INTENT_MINI_PROGRAM: "入住走安心宿。", INTENT_HOTEL: "早餐 7:00-10:00。"}, STORE_A, expected="小程序+早餐", need_resource=True),
        Scenario(141, "E21", "保洁+房号", "1208 明早打扫", {}, STORE_A, "semi", "store", "保洁动作", need_human=True),
        Scenario(142, "E22", "维修+房号", "802 空调坏", {}, STORE_A, "semi", "store", "维修动作", need_human=True),
        Scenario(143, "E23", "叫醒+房号", "901 明早 7 点叫醒", {}, STORE_A, "semi", "store", "叫醒动作", need_human=True),
        Scenario(144, "E24", "行李+位置", "大厅帮拿行李", {}, STORE_A, "semi", "store", "行李动作", need_human=True),
        Scenario(145, "E25", "价格+情绪", "太贵了为什么别人便宜", {}, STORE_A, "semi", "hq", "价格人工", need_human=True),
        Scenario(146, "E26", "投诉+安全", "门锁坏了我要投诉", {}, STORE_A, "semi", "store", "安全投诉", need_human=True),
        Scenario(147, "E27", "纯寒暄", "在吗", {}, STORE_A, expected="轻互动"),
        Scenario(148, "E28", "纯确认", "好", {}, STORE_A, expected="确认"),
        Scenario(149, "E29", "非问题表情", "[表情理解] 客人发了 OK 手势", {}, STORE_A, media="表情", expected="表情确认"),
        Scenario(150, "E30", "最大混合", burst("[图片理解] 电视投屏码", "WiFi密码", "停车免费吗", "定位发我", "小程序发我", "谢谢"), {INTENT_WIFI: "WiFi：房间号后四位。", INTENT_HOTEL: "停车免费。"}, STORE_A, media="图片", expected="最大混合", need_resource=True),
    ]


def flowchart_checks(item: Scenario, result: dict) -> list[str]:
    checks = []
    if "客人刚才连续发了几条消息" in item.message:
        checks.append("聚合连续消息")
        checks.append("拆分逐项问题")
    if item.media:
        checks.append("媒体摘要进入上下文")
    if result["knowledge_hit"]:
        checks.append("知识库优先")
    if result["route"] in {"send_location", "send_mini_program", "disclose_phone"}:
        checks.append("资源按需披露")
    if result["route"] in {"hq_web_agent", "store_wecom_group", "phone_fallback"}:
        checks.append("接待路由")
    if not checks:
        checks.append("直接短答/追问")
    return checks


def render_5x30(results_by_batch: list[list[tuple[Scenario, dict]]]) -> str:
    flat = [item for batch in results_by_batch for item in batch]
    passed = sum(1 for _, result in flat if result["score"] >= 85)
    avg_score = sum(result["score"] for _, result in flat) / len(flat)
    max_latency = max(result["latency_ms"] for _, result in flat)
    lines = [
        "# Reply Runtime Engine 5 轮 x 30 次连续对话流程图合规评估",
        "",
        f"- 生成时间：{datetime.now().strftime('%Y-%m-%d %H:%M:%S')}",
        "- 覆盖规模：5 组，每组 30 次，共 150 次",
        f"- 通过轮次：{passed}/{len(flat)}（评分 >= 85）",
        f"- 平均评分：{avg_score:.1f}",
        f"- 规则层最大耗时：{max_latency:.3f}ms",
        "- 链路依据：关键流程图：连续消息聚合 -> 拆问 -> 媒体上下文 -> 意图识别 -> 知识库优先 -> 资源按需披露 -> 接待路由 -> 简短回复。",
        "- 说明：本评估不向真实企微客户发送消息，验证的是 Reply Runtime 本地规则链路。",
        "",
    ]
    for batch_index, batch in enumerate(results_by_batch, start=1):
        batch_passed = sum(1 for _, result in batch if result["score"] >= 85)
        lines.extend([f"## 第 {batch_index} 组（30 次）", "", f"- 通过：{batch_passed}/30", ""])
        for scenario, result in batch:
            lines.extend([
                f"### {scenario.no:03d}. {scenario.customer}｜{scenario.scene}",
                "",
                "```text",
                scenario.message,
                "```",
                f"- 流程图节点：`{' -> '.join(flowchart_checks(scenario, result))}`",
                f"- 拆分问题：`{' | '.join(result['questions'])}`",
                f"- 识别意图：`{', '.join(result['intents'])}`",
                f"- 知识库命中：`{str(result['knowledge_hit']).lower()}`",
                f"- 路由结果：`{result['route']}`",
                f"- 模拟回复：{result['reply']}",
                f"- 评分：`{result['score']}`；问题：{'；'.join(result['problems'])}",
                f"- 规则层耗时：`{result['latency_ms']:.3f}ms`",
                "",
            ])
    lines.extend([
        "## 总结",
        "",
        "- 已按关键流程图验证 150 次连续对话。",
        "- 重点覆盖 FAQ、用品、发票、定位、小程序、电话、富媒体、人工路由、全托管/半托管/非托管、情绪与错别字。",
        "- 规则层很快；线上慢点继续从企微回调、媒体理解、知识库检索和模型请求链路定位。",
        "",
    ])
    return "\n".join(lines)


def main() -> None:
    batches = build_batches()
    results_by_batch = [[(scenario, simulate(scenario)) for scenario in batch] for batch in batches]
    report = render_5x30(results_by_batch)
    output = Path("docs/generated/reply-runtime-5x30-flow-eval.md")
    output.write_text(report, encoding="utf-8")
    print(report)


if __name__ == "__main__":
    main()
