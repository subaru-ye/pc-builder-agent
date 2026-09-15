# Firecrawl 与现有网页读取方案对比（2026-09-14）

结论：**保留普通 HTTP 作为优先读取方式；Firecrawl 值得作为托管的动态网页读取选项试接入。当前测试不支持全面替换 HTTP，也没有证明它能解决电商实时价格和动态 CPU 支持表问题。** Crawl4AI 可以保留为需要本地控制时的选择，不建议每个请求依次调用三个读取器。

本轮只新增评估脚本、记录和文档，没有修改业务默认路由、服务配置或数据集，没有将联网资料写入商品库。

## 范围与可比性

复用此前 12 个网址和预先保存的参照点，重新实际读取当前工作区的 HTTP 与 Crawl4AI 实现；没有用上一份报告的耗时冒充本轮基线。三种方案对每个网址独立读取一次，不是 HTTP 失败后的自动绕过测试。

- HTTP：现有 Go 读取器，含编码处理、正文清理与质量检查。
- Crawl4AI：镜像 `unclecode/crawl4ai:0.9.3`，现有项目 `/read` 适配器；独立容器，2 CPU / 4 GiB 上限，0.5 秒页面等待，缓存旁路。
- Firecrawl：当前可用 MCP 连接调用托管服务，`formats=["markdown"]`、`onlyMainContent=true`、`maxAge=0`、`proxy="basic"`。未使用登录、点击交互、高级代理、AI 提取或搜索附带抓取。
- Firecrawl 额外重复读取 AMD、MSI 规格页各一次，再做 3 个中文/硬件搜索，每次 3 个结果。
- 共 **38 次显式页面读取 + 3 次搜索**；自动重试 0。未调用独立模型、Embedding 或 SerpAPI。
- 六个有可信参照的页面共 27 个信息点；模型辅助人工检查相邻标签、型号、单位、否定和适用条件。数字表示**来源信息保留率**，不表示硬件真实性或最终回答准确率。未取得可信原文的动态支持页不纳入分母。

Firecrawl 时间包含 MCP 往返和云端服务；HTTP 为本机直连，Crawl4AI 为本机 Docker 调用。三者出口、缓存实现和执行开销不同，衡量的是本机使用时的端到端体验，不是隔离网络因素的引擎跑分。调用按固定顺序完成，没有随机交叉、长期采样或并发容量测试。Firecrawl 内部重试与资源开销不可观测。

## 主要数据

| 项目 | 普通 HTTP | Crawl4AI | Firecrawl |
| --- | ---: | ---: | ---: |
| 首次读取数量 | 12 | 12 | 12 |
| 取得有意义正文 | 2/12 | 7/12 | 7/12 |
| 完整正文保留参照点 | 9/27 | 22/27 | 22/27 |
| 无查询词、前 16,000 字符保留点 | 9/27 | 18/27 | 22/27 |
| 全部请求中位耗时（含失败） | 0.260 秒 | 7.468 秒 | 3.795 秒 |
| 有意义正文的中位耗时 | 0.308 秒 | 6.956 秒 | 5.233 秒 |
| 本轮最长请求 | 1.209 秒 | 37.737 秒 | 12.904 秒 |

“有意义正文”包括技嘉产品说明和什么值得买的历史优惠，不代表已回答最低 BIOS 或当前价格。两种浏览器读取方式成功的七页相同；HTTP 只成功两页，所以三列“成功耗时”不能直接当等质量任务的倍数。

两种浏览器方式共同读到的五个可信规格/知识页，中位数也是 **Crawl4AI 6.956 秒、Firecrawl 5.233 秒**。但中关村单页 Firecrawl 12.904 秒，比 Crawl4AI 4.774 秒更慢，不能说每页都更快。

本轮华硕页面：HTTP 404、Crawl4AI 读取失败约 37.7 秒、Firecrawl 得到 502 维护页。此前曾成功，本轮三者都丢失该页五个信息点，说明网站当时状态会影响结果。

## 正文质量和实际边界

- **中关村参数页**：HTTP 已正确处理 GBK，四项信息均保留。Crawl4AI 返回 61,153 字符，关键参数在约 27,000 字符后；Firecrawl 清理成 7,763 字符，型号、TDP、内存与适用声明均在首段范围内。
- **MSI 规格页**：两者都保留 4 槽/128GB、ECC 只能以 non-ECC 模式使用、M.2/PCIe 排他条件及显示输出依赖集显 CPU。Firecrawl 的 Markdown 对下划线有转义，评审时规范化，不算信息丢失。
- **Noctua**：两者均保留带扇/不带扇尺寸、重量与保修。按参照页当时的 168mm/160mm 判断保留情况，没有用记忆“纠正”来源。
- **Kingston 知识页**：两者都读到重点，但 Firecrawl 仍返回约 85,470 字符，Crawl4AI 约 88,557 字符。“清洁 Markdown”不等于完全去掉推荐内容或低 Token。
- **动态支持表**：MSI 的 Firecrawl 结果只有栏目入口；Crawl4AI 是 Cookie 提示并被拒绝。技嘉两者都读到产品说明，却没有 5900X 对应的最低 BIOS 行。默认读取不能保证交互式表格被展开。
- **无效页**：Firecrawl 的华硕 502、Crucial 404、京东单个句点、淘宝单个 X，均返回非 MCP 错误结果且各报告 1 credit。不能只判断工具调用是否成功。HTTP 当前也存在将 Crucial 的 HTTP 200 “Request Rejected” 当作正文的问题，本轮记录了问题，没有顺带修改业务代码。
- **电商价格**：京东/淘宝短链均没有可用商品正文；什么值得买保留的是 2023-02-20 的优惠及当时价格，不能作为现在的报价。Firecrawl 不替代精确型号匹配、报价时间校验和买手数据的人工复核。

Firecrawl 官方也明确区分 API 调用成功与目标页 HTTP 状态，接入时需要同时检查后者及正文质量。[官方 Scrape 文档](https://docs.firecrawl.dev/features/scrape)

## Token 与费用

使用已有镜像内 tiktoken 0.14.0，在 **network=none** 的独立短命容器里读取已有词表计数。这里列 `o200k_base` 正文 Token；也保存了 `cl100k_base` 计数。未调用模型，不含提示词、上下文、工具包裹及重读窗口，不代表实际 Builder 账单。

| 共同成功页面 | HTTP 全文 | Crawl4AI 全文 | Firecrawl 全文 |
| --- | ---: | ---: | ---: |
| AMD | 523 | 1,191 | 1,075 |
| 中关村 | 2,908 | 27,521 | 3,217 |
| MSI 规格 | 不可用 | 2,745 | 2,522 |
| Noctua | 不可用 | 2,894 | 1,320 |
| Kingston | 不可用 | 23,360 | 22,055 |

五页全文合计：Crawl4AI **57,711**、Firecrawl **30,189**，后者少约 48%，主要由中关村页面贡献。若都只取前 16,000 字符：分别 **16,264** 和 **11,956**；此时 Crawl4AI 缺失中关村四项重点，因此不能单纯按 Token 判胜负。

当前服务端保留全文并支持 `query/offset` 定位。18/27 是“从头一次读取”的结果，不是永久丢失；这次没有调用真实 Builder 验证它是否能主动找回遗漏，也未测后续模型 Token/往返和生成时长。Firecrawl 尚未接入这个业务窗口，本报告对其做相同窗口的离线模拟。

实际返回用量：**14 次 scrape × 1 + 3 次 search × 2 = 20 credits**。其中已知的维护页、404 和两条空壳共 4 credits，没有转化为有效资料。上个任务的 4-credit 小试单独存放，不计入本轮。未核验账户余额或现金扣费，不把 credits 换算为确定人民币费用。

按本次基本选项估算，一轮“1 次搜索 + 3 页正文”是 5 credits；100 轮约 500 credits，尚未计失败、重复或额外选项。实际限额和费用按账户与官方当前规则核对。[官方定价](https://www.firecrawl.dev/pricing)

Crawl4AI 本轮容器 cgroup 内存峰值约 **784 MiB**，累计 CPU 使用约 **72.6 CPU 秒**；结束时 active/pending 均为 0，容器已删除。这个峰值不含整台 Docker VM，也不是进程 RSS；2 CPU/4 GiB 是限制，不是实际消耗。HTTP 没有单独采样 CPU/内存，不能填成零；Firecrawl 云端 CPU/内存无法观测，转由服务商承担并计量。

## 搜索补充测试

| 查询 | 耗时 | 返回的 3 条结果 | 判断 |
| --- | ---: | --- | --- |
| 锐龙 5900X B550 A PRO CPU 支持 BIOS | 1.401 秒 | Reddit、ASRock、Facebook | 有线索，含其他厂商/型号，不能确定目标 BIOS |
| site:msi.com "B550-A PRO" "5900X" BIOS | 0.773 秒 | 三条 MSI 用户论坛 | 限定厂商域仍不等于官方规格资料；没有目标支持表 |
| DDR4 DDR5 内存 不能混用 主板 兼容 | 0.701 秒 | Oscoo、Reddit、Corsair FAQ | 知识问答线索较好，有厂商 FAQ；本轮未继续读取这些正文 |

三次搜索都没有附带抓取，也没有把摘要当作规格证据。**没有对 SerpAPI 做同期同查询 A/B，所以不能判断 Firecrawl 的搜索效果优于 SerpAPI。** 它将搜索和正文读取放在同一套接口里的便利性是真实的，但检索相关性仍需测试。[官方 Search 文档](https://docs.firecrawl.dev/features/search)

两次重复 scrape 均与首次 Markdown 完全一致：AMD 3.056 → 5.770 秒，MSI 规格 3.471 → 3.265 秒；每次 1 credit。样本过少，不足以证明长期稳定性或尾延迟。

## 对本项目的建议

1. **普通 HTTP 优先**：公开静态规格、指南、FAQ 能读到且信息齐全时，直接使用。AMD 例子里更短、更快，不需要强制云端读取。
2. **Firecrawl 作为可切换的托管读取选项**：公开页面有动态空壳、关键表格未展开或清理质量不足时，在既定调用额度内使用。保留现有来源快照、字段关联和正文窗口，不能将 API 成功直接视为证据成功。遇到登录、验证码、403/429，应遵循现有策略换公开资料或解释受限，不自动轮换三个工具绕过。
3. **Crawl4AI 保留本地控制能力**：需要控制环境和数据流、做定制读取时有价值；本轮没有发现只有它才能取得的目标信息，不必默认常驻调用两套浏览器方案。
4. **搜索与读取分开选供应商**：先保留 SerpAPI，Firecrawl 搜索作为独立开关试用；当前没有证据支持立即替换搜索。不要为了“一套工具”同时改变检索和读取，导致后续无法定位收益来源。
5. **继续扩充结构化数据集**：联网用于新型号、缺规格与知识问答补充；已核验的常用型号/报价仍走数据集。本次结果没有解决大规模电商商品、SKU 变体绑定和实时价格问题。

正式接入前需补的范围：限额耗尽与熔断、取消/超时、并发与长期稳定性、缓存报价时效、最终 URL 与来源时间留存、云端数据保留策略、真实 Builder 从搜索到引用的成本/答案质量。现有测试只支持小规模只读试点，不构成生产容量或整条 Agent 链路验收。

## 证据与复现

原始记录在仓库忽略目录 `artifacts/firecrawl-comparison-20260914/`：

- `baseline/`：24 次现有 Go 读取结果、容器指标及哈希。
- `firecrawl/`：12 次首次、2 次重复、3 次搜索的请求参数、开始时间、耗时、正文及精简元数据。清除了无关站点元数据，未记录 API key。
- `requests.json`：本轮 Firecrawl 调用清单，可按顺序重放到对应 MCP 工具。
- `rows.csv`、`summary.json`、`point-review.json`、`contexts.txt`、`tokens.json`：逐页数据、评审依据及统计。
- `run-manifest.json`：数据、证据、词表和实际读取源码的 SHA-256；HEAD 之外的未提交读取改动也保存源码快照。

先重新统计已有证据（不联网）：

```bash
python scripts/eval/firecrawl-comparison/inspect.py
python scripts/eval/firecrawl-comparison/analyze.py
python scripts/eval/firecrawl-comparison/tokenize.py --cache C:/code/pc-builder-agent-ab-eval/artifacts/crawl4ai-ab-20260914/tokenizer-cache
python scripts/eval/firecrawl-comparison/analyze.py
```

HTTP/Crawl4AI 重新抓取使用现有脚本，必须指定不存在的新目录，最多 24 次，网络结果不会保证相同：

```bash
python scripts/eval/page-reader/run.py --dataset scripts/eval/page-reader/dataset.json --out artifacts/firecrawl-comparison-rerun/baseline --max-reads 24
```

Firecrawl 本轮走 MCP，不伪称 REST 命令是相同传输路径。按 `requests.json` 的 `tool/request` 调用对应工具即可重放；需要现有连接可用，重新执行将消耗额度。保存工具端到端时间及返回的 `creditsUsed`，不得加入额外重试或自动抓取。公开网页内容可能变化，离线评分应以同次快照为准。

Windows 普通命令通过 PowerShell 启动 Git Bash 执行。当前评估未提交代码，原有未提交业务修改保持原状。
