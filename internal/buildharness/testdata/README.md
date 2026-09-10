# 需求约束故障回放

`video_editing_failure_requirement.json` 根据本次任务中已只读核对的 `run_evidence.build_input` 字段保存，来源为会话 `bb36d158-67c6-4a95-b1e5-4a06ed229dcc` 的 build run `0d8aab2d-10a3-4b3a-a852-722e7de38d31` / `e97c2c5b-7daa-4057-9aad-dffd346119ff`（2026-09-10 14:41，Asia/Shanghai）。这是需求字段回放，不是数据库原始响应的逐字备份。

用户原话为“预算 6000，主要剪 4K 视频，尽量安静”，之后仅在面板将预算从 must 改为 prefer。原失败由未区分用途说明的 must notes 触发；预算弹性 0.1 是投影默认值，未被用户明确表达。fixture 故意不补 `requirement_semantics`，验证旧状态兼容路径，不修改历史证据来掩盖失败。

对应 Harness 测试使用固定的离线 Builder 输出及校验替身，验证故障入口至配置成功结果和完整需求传递；真实规则、持久化与浏览器链路另由产品集成回放覆盖。测试无网络、无数据库和真实模型调用。
