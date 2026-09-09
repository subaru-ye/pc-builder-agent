package pipeline

// NewBuildPromptComponents 返回明确尚无配置版本时实际发送的编译提示词原文。
// cmd/eval 的单轮及多轮初筛均显式使用这个状态，不将未使用的 legacy 指令混入身份。
func NewBuildPromptComponents() map[string]string {
	return map[string]string{"new_build_system": newBuildOnlyInstruction, "format_retry": screeningFormatRetryInstruction}
}

const screeningFormatRetryInstruction = "上一条输出未通过需求草稿结构检查。请重新依据原用户消息输出一个完整闭合、符合既定字段和枚举的需求 JSON：schema_version 为数字 1，不含 intent 或改单字段；priority 只能包含硬件品类，notes 必须是字符串。缺少的需求信息省略对应字段，不猜填、不改变已知业务事实。"
