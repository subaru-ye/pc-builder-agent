# 保存的真实降预算回答：浏览器验收

2026-09-15，业务代码 `b529ea5`，新增测试入口 `internal/planningeval/browser_test.go`。临时数据库由现有评估启动器创建；产品API 18089、Next.js 3107，仅绑定127.0.0.1。不加载真实模型、联网或共享服务配置。

## 证据与范围

使用 `budget_clarification_success_recording_20260915.json` 的7条真实Builder输出及其绑定的174件/122报价目录。历史v1、7500元预算与必须保留显卡的前提复用原L2-104，初始化走现有历史夹具和服务端需求编辑。用户点击需求确认后，生产Runner实际执行保存的检索/校验调用，经A2A编解码、产品服务、PostgreSQL事务和HTTP读模型生成v2；没有前端路由mock，也没有直接插入成功v2。

这是离线浏览器回放，不是新的真实模型通过率。本轮没有重测自然语言Screening、跨会话个人画像、必须静音继续修改或所有历史场景。独立服务未连接真实Builder健康探针及Redis，因此页面显示服务不可用/事件存储降级；生成和状态恢复经上述离线路径完成，不宣称生产健康或SSE恢复已验收。

## 实际浏览器结果

1. 桌面1280×720：需求面板正确显示7500元、游戏、2K和必须保留显卡；确认后无需额外方案确认，自动显示正式v2、6754元、余额746元、父版本v1、缺价0，会话列表同步为配置就绪/2版。
2. 版本页显示显卡未变；切到v1显示“历史只读”和原配件。刷新后URL中的version=1、只读状态及原金额保持。返回当前会话恢复v2，未新增版本或模型调用。
3. 选配说明显示“本轮未联网，使用本地资料”，最终采用资料归并为2个链接/12条依据；另7个链接置于其他检索资料中。展开统计为搜索尝试0、实际搜索0、网页读取0。
4. 移动端390×844：配置详情收纳为独立面板，可返回对话；会话菜单可创建新会话。新会话预算/用途/分辨率保持未知，居中展示3个建议输入；点击建议只填入输入框，没有自动发送。恢复桌面仍保留居中空态及左右导航。DOM测得桌面scrollHeight=720/scrollWidth=1280，移动端844/390，均与视口一致。
5. 复制按钮出现“已复制”，但浏览器剪贴板读取及粘贴仍得到先前测试文本。本项不计为通过：代码调用并等待navigator.clipboard.writeText，当前证据尚不足以区分浏览器工具桥接与实际客户端剪贴板问题。测试文本已清空，未发送；本轮不以修改业务代码猜测解决。

截图和可访问性树保留在本任务的浏览器工具输出中，没有伪称生成独立截图文件。`artifacts/planning-recorded-browser-20260915-r1/observations.json` 保存最后一次服务端状态；`tests.log`、`manifest.json` 记录代码/资料哈希及测试结果。

## 服务端与清理

测试入口最终断言通过：总计7次离线协议响应，v2仅生成一次，报价6754元，v1完整存储记录与初始化时相等。浏览器运行包含人工操作等待336秒，不是模型或服务延迟。实际模型、外部搜索、网页读取新增消耗均0；十四批真实模型累计426次/4820711 tokens不变。

首次页面打开因Next初次编译超过导航等待时间，随后读取同一tab成功，未重复启动服务。临时API经专用shutdown端点正常退出，测试PASS，评估启动器清理自己的数据库容器；Next仅终止本次进程句柄，临时tab关闭、视口重置。Next自动修改的tsconfig已核对并恢复，共享进程未重启。

复现（Git Bash，两个终端）：

```bash
PLANNING_RECORDED_BROWSER=1 GOFLAGS="-run=TestRecordedBudgetBrowserServer -v" python scripts/eval/planning/run.py --out artifacts/planning-recorded-browser-new-run --go-tests ./internal/planningeval --test-timeout 30m
cd web
GO_API_BASE_URL=http://127.0.0.1:18089 NEXT_DIST_DIR=.next-recorded-browser pnpm exec next dev --webpack --hostname 127.0.0.1 --port 3107
```

打开 `http://127.0.0.1:18089/__offline/start`，按上述操作验证；完成后POST `/__offline/shutdown`让测试核验并清理数据库。该入口只存在于显式启用的Go测试进程，不进入默认产品运行入口。单条回复的解释真实性仍有已登记问题，浏览器展示成功不表示该文案已正确。
