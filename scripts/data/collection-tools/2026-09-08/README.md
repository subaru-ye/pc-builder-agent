# 2026-09-08 采集批次脚本

本目录保留该批次的搜索词、匹配规则和逐 SKU 复核决策。它们是可追溯的批次工具，不是 builder/screening 的运行依赖，也不是通用自动发布入口。

- `maishou_driver.py`：19 SKU 试采。
- `maishou_sweep*.cjs`：全目录扫描、补扫和换词复扫。
- `maishou_merge3_20260908.cjs`、`maishou_match*.cjs`、`maishou_review*_gen.cjs`：合并、匹配和生成人工复核底稿。
- `build_full_update_20260908.cjs`：用已保存的结果和决策表离线重建 102 条新价 + 58 条沿用价。
- `fetch_detail_links_20260908.py`、`merge_listing_urls_20260908.py`：获取购买短链及合并链接。
- `mmb_client.py`：当时未成功完成采集的慢慢买试验客户端，不参与本批次快照生成。

需要 Node.js、Python；联网脚本另需 PATH 中的 `uv` 及已安装的外部 taobao skill。通过 `MAISHOU_SKILL_DIR` 指定其目录；慢慢买凭据从 `MMB_API_KEY` 读取。不要把凭据写入代码。

原始数据默认读取仓库下的 `var/data/collections/2026-09-08/`，可用 `COLLECTION_DATA_DIR` 改写。联网采集会更新其中的中间结果，重采前另建批次目录。原始数据不随 Git 提交，需单独备份和传递，文件清单与哈希见 [批次清单](../../price-batches/2026-09-08/manifest.json)。

在仓库根目录离线重建，无需模型、网络或数据库：

```bash
node scripts/data/collection-tools/2026-09-08/build_full_update_20260908.cjs
python scripts/data/collection-tools/2026-09-08/merge_listing_urls_20260908.py
```

重建结果写入原始数据目录的 `rebuild/`，可用 `COLLECTION_OUTPUT_DIR` 改写；不会覆盖仓库中的日期快照或已提交的复核记录。缺失/歧义决策或数量异常会终止生成。人工决策表固定本批次，后续采集应建立新批次，不要直接更新这张表。

最终结果与证据限制见 [批次说明](../../price-batches/2026-09-08/README.md)。
