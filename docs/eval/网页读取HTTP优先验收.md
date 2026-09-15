# HTTP 优先网页读取验收（2026-09-14）

本轮落实普通 HTTP 优先、Crawl4AI 按需使用，并修复正文丢失与浏览器生命周期。没有新增模型、Embedding 或 SerpAPI 调用，没有修改商品库、价格、用户会话或历史方案。买手补库工作区文件未参与本次改动。

## 实现与范围

- 默认 `read_page` 先 HTTP，仅动态空壳自动尝试浏览器。模型可显式选 browser 读取动态资料；访问验证、登录、Cookie 同意墙、限流、网络故障不自动升级。服务不可用不阻断本地规划。
- HTTP 解码 GBK 等网页字符集，保留段落及短表格行。浏览器使用已观测的最终 URL/状态，修复初始 307、最终 200 被误拒。
- 完整有界正文保存在方案原有 evidence 快照，每页最多 2 MiB。模型单次窗口最多 16000 字，可按 query 定位、按 next_offset 续读。`read_evidence` 不联网，跨轮恢复可继续使用；旧证据不回填、不重抓。
- 受控 Docker `/read` 接口每次只接收一个 URL。每项任务独立浏览器进程组和临时状态，实际并发 2、等待 8、总期限 45 秒。取消、超时、退出、正常完成都先回收进程再释放容量。该选择增加启动成本，换取可核验的生命周期与会话隔离；没有声称复用浏览器后仍保持相同性能。
- `read_attempts` 区分物理 HTTP/浏览器读取及失败；搜索额度和模型调用上限不变。HTTP、A2A 所承载的规划结果及前端生成类型同步扩展，历史字段仍可缺失。

## 离线验证

全部通过：

1. `internal/planning`：HTTP 优先、手动 browser、动态空壳升级、访问限制不重试、GBK、表格、中文字符偏移、相关定位、续读拼接、序列化恢复、最终跳转状态、登录/图片入口/Cookie 页面拒绝、来源不随窗口修改。
2. 原始 A/B 快照回放：ZOL 原始 GBK 响应重新解码；ZOL 浏览器正文的插槽/功耗/内存/适用声明可通过窗口读取；Noctua 已记录的 307→200 和五项尺寸/重量/保修信息可用；原京东和淘宝入口不再作为正文。
3. 本轮六个参照页的真实新响应回放，检查规格和条件能进入模型读取窗口。Kingston 的兼容性段与电压/通道表使用两个本地窗口，不声称一次截取包含全文。
4. `pipeline`、`product`、`producthttp`、`schemas`、`store` 单测及 `go vet ./internal/planning`；前端 OpenAPI 生成和 `pnpm typecheck`。
5. Docker 离线浏览器测试 **7 项通过**：真实 Chromium、真实回环 HTTP，在 `--network none` 中通过本地路由提供页面。覆盖正常结束、HTTP 断开、超时、并发/等待上限、服务退出、请求大小/频率上限和失败计量，断言没有存活浏览器进程和遗留任务目录。不是只测 Go 请求取消，也不是外网压力测试。

原始 A/B 来自独立评估提交 `92c9dcc`。大型响应保留在原哈希归档中；本仓库测试通过环境变量读取，不假装 CI 默认拥有这些原始文件。

```bash
PAGE_READER_RECORDINGS=C:/code/pc-builder-agent-ab-eval/artifacts/crawl4ai-ab-20260914 \
PAGE_READER_RECHECK=C:/code/pc-builder-agent/artifacts/page-reader-recheck-20260914 \
go test ./internal/planning -count=1

go test ./internal/agents/pipeline ./internal/product ./internal/producthttp ./internal/schemas ./internal/store -count=1
go vet ./internal/planning

# Git Bash；只创建并清理独立测试容器，无外网访问。
MSYS_NO_PATHCONV=1 docker run --rm --init --name pcbuilder-reader-lifecycle-20260914 \
  --network none --shm-size 1g --memory 4g --cpus 2 --entrypoint python \
  -v C:/code/pc-builder-agent/deploy/crawl4ai/reader.py:/app/pc-reader/reader.py:ro \
  -v C:/code/pc-builder-agent/deploy/crawl4ai/tests:/tests:ro \
  unclecode/crawl4ai:0.9.3 /tests/test_reader.py
```

## 同网址实际复测

原来 12 个网址，固定 HTTP/browser 各一次，共 24 次读取；之前另做 2 次 Kingston 连通性探测，**本轮实际外网页面读取合计 26 次**。没有搜索、模型调用或失败重试。显式双路径是 A/B 测量安排，不是生产 auto 策略。使用独立端口 11336、独立随机服务凭据，运行后删除专用容器，没有重启共享产品服务。

| 页面 | HTTP 秒 | 浏览器秒 | 实际观察 |
|---|---:|---:|---|
| Kingston 知识页 | 0.721 | 9.099 | HTTP 403；浏览器保留兼容性、电压、通道内容，但完整文本含大量关联内容，需定位窗口 |
| AMD 5900X | 0.350 | 7.534 | 两种方式均保留规格，普通 HTTP 已足够 |
| ZOL 参数 | 0.505 | 4.559 | HTTP 中文解码正常；浏览器完整正文保留，相关词定位能读取尾部参数 |
| ASUS FlashBack | 0.193 | 4.730 | 两者保留操作步骤及适用条件 |
| MSI 规格 | 0.270 | 10.360 | HTTP 403；浏览器包含内存、ECC、M.2/PCIe 排他及集显前提 |
| MSI 支持 | 0.049 | 7.744 | HTTP 403；本次浏览器只得到 Cookie 提示和帮助入口，没有目标支持表 |
| Noctua | 0.555 | 5.499 | HTTP 429；浏览器最终 200，规格可用，不再因初始跳转误拒 |
| Gigabyte 支持 | 0.147 | 5.137 | HTTP 403；浏览器有支持资料，未证明特定 CPU 最低 BIOS 行已取得 |
| Crucial 安装页 | 1.197 | 7.693 | HTTP 的 Request Rejected 被识别；浏览器页面失败，不作为知识证据 |
| 什么值得买 | 0.131 | 4.247 | HTTP 202；浏览器保留历史优惠文章，不代表当前价格 |
| 京东短链 | 0.117 | 4.349 | HTTP 空壳；浏览器抓取失败，不作为商品证据 |
| 淘宝短链 | 0.204 | 2.860 | HTTP 空壳；浏览器抓取失败，不作为商品证据 |

复测时发现 MSI Cookie 提示误报，在随后代码中修复，并用该保存响应离线回放验证；没有为改善结果反复请求。表中仍保留当时观测。成功读取的 HTTP 中位数为 **0.350 秒**；浏览器在剔除这个 Cookie 空壳后为 **5.318 秒**。成功集合不同，不能把该比值推广到所有网页。六个有参照页的完整浏览器结果可回溯原检查点，但本轮窗口式读取与旧一次截断输出的分母不同，不发布一个混合的“最终准确率”。

## 资源与限制

- 同为 2 CPU/4 GiB 上限、1 GiB shm。复测容器 `memory.peak=538767360`，约 **514 MiB**；结束时 `memory.current=73433088`，约 **70 MiB**；在途/活动任务均为 0，采样时 pids.current=3（含采样进程）。这是一次顺序样本，不是生产容量承诺。
- 容器 CPU 累计约 **57.75 CPU 秒**，包含启动及服务活动，不能当作每页纯增量；浏览器外网字节没有单独采集。保存的 read_bytes 是客户端响应体字节，不包含全部子资源流量。
- 未新增付费 API；不能把“未付 API 费”写成计算、网络、运维零成本。Token 成本仍取决于模型实际调用的窗口数量，本轮没有实际模型回答或费用验证。
- 实际网络、网页版本与时间点均可能变化；不将原 1.23 GiB 与本次峰值直接解释为普遍节省比例。新实现不复用浏览器，首次/重复读取的成本模型已变化。
- 本轮没有运行整个产品 UI 的装机对话，也没有再次调用真实 Screening/Builder；工具执行、结果快照与相关业务回归通过离线测试验证。知识回答准确率、长期运行、目标站点稳定性及生产吞吐仍须另行评估。

新结果：`artifacts/page-reader-recheck-20260914/`；探测：`artifacts/page-reader-recheck-probe-20260914/`。完整保存每页正文、错误、实际调用信息与耗时，另保存最终 service-metrics/cgroup 数据。文件哈希清单见同目录的 `manifest.json`。大型正文不进入 Git。

```bash
# 会真实访问公网；新目录，不覆盖旧结果。最多 24 次，原 12 页，无搜索/模型。
python scripts/eval/page-reader/run.py \
  --dataset scripts/eval/page-reader/dataset.json \
  --out C:/code/pc-builder-agent/artifacts/page-reader-recheck-new \
  --max-reads 24
```

服务运行与工具参数见[网页读取与补库策略](../tech/网页读取与补库策略.md)。
