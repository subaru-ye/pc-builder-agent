# Builder v2 评估产物可复核索引（2026-09-27，HEAD abdc46e 时点）

`artifacts/` 在 .gitignore 中不入库。本索引为需留存产物的 sha256 清单，供后续复核
（文件移动或清理后可凭哈希对照备份）。生成方式：`sha256sum` 逐文件；suite/provenance
未列出的 replay 目录（mech v35–v40 等）仅报告归档，关键结论已在 change 文档与运行
记录中以文字登记。

## Pass³ live 批次

| 批次 | 目录 | 文件 | sha256 |
| --- | --- | --- | --- |
| Pass³ v4 r1（9/10，候选 2f790d2） | `artifacts/builder-v2-20260925-pass3v4-r1-20260927` | plan.json | `96048b2d3a2272ab14b902409a6acac716212229a452a08057ff02a02db5020c` |
| | | report.json | `b2c4bd745441560c118117bed994f2884837efe65c0a0c3ea33a8e4831e5058d` |
| | | events.jsonl | `f701a90c4e673ab73b54434236a0451af95a0e936468c7ba62c1672f2a3e5b6d` |
| | | manifest.json | `c68b080cf1cfcedac0297d6bf26fdedf56aed07aa49066301e45b7ef75e2542c` |
| Pass³ v4 r2（10/10） | `artifacts/builder-v2-20260925-pass3v4-r2-20260927` | plan.json | `8d1231173402f458ed6771fd8f123db36aed2be4f640202ad727de0cef5f5a51` |
| | | report.json | `259ebc13f0c0de11227728b28affde3f459004dcf4b078205ce514d6f550fb6e` |
| | | events.jsonl | `0671fc8beb1beb5cf664296da6ddfeb8796fbd0ef29dfa54d90789a29c13eb85` |
| | | manifest.json | `21c73ac6e4bbf2028f13747495551efc4ec38f8c043b858c76028af0464a2d68` |
| Pass³ v4 r3（10/10） | `artifacts/builder-v2-20260925-pass3v4-r3-20260927` | plan.json | `94418ccf7464ced8e7509b5de2c3d759edcc93690ee3cbd258e77a71473b9ff3` |
| | | report.json | `cc7bd0447a1f714283eec5f0ff4057cf77f3704c07d22177a53bf201e9d11604` |
| | | events.jsonl | `c7ea703d99e1d87dee188597d08f91fb7ad164d27e19c8d501f5d924acbe3b0d` |
| | | manifest.json | `21c73ac6e4bbf2028f13747495551efc4ec38f8c043b858c76028af0464a2d68` |
| Pass³ v3 r1（10/10，候选 4fc6077） | `artifacts/builder-v2-20260925-pass3v3-r1-20260927` | plan.json | `1586f54651811c9ec664bf6d312c4d72fc27c6fb42344614b235f9f34bdbf404` |
| | | report.json | `077b6bf75c8fc3a5ea882a11b2b6c639f20619ef07741fd15f75204242b3b2d7` |
| | | events.jsonl | `e1a87d9386321ad0c4076c4c342fe916963e5161d9d9b2986976b3457baf43cb` |
| | | manifest.json | `b6fda45fc19186bd125bf4dd5d664b68d0fdabbce553fea6b30ab361dc63678f` |
| Pass³ v3 r2（10/10） | `artifacts/builder-v2-20260925-pass3v3-r2-20260927` | plan.json | `8b2840bf14ed701ace996a283aad12dd1da29255f41de7b8e5f123b1d0386174` |
| | | report.json | `2710b49016e1726d6eaee1366616a150ea9737036440a8721c96309c972b87f2` |
| | | events.jsonl | `b1cd71a85c93496a9e130df050b9b1ed2d8ea9a0001ee52cb8c01b301fca4bfa` |
| | | manifest.json | `b6fda45fc19186bd125bf4dd5d664b68d0fdabbce553fea6b30ab361dc63678f` |
| Pass³ v3 r3（8/10） | `artifacts/builder-v2-20260925-pass3v3-r3-20260927` | plan.json | `a31fada6f9bba577a35fe72c54b453edaf24a01f1ce57243fda387d930878d45` |
| | | report.json | `3bd499ee218fd1a2b144a634f1e5b6816e9a66d3dbdb647a1dc8192c4ef976fa` |
| | | events.jsonl | `7edcbe14a45bcf3b72f847aa4c7b5e7da1c0f0cc013678f905714c50c68962e8` |
| | | manifest.json | `58192613d47ae35856c8efef36fc79dd1799428b99916fb1c378ea1be309eff7` |
| 定向 live BV2-104（1/1，HEAD 4f93f83 时点） | `artifacts/builder-v2-20260925-bv2104-directedlive-b-20260927` | plan.json | `0628f079042868ef10013f4dd99663dbd3f060364d146f376b85c83bcc91b6d9` |
| | | report.json | `747320bd779a31f99d22184e4031586b3fc53718a658dc92fef79897decc86d9` |
| | | events.jsonl | `809600d458b3f4983b5c6a8cddc9154d49c61ef210e1e34e5039786ccf107a20` |
| | | manifest.json | `ef8857e0f5bfa53a2dc5595f9795c206f19bacc0e029e8646fc990b156240223` |
| 定向 live BV2-105（1/1） | `artifacts/builder-v2-20260925-bv2105-directedlive-b-20260927` | plan.json | `12d7ffef7d4edebde48508d9411a85597f92a879822b987afcc648d6ed39936f` |
| | | report.json | `d460faabdf574d9c76197b88842dc082fea61de6efc1edd27fbab2435d9004ff` |
| | | events.jsonl | `d30e1484c4ca7b15d0ff6f85e8a9bb1979cccd4551955637864e630b95c8f42e` |
| | | manifest.json | `ef8857e0f5bfa53a2dc5595f9795c206f19bacc0e029e8646fc990b156240223` |

**manifest head 说明**：pass3v4 三轮 manifest head=`2f790d2`（运行于该提交之后）；
pass3v3 三轮 head=`4fc6077`；定向 live head=`4f93f83`——定向验证与 mech v38/v39 replay
运行于各自冻结提交**之前**（工作树内容即随后提交的内容），manifest 记录的是当时 HEAD，
非最终冻结提交号；对照关系：mech v38（head 65253cf）→ 冻结 4fc6077；mech v39（head
4f93f83）→ 冻结 b63db12；mech v40（head b63db12）→ 冻结 2f790d2。

## 其他

- `artifacts/builder-v2-20260925-pass3v4-report-20260927.md` / `...-pass3v3-report-20260927.md`：
  逐轮报告（本地存档）。
- `artifacts/builder-v2-20260925-regrade-grading-v2-20260927/`：六份旧报告的 grading-v2
  重判 JSON＋MANIFEST.md（无 manifest.json/events，重判为零模型离线操作）。
- `artifacts/builder-v2-20260925-*-preflight-20260927`：plan-live 零调用预检（仅 plan）。
- 单题派生套件已入库：`internal/planningeval/testdata/builder-v2-live-20260925-grading-v2/single-bv2-104|105/`。

## 复核口径

1. 逐轮分数/用量以 report.json 为准（本索引锁定其哈希）；manifest.json 记录 Git HEAD、
   二进制与逐源码哈希、退出码（exit=1 系"未全过即退出"既定语义）。
2. events.jsonl 为 O_EXCL 独占创建的逐事件日志，model_request/response 成对；中断/重试
   审计以此为准。
3. provider 调用数与 token 以提供方账单为最终核实途径（报告内数字为本地计数与推导）。
