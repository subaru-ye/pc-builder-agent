# MSI ITX 主板补证与发布

新增 `mb-msi-b650i-edge`（MPG B650I EDGE WIFI），正式目录从174件/122报价增加到175件/123报价。原101个待审候选中此项已完成补证，剩余100个；原逐项处置文件保留，读取当前状态时以本发布记录更新此型号。

## 依据

- [MSI 官方规格](https://www.msi.com/Motherboard/MPG-B650I-EDGE-WIFI/Specification)：准确型号、AM5、B650、DDR5、Mini-ITX、两个M.2；7200+为OC支持标称，不保证所有CPU/内存组合达到该频率。7条字段证据及原网页哈希见 `evidence.jsonl`。
- 原买手报价行 `6ef4311fbdde68cc`，准确主板标题，1450元，齐鲁数码商城东岳店。标题中的CPU型号位于“支持”之后，不将它解释为CPU或套装报价。原搜索和详情的标题、店铺、购买链接及价格一致。
- 原请求和响应的聚合goodsId不同，差异在 `detail-review.json` 显式保留；关联依据为原请求索引及原详情文件哈希，同时比对准确标题、卖家、链接和价格，不默认为两个opaque ID完全相同。
- 报价观察日保留2026-09-13，来源为 `aggregator_secondary`；库存、成色及包装细节未据此获得新确认，不表示实时可购。没有新增买手查询。

## 复现与记录

```bash
PYTHONPATH=scripts/data/src python -m unittest discover -s scripts/data/collection-tools/2026-09-14 -p test_itx_intake.py -v
PYTHONPATH=scripts/data/src python scripts/data/collection-tools/2026-09-14/prepare_itx_intake.py --pages artifacts/data-intake-official-itx-20260915-r1 --out artifacts/<新的候选包目录>
```

准备脚本只重放固定审核资料，使用本机已有PyYAML。原始385行报价中存在跨候选共用聚合行键，故只把准确型号和行键的唯一记录交给通用准备器，不删除或全局去重原文件。输出目录必须新建，不能覆盖既有批次。

独立数据库演练：`artifacts/data-intake-itx-rehearsal-20260915-r1/`；正式发布：`artifacts/data-intake-itx-publish-20260915-r1/`。两者均验证175件/123报价、旧商品和历史报价不变、需求与配置历史不变、报价重试幂等。正式发布使用现有手动审核流程；此文档不是自动发布入口。

发布后快照：`artifacts/data-intake-after-itx-20260915-r1/database-snapshot.json`，SHA256 `08ef04e6af1d8ff7f98995e07b3738c1216c6608d21241171c20f77b3589422e`。商品/报价发布编号及快照ID 9见 `publication.json`。旧174件评估快照及真实结果未修改。

本次采集：Codex网页搜索1次、官方页面打开1次，另经现有采集脚本实际HTTP抓取1页、重试0；没有Builder/Screening真实模型、Embedding、SerpAPI、Firecrawl或买手API消耗。目录扩充尚未进行新的真实模型或浏览器生成验收。
