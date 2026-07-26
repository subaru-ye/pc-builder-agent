# DIY 装机配置单 Agent — 产品调研

> 调研日期:2026-07-26 · 定位:个人学习向项目(目标技术栈:多 Agent 流水线 + A2A + 记忆基座)
> 前置结论:**值得做**。竞品要么"手动工具无 AI",要么"纯 LLM 生成不可靠",恰好留出了"LLM 意图理解 + 规则引擎硬校验"这个技术定位;数据源有现成开源数据集,冷启动无爬虫负担。

## 一、竞品格局

### 1.1 国际

| 产品 | 形态 | AI 程度 | 关键点 |
|---|---|---|---|
| **PCPartPicker** | 手动选件 + 兼容性引擎 + 多商家比价 | 无对话 AI,有按预算的自动配置生成器 | 事实标杆。兼容性规则覆盖:插槽/芯片组、内存代数、电源功率、显卡长度对机箱限长等([techfuelhq](https://techfuelhq.com/tools/pc-builder/)) |
| **Newegg "Build with AI"** | 自然语言描述 → 可编辑配置单 | ChatGPT 驱动(2023 年即推出插件) | **口碑差**:上线时被 PC Gamer/Dexerto 评为"离谱""absolutely useless",纯 LLM 输出预算分配失衡、搭配不合理([PC Gamer](https://www.pcgamer.com/newegg-ai-pc-builder-generator/)、[Dexerto](https://www.dexerto.com/tech/neweggs-chatgpt-powered-pc-builder-useless-2098496/));现版本靠"过滤到兼容组合"补救,官方仍建议用户自行核对边缘情况([Newegg](https://www.newegg.com/insider/how-to-use-newegg-custom-pc-builder-step-by-step/)) |
| **BuildCores** | 3D 装机模拟 + AI 推荐 + 电商实时价格 | AI 按预算/需求推荐搭配 | 有中文版分发,主打 3D 可视化与价格追踪([buildcores 中文站](https://www.buildcores.com.cn/download.html)) |
| 一批 AI 套壳站(pcbuilderai.com、aipcbuilding.com 等) | 对话问预算/用途 → 输出配置单 | 纯 LLM | 无兼容性硬校验、无实时价格,同质化严重([AI PC Building 对比文](https://aipcbuilding.com/blog/ai-pc-builder-vs-pcpartpicker)) |

### 1.2 国内
- **中关村在线"模拟攒机"**([zj.zol.com.cn](https://zj.zol.com.cn/))、**太平洋"自助装机"**([mydiy.pconline.com.cn](https://mydiy.pconline.com.cn/)):十年前形态的手动攒机器,选件+简单兼容检查+生成配置单,**均无 AI 对话能力**,价格数据滞后。
- B 站/抖音 UP 主配置单、贴吧图吧文化:需求旺盛但全靠人肉;linux.do 等社区有用户发帖求"AI 装机方案"([linux.do](https://linux.do/t/topic/424175)),需求真实存在。
- 腾讯云开发者社区已有"用 AI 做配置单生成工具"的教程文([腾讯云](https://cloud.tencent.com/developer/article/2522774))——作为学习项目题材已被认可,但没有出现成型产品。
- **结论:国内没有"对话式 AI + 硬兼容校验 + 实时价格"三者兼备的产品,空位真实存在。**

### 1.3 通用大模型(替代品威胁)
- ChatGPT/豆包/DeepSeek 都能直接配单,也确实被大量使用;但三个固有缺陷:知识截止(新平台如新一代 CPU 发布后长期答错)、价格失真(训练数据里的历史价)、幻觉搭配(编造不存在的型号组合)。
- 豆包"帮你选"等购物助手在向该场景渗透([CSDN/DeepSeek社区](https://deepseek.csdn.net/6a05a0bd10ee7a33f2727c96.html)),但通用助手不做硬约束校验——这正是 Newegg 翻车的原因,也是本项目的差异化立足点。

## 二、数据源可行性(学习项目的生死题)

| 数据 | 来源 | 可行性 |
|---|---|---|
| 零件参数库 | [docyx/pc-part-dataset](https://github.com/docyx/pc-part-dataset)(PCPartPicker 全品类爬取,JSON/CSV 现成)| ★★★ 直接可用,冷启动零爬虫 |
| GPU 详情库 | [painebenjamin/dbgpu](https://github.com/painebenjamin/dbgpu)(2000+ GPU,架构/接口/性能,pip 可装) | ★★★ 直接可用 |
| CPU/GPU 性能分 | [passmark-scraper](https://github.com/ading2210/passmark-scraper)、[TechPowerUp scraper](https://github.com/BaraaZ95/Techpowerup-scraper/) | ★★☆ 现成脚本,低频跑一次即可 |
| 国内实时价格 | 京东/淘宝无公开 API,需爬虫或手工维护主流 SKU 价格表 | ★★☆ 建议 MVP 阶段用"手工价格表 + 报价快照"设计,把价格时效性问题转化为架构展示点 |
| 兼容性规则 | 自建规则表(插槽↔芯片组、DDR 代数、限长/限高、功耗余量) | ★★★ 纯工程,规则量可控(主流平台 Intel LGA1851/AMD AM5 起步) |

## 三、差异化定位与技术展示点

竞品的失败与空缺,恰好映射到目标技术栈的每个组件:

1. **"LLM 不裸配单"**——Newegg 翻车的反面教材:
   - 初筛 Agent(ADK):对话收集预算/用途/偏好 → 结构化需求(A2A 消息)
   - 配置生成 Agent:pgvector 语义检索零件库("安静的显卡"→ 噪音参数过滤)+ 预算分配策略
   - **校验核算 Agent:纯规则引擎**,兼容性硬校验 + 功耗余量 + 性价比核算(PassMark 分/元),LLM 只解释结果不参与判定
2. **报价快照**:价格表带时间戳,配置单版本绑定当日价格——"方案版本+报价快照"的经典设计,且装机场景价格波动真实存在(显卡价格周波动)。
3. **迭代修改**:"换 A 卡""降 500 优先砍哪"→ 增量修改而非重生成,体现多轮上下文与方案 diff 能力。
4. **记忆基座**:PostgreSQL(零件库/配置单版本)+ pgvector(语义选件)+ Redis(会话热上下文),与目标栈一一对应。

## 四、风险与范围控制

1. **零件库维护量**:全品类会失控。MVP 收敛到:当代主流平台(AM5/LGA1851)+ 每类 20-50 个主流 SKU,数据集裁剪即可。
2. **价格时效**:不承诺实时,承诺"快照日期透明"——这在产品上诚实,在架构上反而是加分项。
3. **兼容性长尾**(BIOS 版本支持、内存 QVL 等):明确不做,留免责说明,与 Newegg 同样的边界但明说。
4. **同质化风险**:AI 套壳站一片,项目辨识度必须落在"多 Agent 分工 + 规则引擎兜底 + 版本快照"的架构上,而非"能生成配置单"的功能上。

## 五、结论

- **做。** 需求真实(社区人肉配单文化旺盛)、国内竞品空位(手动工具无 AI,AI 套壳无校验)、数据源现成(开源数据集冷启动)、且业务流程与目标技术栈(ADK + A2A + 多 Agent + 记忆基座)每个组件都有非人为的落点。
- 作为学习项目的额外收益:领域知识门槛低,自己就能验证输出质量;做完可发到装机社区收真实反馈。
