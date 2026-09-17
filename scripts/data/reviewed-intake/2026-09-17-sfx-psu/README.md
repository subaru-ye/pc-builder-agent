# SFX/SFX-L 电源补证与发布（ITX 路线恢复）

新增 `psu-cm-v850sfx-gold-white` 与 `psu-asus-rog-loki-850p-white` 两款 SFX/SFX-L 电源，正式目录从175件/123报价增加到177件/125报价。发布后 NR200P 与 Fractal Terra 的电源形态/长度规则不再 fail/unknown。

## 依据

- [Cooler Master 官方规格页](https://www.coolermaster.com/en-global/products/v-sfx-gold-850-atx-3-1-white-edition.html)（2026-09-17 抓取，SHA256 `73a1d1d0…`，见 `official-manifest.json`）：V SFX Gold 850W ATX 3.1 White Edition、Form Factor SFX、100 x 125 x 63.5 mm、850W、PCI-e 6+2 Pin x 4、12V-2x6 x 1。映射为 `form_factor=sfx`、`length_mm=100`、`wattage_w=850`、`power_connectors=[pcie_8pin x4, pcie_16pin]`。
- [ASUS ROG Loki 850P White 规格页](https://rog.asus.com.cn/power-supply-units/rog-loki/rog-loki-850p-white-sfx-l-gaming-model/spec/)（2026-09-17 抓取，SHA256 `d2ce9a6e…`）：ROG LOKI 洛基 850W SFX-L 白金牌电源、SFX-L、125 x 125 x 63.5 mm、总功率 850W、PCI-E 16-pin x 1、PCI-E 8-pin x 3。映射为 `form_factor=sfx_l`、`length_mm=125`、`wattage_w=850`、`power_connectors=[pcie_16pin, pcie_8pin x3]`。
- 买手报价（观察日 2026-09-17，`aggregator_secondary`，京东自营）：
  - 酷冷至尊 V SFX 850GOLD 白 ATX3.1电源，酷冷至尊京东自营旗舰店，1199元，row_key `ea90aa7a76ed0b88`，https://u.jd.com/41tPHnn
  - 华硕ROG洛基850W SFX-L白色电源 LOKI，华硕京东自营旗舰店，1649元，row_key `09d87a4482de62f2`，https://u.jd.com/4gtBAg8
  - 详情请求与响应的聚合 goodsId 不一致，关联依据为请求索引及详情文件哈希，同时比对精确标题、卖家、购买链接和价格；差异在 `detail-review.json` 显式保留。库存未据此确认，不表示实时可购。
- 明确暂缓（证据不足，未发布）：
  - 利民 TR-SGFX650：官方规格页公布 650W 与尺寸，但未公布供电接口数量，`power_connectors` 无可追溯证据。
  - FSP Dagger SD-650GM、DeepCool PS750G：厂商官网无对应产品页，无可追溯规格。
  - 详见 `detail-review.json` 的 `deferred`。

## 复现与记录

```bash
PYTHONPATH=scripts/data/src python scripts/data/collection-tools/2026-09-17/prepare_sfx_psu_intake.py \
  --pages artifacts/data-intake-official-sfx-psu-20260917-r1 \
  --out artifacts/<新的候选包目录>
```

准备脚本只重放固定审核资料，无网络调用；输出目录必须新建，不能覆盖既有批次。原始买手搜索/详情保存在 `var/data/collections/2026-09-17-sfx-psu-01/`（不入库）。

独立数据库演练：`artifacts/data-intake-sfx-psu-rehearsal-20260917-r1/`；正式发布：`artifacts/data-intake-sfx-psu-publish-20260917-r1/`。两者均验证 177件/125报价、旧商品和报价不变、需求与配置历史不变、报价重试幂等（`model_calls=0`、`external_search_calls=0`）。正式发布使用现有手动审核流程；此文档不是自动发布入口。

发布结果：parts_release `b1e13ebd-71be-57e5-a1e9-755312ef6caa`，price_release `17ad835a-5794-522d-aee7-a2750faf18a8`，快照 ID 10。run_id 与 Diff 见 `packet.json`（两条 change 均为 added，10 条官方字段证据）。

## 安装规则验证

发布后用 `go run ./cmd/validate` 对生产库实际数据验证 2 款电源 × NR200P/Terra 共 4 组（输入与报告存于 `artifacts/data-intake-sfx-psu-publish-20260917-r1/fit-checks/`）：

- `FORM_FACTOR_SUPPORT` 四组全部 pass（sfx/100mm 与 sfx_l/125mm 均 ≤ 130mm 限长且在 `["sfx","sfx_l"]` 支持集合内）。
- NR200P 两组整体 pass；Terra 两组仅剩风冷限高失败（AG400 150mm > 77mm），与电源无关，属既有散热器选择问题。
- 电源形态与长度不再出现 fail 或 unknown。
