# 主板与 ITX 电源安装规格补证

本批只修复当前主要场景中的四条商品记录，不采集报价、不调用模型，也不改变历史配置或已确认需求：

- Gigabyte B650 AORUS ELITE AX：rev. 1.0/1.1 与 rev. 1.2 官网均列出 DDR5 8000(OC)，补 `memory_speed_max_mts=8000`；保留实际频率取决于 CPU、内存和支持列表的限制。
- MSI MAG A650BN：官网资料为 ATX、机身长度 140 mm。
- Cooler Master NR200P：官网资料为仅支持 SFX/SFX-L，电源限长 130 mm。
- Fractal Terra：官网资料为仅支持 SFX/SFX-L，电源限长 130 mm。

当前 14 款有价电源及 6 款已退库电源中没有 SFX/SFX-L。补证后，NR200P + A650BN 会确定判为安装不兼容；其他缺少外形证据的 ITX 电源组合保持未知，不把未知改成通过。完整页面直连返回 403，因此审核输入保存网页读取器取得的官网原文片段、准确网址和哈希，不把搜索摘要当正文。

```bash
python scripts/data/reviewed-intake/2026-09-16-board-itx-fit/publish.py
python scripts/data/reviewed-intake/2026-09-16-board-itx-fit/publish.py --publish
```
