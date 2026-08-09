package hostruntime

import (
	"bytes"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

const requirementWithBrands = `{
  "schema_version":1,
  "budget_cny":8000,
  "use_case":{"type":"gaming","resolution":"2K"},
  "brand_pref":{"cpu":"intel","gpu":"nvidia"}
}`

func TestSanitizeInferredBrandPrefs(t *testing.T) {
	tests := []struct {
		name    string
		user    string
		wantCPU schemas.CPUBrand
		wantGPU schemas.GPUBrand
		changed bool
	}{
		{name: "风格不推断品牌", user: "8000 元 2K 游戏,要安静的显卡,白色海景房", wantCPU: schemas.CPUBrandAny, wantGPU: schemas.GPUBrandAny, changed: true},
		{name: "显式 N 卡保留 GPU", user: "CPU 随意,显卡要 N 卡", wantCPU: schemas.CPUBrandAny, wantGPU: schemas.GPUBrandNvidia, changed: true},
		{name: "显式英特尔只保留 CPU", user: "CPU 要英特尔,显卡安静即可", wantCPU: schemas.CPUBrandIntel, wantGPU: schemas.GPUBrandAny, changed: true},
		{name: "两个品牌均显式时不改", user: "Intel CPU 搭配 RTX 显卡", wantCPU: schemas.CPUBrandIntel, wantGPU: schemas.GPUBrandNvidia, changed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := sanitizeInferredBrandPrefs([]byte(requirementWithBrands), tt.user)
			if changed != tt.changed {
				t.Fatalf("changed=%v, want %v", changed, tt.changed)
			}
			spec, err := schemas.DecodeRequirementSpec(got)
			if err != nil {
				t.Fatalf("DecodeRequirementSpec: %v", err)
			}
			if spec.BrandPref.CPU != tt.wantCPU || spec.BrandPref.GPU != tt.wantGPU {
				t.Fatalf("brand_pref={cpu:%s gpu:%s}, want {cpu:%s gpu:%s}",
					spec.BrandPref.CPU, spec.BrandPref.GPU, tt.wantCPU, tt.wantGPU)
			}
		})
	}
}

func TestSanitizeInferredBrandPrefsLeavesChangeRequestUntouched(t *testing.T) {
	payload := []byte(`{"schema_version":1,"base_build_ref":"v1","intent":"swap_part","swap":{"category":"gpu","target_hint":"换 AMD 显卡"}}`)
	got, changed := sanitizeInferredBrandPrefs(payload, "换成 A 卡")
	if changed {
		t.Fatal("ChangeRequest 不应被修改")
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload 被修改:\n got: %s\nwant: %s", got, payload)
	}
}

func TestIsProductOwnerID(t *testing.T) {
	if !isProductOwnerID("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA") {
		t.Fatal("32-byte base64url owner 应识别为产品匿名身份")
	}
	for _, value := range []string{"", "user", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if isProductOwnerID(value) {
			t.Fatalf("%q 不应识别为产品匿名身份", value)
		}
	}
}
