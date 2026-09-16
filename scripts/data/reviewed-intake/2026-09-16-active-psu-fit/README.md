# 在售电源安装规格补证

本批承接 `2026-09-16-board-itx-fit` 发布，只为其余 13 款在售电源补充外形和机身长度。资料来自对应型号官网产品页或官网 PDF；不采集报价、不调用模型、不改变商品上下架状态、历史配置或已确认需求。

- 13 款均为 ATX 电源，长度为 140、160 或 180 mm。
- 2021 款 Corsair RM1000x 的官网手册标注长度为 160 mm；本批按该型号手册记录。
- 连同上一批 MSI MAG A650BN，当前 14 款在售且有价电源的 `form_factor` 与 `length_mm` 已全部补齐。
- 当前两款纯 ITX 机箱只支持 SFX/SFX-L，因此与全部 14 款在售电源组合时会明确判为不兼容，不再因电源外形缺失停在未知。

```bash
python scripts/data/reviewed-intake/2026-09-16-active-psu-fit/publish.py
python scripts/data/reviewed-intake/2026-09-16-active-psu-fit/publish.py --publish
```
