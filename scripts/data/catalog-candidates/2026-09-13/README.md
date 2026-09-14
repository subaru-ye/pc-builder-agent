# 买手扩库候选批次 2026-09-13(batch-01)

## 概述

使用本机买手技能(search/detail)做目录外新型号发现与身份去重,首阶段目标
"累计 100 个经准确身份去重的新型号候选"已达成:**接受候选 115 个**(原 160 SKU 之外),
另有 49 个候选因无法通过身份绑定被拒、0 个待复核。所有数据来自买手技能返回的
真实报价行,未编造任何数值;缺规格的候选保留并列出缺项(见 missing-specs.md)。

**本批仅为候选,未导入产品库/基线。**同型号的多店铺报价记为多 offer,不计为多型号。

## 复核方法(Agent 辅助复核)

- 变体绑定原则:电商多型号列表的价格只绑定**尾部区段**(约末 16 个 sq 规范化字符)
  或【】括注内的型号;标题其他位置出现型号 token 不绑定价格。
- 清洗后仍无绑定行 → reject(不猜造);绑定冲突(如 B570 标 8GB) → reject。
- 价格带异常只作 flag 不拒收(新模型无旧基线);单店、跨平台直邮等同样只记 flag。
- 命中旧 160 SKU 身份的行分流至 legacy-price-hits.jsonl(926 行,覆盖 68 个旧 SKU,
  可用于下次基线报价刷新,本批不处理)。

## 旧 SKU 基线刷新(legacy-refresh.jsonl + prices/2026-09-13.csv)

同日零新增调用,用 legacy-price-hits.jsonl 按 v3.1 判例刷新旧 160 SKU 报价
(工具:collection-tools/2026-09-13/refresh_legacy_20260913.py,含 --sku 可复现审计):

- 判例落地:①京东变体选择器行按尾部/【】区段绑定(tier A,需 zone 内含全部身份
  token 且无其他型号 token);②非京东行多型号链接显示最低档价不可用,即便尾部命中
  也要求全标题单型号;③tier B 仅接受守卫唯一且全标题单型号的行。
- 守卫:品类容量/频率/瓦数、跨品牌混列 FORB、散片/套装/配件排除、logical 去重
  (同价+同规范化标题)、系列精修(-pulse SKU 的选中变体后缀必须含"脉动")、
  散片判定看选中变体后缀而非全标题;盒装与散片并存取盒装。
- 偏离旧基线超出 [0.55, 1.5] → review(自动 carry,留待人工),不自动晋升。
- 结果:**accept 14 / review 4 / carry 142**。accept 全部为京东选择器行,逐条附
  row_key 证据;review 4 条(nr200p 0.53、r7-7700 0.53 仅散片、gb-5060 1.69、
  bx500 2.23)按 carry 处理留待人工。
- 未刷新的原因分布:无非京东可绑定行、多型号混列、系列不匹配(如 7700XT 只有
  白金版行,7900XT 只有极地版行,均非 Pulse,不冒充)、变体行本身是数显/二代等
  改版(AK620 数显、AG400 G2)。mx500 选择器行无容量证据,同样 carry。
- prices/2026-09-13.csv:14 行 maishou88/2026-09-13,其余 146 行逐字沿用 09-08
  (含 07-28 的 jd/taobao/zol/pdd 行);pcdata 载入最新文件整体生效。


## 交付物

| 文件 | 内容 |
| --- | --- |
| candidates.jsonl | 115 个接受候选:身份、标题可见规格、缺项、价格 min/median、平台、复核备注 |
| offers.jsonl | 385 条报价行(去重后):goodsId、店铺、标题、价格、is_primary、buy_url |
| review-decisions.jsonl | 全部 164 个模型条目的 accept/reject 决策与理由(含改名/拆分记录) |
| missing-specs.md | 95/115 个候选的缺规格清单 |
| legacy-refresh.jsonl | 旧 160 SKU 基线刷新决策留痕(accept/review/carry + row_key 证据) |
| manifest.json | 文件 SHA-256、预算消耗、数量统计 |
| legacy-price-hits.jsonl | 旧 160 SKU 的价格命中行(926 行 / 68 SKU,供基线刷新用) |

## 品类分布

| 品类 | 接受 | 品类 | 接受 |
| --- | --- | --- | --- |
| gpu | 26 | case | 12 |
| motherboard | 21 | cooler | 9 |
| cpu | 18 | memory | 7 |
| ssd | 17 | psu | 5 |

82 个 cpu/gpu/motherboard/ssd 候选已用 detail 复核(购买链接+详情价与搜索价一致)。

## 调用消耗

- search 139/200(131 轮次 1 + 8 轮次 2 补搜,0 失败)
- detail 82/120(0 失败)
- HTTP 请求合计 303

## 已知边界

- 银爵 DDR5-6000 16G 套条存在 1779 与 369 两个官方店报价,369 行疑似旧价/占位,
  未纳入 offers(记录于 review-decisions 备注)。
- 航嘉 WD650EVO 的 80PLUS 认证在不同行分别标金牌/铜牌,认证列为缺项。
- 多瓦数电源(冰山版/HV VITA G PRO/长城G7 等)标题无法绑定瓦数,整组拒收,
  后续需逐瓦数精确搜索词重试。
- case/cooler 品类配件(侧板、模组线、收纳包、灯板)干扰大,依赖 GLOBAL_NOT 过滤
  + 复核剔除。
