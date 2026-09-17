# AMD AM4 核显 CPU 补证与发布（办公场景恢复）

新增 `cpu-r5-4600g` 一款 AM4 核显 CPU，正式目录从 177 件/125 报价增加到 178 件/126 报价。发布后"办公用、无独显"场景不再被 DISPLAY_OUTPUT_FAIL 完全拦截（发布前目录 28 款 CPU 无任何 AM4 核显款）。

## 依据

- [AMD 官方规格页](https://www.amd.com/en/support/downloads/drivers.html/processors/ryzen/ryzen-4000-series/amd-ryzen-5-4600g.html)（2026-09-17 抓取，见 `artifacts/data-intake-official-igpu-cpu-20260917-r1/manifest.json`）：Name=AMD Ryzen™ 5 4600G、CPU Socket=AM4、Supporting Chipsets=X570, X470, X370, B550, B450, B350, A520, A320、Default TDP=65W、Graphics Model=Radeon™ Graphics。映射为 `socket=AM4`、`supported_chipsets=[8 项]`、`tdp_w=65`、`has_igpu=true`，共 5 条官方字段证据（method=deterministic，evidence_status=verified）。
- 买手报价（观察日 2026-09-17，`aggregator_secondary`，淘宝）：
  - 锐龙R5-4600G带核显无需显卡即可使用，众鑫设备，657.8 元，row_key `ac93790382d4fd6b`（见候选包 `reviewed-offers.jsonl`）
  - 详情请求与响应的聚合 goodsId 不一致，关联依据为请求索引及详情文件哈希，同时比对精确标题、卖家、购买链接和价格；差异在 `detail-review.json` 显式保留。库存未据此确认，不表示实时可购。买手标题未标注散片/盒装，packaging 记录为 unknown。
- 明确暂缓（证据不足，未发布）：
  - Ryzen 5 5600G、Ryzen 7 5700G：官方规格页公布名称/默认 TDP 65W/CPU Socket AM4/Graphics Model，但**未公布 Supporting Chipsets**，`supported_chipsets` 无可追溯 A 级证据；按"未知优于猜测"暂缓，不查枚举猜测补位。买手侧干净候选行已留存（`var/data/collections/2026-09-17-igpu-cpu-01/`，不入库），待官方页公布芯片组或取得人工例外批准后再议。
  - 详见 `detail-review.json` 的 `deferred`。

## 复现与记录

```bash
# 1) 买手搜索/详情（既有 expand_driver 路线，无模型）
#    计划: scripts/data/collection-tools/2026-09-17/search_plan_igpu_cpu*.jsonl
#    详情: scripts/data/collection-tools/2026-09-17/detail_ids_igpu_cpu.jsonl
# 2) 官方页抓取（fetch_intake_specs.py 新增 amd-4600g/amd-5600g/amd-5700g 映射）
PYTHONPATH=scripts/data/collection-tools/2026-09-14:scripts/data/src \
  python scripts/data/collection-tools/2026-09-14/fetch_intake_specs.py \
  --out artifacts/data-intake-official-igpu-cpu-20260917-r1 --pages amd-4600g amd-5600g amd-5700g
# 3) 准备（只重放固定审核资料，无网络调用；输出目录必须新建）
PYTHONPATH=scripts/data/collection-tools/2026-09-14:scripts/data/collection-tools/2026-09-17:scripts/data/src \
  python scripts/data/collection-tools/2026-09-17/prepare_igpu_cpu_intake.py \
  --pages artifacts/data-intake-official-igpu-cpu-20260917-r1 \
  --out artifacts/<新的候选包目录>
```

原始买手搜索/详情保存在 `var/data/collections/2026-09-17-igpu-cpu-01/`（不入库）。

独立数据库演练：`artifacts/data-intake-igpu-cpu-rehearsal-20260917-r1/`；正式发布：`artifacts/data-intake-igpu-cpu-publish-20260917-r1/`。两者均验证 178 件/126 报价、旧商品和报价不变、需求与配置历史不变、报价重试幂等（`model_calls=0`、`external_search_calls=0`）。正式发布使用现有手动审核流程；此文档不是自动发布入口。

发布结果：parts_release `4dc0da59-f63b-5adc-a3b0-cd30394b7bd9`，price_release `529ec581-b4fd-58b0-be69-f78ca33ad618`，快照 ID 11。Diff 仅 1 条 added change + 5 条官方字段证据。

## 发布后验证

用与试用场景 7 相同的办公需求（预算 4000 元、静音、Office/网页/视频）真实模型冒烟（会话 `c2323e57-dfb7-44ec-bf65-4301a8195426`）：

- 方案选中 `cpu-r5-4600g`、`gpu=null`，rationale 明确"自带核显，满足办公/网页/视频需求且无需独显"；无 DISPLAY_OUTPUT_FAIL，不再出现"目录暂无收录核显 CPU"。
- 剩余待解决仅"静音仍待确认是否满足"（软偏好，如实保留，不宣称已满足）。
- 消耗如实记录：model_calls=8、tool_calls=13、search_calls=1、tokens=192,029、duration_ms=93,409。
