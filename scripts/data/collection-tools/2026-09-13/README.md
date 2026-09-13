# 2026-09-13 买手扩库批次工具

目标:发现旧 160 SKU 目录之外的新型号候选(首阶段累计 ≥100 个身份去重候选),
只走买手 skill(search 发现 + detail 详情/购买链接),不用 Crawl4AI/SerpAPI/模型 API。

## 预算与纪律(任务提示词)

- search ≤200 次调用(每次 1 HTTP)、detail ≤120 次(每次 2 HTTP),失败与重试计入;
- 单查询最多 5 页;串行(并发 1);调用间 0.7s;
- 同型号多店铺/多 goodsId = 同一候选的多个 offer,不算多个新增型号;
- detail 未提供的规格一律缺项,不从标题/常识猜造;
- `aggregator_secondary` 全程标注;新候选不入当前产品库,只落候选数据。

## 文件

- `search_plan.jsonl` — 发现关键词计划(131 查询,八类,品牌/系列/缺失档位)。
- `expand_driver.py` — 预算化驱动:search/detail 两阶段,checkpoint 幂等可续跑,
  每次调用落原始文件 + SHA-256 进 raw_index.jsonl。
- `legacy_patterns.py` — 旧 160 SKU 身份正则表(分流 legacy 刷新 vs 新候选)。
- `build_candidates.py` — 汇池:去重(source+goodsId)、垃圾词 flag、legacy 分流。
- `emit_candidates.py` — 合并复核决策 + offer + detail 规格 → 最终交付 JSONL。

## 原始数据(不入库)

`var/data/collections/2026-09-13-maishou-expansion-01/`:
`raw/search/*.csv`、`raw/detail/*.yaml`、`raw_index.jsonl`(每次调用的
关键词/平台/页码/时间/SHA-256)、`checkpoint.json`(预算状态,恢复权威)。

## 复现

```bash
export MAISHOU_SKILL_DIR='C:\Users\83818\.qoder\skills\taobao'
cd scripts/data/collection-tools/2026-09-13
python expand_driver.py search --plan search_plan.jsonl   # 发现
python build_candidates.py                                # 汇池
python expand_driver.py detail --ids detail_ids.jsonl     # 详情+购买链接
python emit_candidates.py                                 # 出交付物
```

交付物与统计见 `scripts/data/catalog-candidates/2026-09-13/README.md`。
