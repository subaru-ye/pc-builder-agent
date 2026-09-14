# 旧 SKU 基线刷新批次 2026-09-14(legacy-refresh)

## 概述

针对 2026-09-13.csv 中仍挂 2026-07-28 旧价的 57 个 SKU,用买手技能做一轮
定向重扫(source=0 聚合)。预算按批次记 61 次 search(57 主搜 + 4 补搜,
0 失败),但全局 search 预算在本批内触顶(09-13 批 139 + 本批 61 = 200/200),
detail 0 消耗(全局 82/120)。本批只做旧 SKU 价格刷新,不做新型号发现;
候选新型号一律不导入产品库。

原始采集行落 var/data/collections/2026-09-14-legacy-refresh-01/;
提取后命中行 legacy-hits.jsonl(254 行 / 46 SKU)。

## 判例(v3.2,工具 collection-tools/2026-09-14/refresh_legacy_20260914.py)

在 09-13 v3.1 基础上:

- sq() 增加"4位数字+gb"粘合修复:剥空格后 "DDR5 6000 32GB" 粘成
  ddr5600032gb,频率 6000 无法用 (\d{4})(?!\d) 识别 → 在 4 位数字后紧跟
  容量 gb/g2 的边界插入 w 标记(6000w32gb)。这是 09-13 内存 0 接受的根因。
- TAIL_ID 补全 case/cooler/mem/psu/gpu/mb 缺失身份 token;内存非 MEM_SPEC
  SKU 的频率并入 TAIL_ID(频率既是身份也是区分子型号的依据)。
- 系列钉死进 TAIL_ID(zone 级):5080-gaming 需"魔鹰"、windforce 系需
  "风魔"、5070-sff 需 "sff";sapphire -pulse 系仍用 SERIES_SUFFIX(选中
  变体后缀含"脉动")。
- 其余判例与守卫同 09-13:v3.1 尾部绑定 / 非京东全标题单型号 / 系列精修 /
  散片后缀判定 / 跨品牌与改版 FORB / logical 去重 / 盒装优先 /
  合理带 [0.55, 1.5](出带 → review 自动 carry)。
- 新增 JUNK:防尘罩 / 保护套 / 扩展坞(机箱配件与显卡坞曾误绑定)。

## 结果

**accept 5 / review 5(自动 carry)/ carry 150。**

| SKU | 旧 | 新 | 比 | 依据 |
| --- | --- | --- | --- | --- |
| case-corsair-5000d-airflow | 999.00 | 999.00 | 1.00 | JD "5000D RGB AIRFLOW" 选择器行(白/黑两行,row_key 7b3bc627…/9b306ec2…),与 09-08 基线同 listing 族 |
| cpu-i3-12100f | 619.00 | 603.68 | 0.97 | 仅散片(2 条 tier-A 散片行),沿用 09-13 12600k 仅散片判例 |
| cpu-r5-7500f | 784.00 | 780.00 | 0.99 | 仅散片【全新散片】行 472c5bfe… |
| gpu-gb-5060-windforce | 2499.00 | 3028.00 | 1.21 | JD 变体行"技嘉RTX5060 风魔 8G" bab7808b… |
| gpu-gb-5080-gaming | 15099.00 | 13999.00 | 0.93 | JD 变体行"技嘉魔鹰RTX5080 OC" 2c514a6f… |

review 5 条(h7-flow-2024 2.55、r7-7700 0.51、
asus-4060ti-dual-evo 1.86、gb-5060ti-16g 1.68、s5-32-6000 5.96)按 carry
处理,留待人工,详见 legacy-refresh.jsonl。

已核验的池内残余(不影响 min 取值):5080 池另有 15599 冰雕行;5060 池
另有 3699/3740 风魔 MAX OC 行,均为真品行,取 min 正确。

## 未刷新原因(52 个 carry SKU)

- 3 个 SKU 无任何数据行(搜索失败/无返回):mb-asrock-b650m-hdv-m2、
  mem-team-delta-32-3200-d4、cooler-msi-mag-coreliquid-e360。
- 其余为行不可绑定:JUNK(拆机/配件/主机)95 行、多型号混列
  (MEM_MULTI "16g32g" 式 27 行)、跨品牌 FORB(B580 蓝戟/磐镭类 15 行)、
  容量/瓦数不符(cap 守卫 24 行)、8g/16g 相邻 8 行等;行级判
  {'junk': 95, 'tier_none': 69, 'tier_A': 15, 'dup_logical': 5, 'tier_B': 2}。
- 不编造:无可用绑定行即 carry 旧价,理由留痕于 legacy-refresh.jsonl。

## 交付物

| 文件 | 内容 |
| --- | --- |
| legacy-hits.jsonl | 命中旧 SKU 身份的报价行(254 行 / 46 SKU),refresh 工具输入 |
| legacy-refresh.jsonl | 160 个旧 SKU 的 accept/review/carry 决策留痕(含 row_key 证据) |
| manifest.json | 文件 SHA-256、预算消耗、数量统计 |

prices/2026-09-14.csv:5 行 maishou88/2026-09-14 新价,其余 155 行逐字沿用
2026-09-13.csv;pcdata 载入最新文件整体生效。
