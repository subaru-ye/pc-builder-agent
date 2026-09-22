# 在售散热器 cooling_capacity_w 复核（2026-09-17）

结论：13 款在售散热器中 10 款缺 `cooling_capacity_w`。逐款核查厂商官网具体型号页后，**没有任何一款官网公布可映射的额定散热能力瓦数，因此本次不发布任何规格更新，10 款全部保持 unknown**。原有数值保留不变：AG400 220W、AK620 260W、Dark Rock Pro 5 270W。

## 逐款核查记录

来源页面全文存证于 `var/data/collections/2026-09-17-cooler-capacity-01/official/`（原始页面不入 Git）。判定仅依据页面正文；页面出现的风扇/水泵功耗（如 4.8W、0.96W）是设备自身功耗，不映射为散热能力；第三方媒体宣称的解热瓦数不在官网页面上，不采用。

| SKU | 型号 | 已查来源 | 正文结论 | 存证 SHA256 |
|---|---|---|---|---|
| cooler-thermalright-pa120se | Peerless Assassin 120 SE | thermalright.com 产品页 | 无瓦数 | cebe6f637399d077b17990b75495095ef9dd0bca11d4643ea4a7fa8045032bda |
| cooler-thermalright-ps120se | Phantom Spirit 120 SE | thermalright.com 产品页 | 无瓦数 | f6e183579866f11a384b902ebe6912f09e3c0b8d76d15e43cfcab3c945925563 |
| cooler-thermalright-frozen-prism-240 | Frozen Prism 240 (Black) | thermalright.com 产品页（黑/黑ARGB 两页） | 无瓦数；仅泵功耗 4.8W | a358f29c556600a4ef8d4ac3e4f9d35bc4b64b36a9bb5bed44f9e4cda516b3ac / e4350f10d1f13b94613adb243bcbe4199961f6344048f95d7e686515ec28f772 |
| cooler-deepcool-assassin-iv | ASSASSIN IV | cn.deepcool.com / deepcool.com 产品页 | 无瓦数；仅风扇 2.4W、性能模式 4.8W | c814d5202370cc8ffc377227fe8bde7149863ca72f48fd57f3f80cc94af4d208 / a4ebd498ab5d474c36aa2699d4a740657f8515a2d1c976c011a373788d11b3af |
| cooler-deepcool-lt520 | LT520 | deepcool.com / cn.deepcool.com 产品页 | 无瓦数；仅泵 4.56W、风扇 2.64W | f4c3650c26b2bcabbce7530cf7db7b4c1a8f3a34fd81592a5fb4f4c8f97fc18e / da02d5ba04bcc91c772bc303716e29893724a9485fbb6d893724d0b58811764e |
| cooler-arctic-lf3-240 | Liquid Freezer III 240 | arctic.de 产品页 | 无瓦数 | d549fe13781c0d22e9d5901c6f7f60ae2a78a4d43b50560d0b0a69744ce9abb0 |
| cooler-arctic-lf3-360 | Liquid Freezer III 360 | arctic.de 产品页 | 无瓦数 | a892e2c5f6b06c2ffea5a6c410bb6db7c3c10ffe7800f8c7f64264d63a421a5a |
| cooler-coolermaster-hyper212-black | Hyper 212 Black Edition | coolermaster.com 产品页 | 无瓦数；仅风扇功耗 0.96W | 903388b96c28809bbed6d08495332887e00221dc740f8ecefe2264521ecc7e15 |
| cooler-noctua-nh-d15 | NH-D15 | noctua.at 产品页 | **无法核实**：官网反爬拦截（curl 返回安全检查页、WebFetch 429、archive.org 超时），未取得正文，未存证 | — |
| cooler-noctua-nh-u12s | NH-U12S | noctua.at 产品页 | **无法核实**：同上 | — |

已查 URL 明细（由研究过程记录）：

- Thermalright：`thermalright.com` 三款产品页（PA120 SE、PS120 SE、Frozen Prism 240 及其 BLACK ARGB 变体页）
- DeepCool：`https://cn.deepcool.com/products/Cooling/cpuaircoolers/Assassin-IV-Premium-CPU-Air-Cooler-1851-1700-AM5/2024/17134.shtml`、`https://www.deepcool.com/products/Cooling/cpuaircoolers/Assassin-IV-Premium-CPU-Air-Cooler-1851-1700-AM5/2024/17129.shtml`、`https://www.deepcool.com/products/Cooling/cpuliquidcoolers/LT520-WH-240mm-Liquid-Cooler-1851-1700-AM5/2024/16748.shtml`、`https://cn.deepcool.com/products/Cooling/cpuliquidcoolers/LT520-240mm-Liquid-CPU-Cooler-1851-1700-AM5/2024/16311.shtml`
- Arctic：`https://www.arctic.de/en/Liquid-Freezer-III-240/ACFRE00134A`、`https://www.arctic.de/us/Liquid-Freezer-III-360/ACFRE00136A`
- Cooler Master：`https://www.coolermaster.com/en-global/products/hyper-212-black-edition/`
- Noctua：`https://noctua.at/en/nh-d15`、`https://noctua.at/en/nh-u12s`（拦截，未核实）

缺失字段统一为 `specs.cooling_capacity_w`。Noctua 两款如后续需补齐，需能通过官网正文核实的渠道（说明书 PDF 等），再走既有审核发布流程。

## 保护逻辑回归（离线重放）

历史散热失败真实录制离线重放：`artifacts/thermal-recorded-replay-20260917-r1/`。结果 0/1（保留失败），8 条 Builder 回答、13 次工具执行，与 `current-123-20260915-r2` 套件冻结预期一致（验收文档已清理），未修改历史录制、未放宽断言。保存方案状态与回复中不含"完全足够/绰绰有余/保证温控"；step1 保存回复明确声明"散热能力字段缺失，无法判定"。报告中该短语仅出现在历史录制的模型输入 trace（`.cases[0].steps[1].trace[*]`），非保存内容。

本次复核：0 次数据发布、0 次买手查询、0 次真实模型调用；联网仅用于厂商官网页面读取。
