#!/usr/bin/env python3
"""Run the second 30-round Reply Runtime Engine evaluation batch."""

from __future__ import annotations

import sys
from datetime import datetime
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

from reply_runtime_30_round_eval import (  # noqa: E402
    INTENT_GENERAL,
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
    render,
    simulate,
)


STORE_NO_GROUP = Store(
    name="丽斯未来酒店·半托管无群店",
    phone="0571-12345678",
    location="浙江省杭州市无群路 9 号",
    mini_program="安心宿小程序 page=/pages/checkin/index?storeId=no-group",
    group_ready=False,
    fallback_to_hq=False,
    has_coordinate=True,
)


def round2_scenarios() -> list[Scenario]:
    return [
        Scenario(31, "橙子", "错别字：wfi", "wfi密码多少", {INTENT_WIFI: "WiFi：房间号后四位，密码 lis888888。"}, STORE_A, "semi", "hq", "错别字也应识别 WiFi", "帮你确认下"),
        Scenario(32, "橙子", "口语：网不好", "房间网连不上", {INTENT_WIFI: "房间 WiFi 名称为 LIS-房间号，密码 lis888888。"}, STORE_A, "semi", "hq", "网络问题先按 WiFi FAQ，不转人工", "稍后让同事跟进"),
        Scenario(33, "橙子", "连续：图片 + WiFi + 停车", burst("[图片理解] 图片是电视投屏二维码页面", "电视怎么投", "WiFi密码", "车能停哪"), {INTENT_HOTEL: "电视首页选投屏，手机连房间 WiFi 后扫屏幕码；停车在东侧地面停车区，前台登记免费。", INTENT_WIFI: "WiFi：房间号后四位，密码 lis888888。"}, STORE_A, "semi", "hq", "多问全部覆盖", "电视我帮你确认下", "图片摘要"),
        Scenario(34, "小孟", "发票口语：报销", "报销票咋开", {INTENT_INVOICE: "发票在安心宿小程序申请，普票和专票都支持。"}, STORE_A, "semi", "hq", "报销票识别为发票", "这个帮你确认下"),
        Scenario(35, "小孟", "发票连续补充", burst("发票开公司", "抬头是杭州测试公司", "税号等下发你"), {INTENT_INVOICE: "专票需要抬头、税号、邮箱；资料齐了在小程序提交。"}, STORE_A, "semi", "hq", "连续发票信息合并回答", "先帮你记录"),
        Scenario(36, "小孟", "早餐错别字", "早饭几电", {INTENT_HOTEL: "早餐 7:00-10:00，在 2 楼餐厅。"}, STORE_A, "semi", "hq", "早饭/错字识别早餐", "你说，我看着。"),
        Scenario(37, "豆豆", "用品：剃须刀", "剃须刀有吗", {INTENT_SUPPLIES: "剃须刀在前台自取。"}, STORE_A, "semi", "hq", "用品知识库自取", "马上安排人送"),
        Scenario(38, "豆豆", "用品：浴巾更换", "浴巾能换一条吗", {INTENT_SUPPLIES: "备用浴巾可到前台领取。"}, STORE_A, "semi", "store", "知识库优先，不承诺送", "已经通知同事送过去"),
        Scenario(39, "豆豆", "保洁服务", "明天上午能打扫一下吗", {}, STORE_A, "semi", "store", "需要门店动作，问房号/接门店", "已记录保洁"),
        Scenario(40, "南瓜", "叫醒服务", "明早七点叫醒我", {}, STORE_A, "semi", "store", "叫醒是动作，不能假装已登记", "好的已登记叫醒"),
        Scenario(41, "南瓜", "行李协助", "行李太多能帮我拿下楼吗", {}, STORE_A, "semi", "store", "人工动作，走门店群", "马上安排同事过去"),
        Scenario(42, "南瓜", "安全风险", "走廊有人一直敲门，我有点害怕", {}, STORE_A, "semi", "store", "安全风险必须人工/门店", "你先别担心"),
        Scenario(43, "阿树", "退款诉求", "我不住了退钱", {}, STORE_A, "full", "hq", "退款人工", "可以退"),
        Scenario(44, "阿树", "差评威胁", "再不处理我就差评", {}, STORE_A, "semi", "hq", "投诉升级人工", "别差评哈"),
        Scenario(45, "阿树", "价格争议", "为什么我朋友比我便宜", {}, STORE_A, "semi", "hq", "价格争议人工", "价格就是这样"),
        Scenario(46, "啵啵", "小程序参数", "入住链接给我，要这个店的", {}, STORE_A, "full", "hq", "发带门店参数小程序", "自己搜安心宿", need_resource=True),
        Scenario(47, "啵啵", "小程序老客", "我以前加过你们，这次还要重新扫码吗", {INTENT_MINI_PROGRAM: "已是好友也可直接发当前门店小程序入口办理入住。"}, STORE_A, "full", "hq", "按消息触发小程序", "不用管", need_resource=True),
        Scenario(48, "啵啵", "定位：附近地铁", "最近地铁站怎么走", {INTENT_LOCATION: "最近地铁站从酒店步行约 8 分钟，可按定位导航。"}, STORE_A, "semi", "hq", "路线知识 + 可发定位", "定位我可以发你。", need_resource=True),
        Scenario(49, "奶盖", "半托管无群无总部", "我想找个人说下这个事", {}, STORE_NO_GROUP, "semi", "store", "无群且不 fallback 时电话兜底", "稍后联系你", need_human=True),
        Scenario(50, "奶盖", "非托管群通知", "叫人来看看空调", {}, STORE_A, "none", "store", "非托管直接门店群", "我让总部客服看", need_human=True),
        Scenario(51, "奶盖", "总部时段", "这个要人工处理", {}, STORE_A, "semi", "hq", "半托管总部时段走网页客服", "门店群通知", need_human=True),
        Scenario(52, "米粒", "客户骂人但问题明确", "你们这网也太烂了，密码多少", {INTENT_WIFI: "WiFi：房间号后四位，密码 lis888888。"}, STORE_A, "semi", "hq", "不被情绪带偏，回答 WiFi", "转人工"),
        Scenario(53, "米粒", "一句两问无编号", "早餐几点，停车收费吗", {INTENT_HOTEL: "早餐 7:00-10:00；停车前台扫码登记免费。"}, STORE_A, "semi", "hq", "一句内两问尽量覆盖", "早餐我帮你确认下"),
        Scenario(54, "米粒", "语音：网和发票", burst("[语音转写] WiFi密码是多少", "[语音转写] 发票怎么开"), {INTENT_WIFI: "WiFi：房间号后四位，密码 lis888888。", INTENT_INVOICE: "发票在安心宿小程序申请。"}, STORE_A, "semi", "hq", "语音转写多问覆盖", "语音识别可能不准"),
        Scenario(55, "咖啡", "文件：合同发票", burst("[文件理解] 文件是一份公司开票资料，包含抬头、税号和邮箱", "这样能开票吗"), {INTENT_INVOICE: "资料齐全就可以开票，在小程序提交即可。"}, STORE_A, "semi", "hq", "文件摘要进入发票上下文", "看不到文件"),
        Scenario(56, "咖啡", "表情 + 催", burst("[表情理解] 客人发了一个皱眉表情", "咋还没回复"), {}, STORE_A, "semi", "hq", "回应催促，不只哈哈", "哈哈"),
        Scenario(57, "咖啡", "门店电话但知识缺失", "直接给我电话吧", {}, STORE_A, "semi", "hq", "电话按需披露", "有事找前台", need_resource=True),
        Scenario(58, "柚子", "无定位配置问地址", "地址在哪", {}, STORE_NO_LOCATION, "semi", "store", "缺定位走门店路由/电话", "地址我发你", need_human=True, need_resource=True),
        Scenario(59, "柚子", "确认模糊词", "嗯可以", {}, STORE_A, "semi", "hq", "作为上下文确认，别机械追问", "你确认什么？"),
        Scenario(60, "柚子", "多轮复杂：入住+定位+用品", burst("我到酒店附近了", "定位发我", "入住小程序也发下", "房间没纸巾"), {INTENT_SUPPLIES: "纸巾在前台自取。"}, STORE_A, "full", "hq", "定位+小程序+用品都覆盖", "我先帮你确认下", need_resource=True),
    ]


def main() -> None:
    results = [(item, simulate(item)) for item in round2_scenarios()]
    report = render(results)
    report = report.replace("# Reply Runtime Engine 30 轮连续对话评估", "# Reply Runtime Engine 第 2 组 30 轮连续对话评估（31-60）", 1)
    report = report.replace("- 覆盖轮次：30", "- 覆盖轮次：30（第 31-60 轮）", 1)
    output = Path("docs/generated/reply-runtime-30-round-eval-round2.md")
    output.write_text(report, encoding="utf-8")
    print(report)


if __name__ == "__main__":
    main()
