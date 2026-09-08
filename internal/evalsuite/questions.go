package evalsuite

import "strings"

// repeatedQuestion 是显式字段契约下的中文近似判卷。保留触发短句供人工复核，
// 不试图判断任意自然语言；与旧 S3 分开，避免追溯改变历史题口径。
func repeatedQuestion(text, field string) string {
	terms := map[string][]string{
		"budget_cny":   {"预算", "多少钱", "多少元", "多少块", "花多少"},
		"budget_basis": {"预算口径", "新增", "新购", "购买", "采购", "整机", "整台电脑", "整台主机", "已有件的价值", "已有配件的价值", "包含已有", "包括已有"},
		"resolution":   {"分辨率", "1080p", "1440p", "2160p", "2k", "4k"},
		"owned_parts":  {"型号", "哪款", "哪一款", "具体配件", "数量", "几颗", "几件", "几条"},
		"use_case":     {"用途", "主要用来", "主要做", "纯游戏", "兼顾办公"},
	}
	for _, sentence := range strings.FieldsFunc(strings.ToLower(expandQuestionList(text)), func(r rune) bool {
		return strings.ContainsRune("。！!；;\n", r)
	}) {
		previous := ""
		for _, clause := range strings.FieldsFunc(sentence, func(r rune) bool { return r == '，' || r == ',' }) {
			clause = strings.TrimSpace(clause)
			// “预算是新增费用，对吗？”的尾问仅关联紧邻分句，不跨段吸附主题。
			if containsAny(clause, []string{"对吗", "是吗", "可以吗", "没错吧", "对吧"}) && len([]rune(clause)) <= 12 {
				clause = previous + "，" + clause
			}
			previous = clause
			if containsAny(clause, []string{"不用", "不必", "无需", "不需要", "无法", "不能", "不再询问", "不再确认", "无需重复"}) {
				continue
			}
			if !containsAny(clause, terms[field]) {
				continue
			}
			// 用完整句识别口径选择，避免“除了已有CPU，新增购买…”被逗号拆散。
			if field == "budget_cny" && basisQuestionTopic(sentence) && !amountQuestion(clause) {
				continue
			}
			// “新增购买预算是多少”是在问金额；“是否需要购买其他件”也不等于问口径。
			if field == "budget_basis" && (!basisQuestionTopic(clause) || (amountQuestion(clause) && !containsAny(clause, []string{"还是", "口径", "对吗", "是吗"}))) {
				continue
			}
			request := strings.NewReplacer("请分别", "请", "请逐一", "请").Replace(clause)
			if containsAny(request, []string{"？", "?", "吗", "呢", "还是", "是否", "能否", "可否", "请提供", "请告诉", "请补充", "请确认", "请说明", "请给出", "请问", "需要确认", "还需确认", "确认一下", "告知"}) {
				return clause
			}
		}
	}
	return ""
}

func amountQuestion(text string) bool {
	if containsAny(text, []string{"多少钱", "多少元", "多少块", "预算多少", "预算是多少", "预算具体多少", "金额是多少", "花多少"}) {
		return true
	}
	return !containsAny(text, []string{"口径", "还是", "是否", "对吗", "是吗"}) && strings.Contains(text, "预算") &&
		containsAny(text, []string{"请提供", "请告诉", "请补充", "请给出"})
}

func basisQuestionTopic(text string) bool {
	return containsAny(text, []string{"预算口径", "新增", "新购", "整机", "整台电脑", "整台主机", "包含已有", "包括已有"}) ||
		(strings.Contains(text, "预算") && containsAny(text, []string{"购买", "采购", "已有件的价值", "已有配件的价值"}))
}
