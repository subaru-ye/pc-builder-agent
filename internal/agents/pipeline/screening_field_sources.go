package pipeline

import (
	"regexp"
	"strings"
)

var budgetUnspecified = regexp.MustCompile(`(?:预算|金额)[^。！？!?；;\n]{0,10}(?:还没定|还没确定|未确定|未定|待定|不确定|没告诉|尚未确定)|(?:取消|撤回)[^。！？!?；;\n]{0,18}预算`)
var resolutionUnspecified = regexp.MustCompile(`分辨率[^。！？!?；;\n]{0,8}(?:没定|没说|未定|不确定|未提供|等下|稍后|还没)`)
var resolutionClaim = regexp.MustCompile(`(?i)(?:分辨率(?:是|为|改为|改成|用|[:：=\s])*|显示器(?:是|用)?|用|^\s*)(1080p|1920[×x*]1080|2k|1440p|2560[×x*]1440|4k|2160p|3840[×x*]2160)|(?:1080p|2k|4k)(?:分辨率|显示器|游戏|玩|屏幕)`)

// 只核对明确的分辨率表达；助手给出的选项不会进入 sources。
// 识别仍有表达边界，不把任意数字或“预算2k”当显示分辨率。
func groundedResolution(resolution string, sources []string) bool {
	known := ""
	for _, source := range sources {
		source = strings.Join(strings.Fields(source), "")
		if resolutionUnspecified.MatchString(source) {
			known = ""
			continue
		}
		matches := resolutionClaim.FindAllString(source, -1)
		for _, match := range matches {
			lower := strings.ToLower(match)
			switch {
			case strings.Contains(lower, "1080"):
				known = "1080p"
			case strings.Contains(lower, "1440") || strings.Contains(lower, "2k"):
				known = "2K"
			case strings.Contains(lower, "2160") || strings.Contains(lower, "4k"):
				known = "4K"
			}
		}
	}
	return known == resolution
}
