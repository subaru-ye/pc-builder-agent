package schemas

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func validPreferenceMemory() PreferenceMemory {
	return PreferenceMemory{
		OwnerID:  "owner-a",
		Subject:  PreferenceSubjectSelf,
		Field:    "brand_pref.gpu",
		Value:    json.RawMessage(`"nvidia"`),
		Strength: PreferenceStrengthPrefer,
		Evidence: PreferenceEvidenceStated,
		Source:   PreferenceSource{Kind: "chat", SessionID: "sess-1", MessageID: "msg-1", Quote: "显卡偏好N卡"},
	}
}

func TestValidatePreferenceMemory(t *testing.T) {
	t.Run("合法记录通过", func(t *testing.T) {
		if err := ValidatePreferenceMemory(validPreferenceMemory()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("代配对象与 must 强度通过", func(t *testing.T) {
		m := validPreferenceMemory()
		m.Subject, m.Strength, m.Evidence = "friend:xiaowang", PreferenceStrengthMust, PreferenceEvidenceAccept
		if err := ValidatePreferenceMemory(m); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("free 字段与易失事实通过", func(t *testing.T) {
		m := validPreferenceMemory()
		m.Field, m.Volatile, m.ObservedAt = "free.quiet_case", true, "2026-09-01"
		if err := ValidatePreferenceMemory(m); err != nil {
			t.Fatal(err)
		}
	})
	cases := []struct {
		name   string
		mutate func(*PreferenceMemory)
	}{
		{"owner 缺失", func(m *PreferenceMemory) { m.OwnerID = "" }},
		{"subject 缺失", func(m *PreferenceMemory) { m.Subject = "" }},
		{"subject 超长", func(m *PreferenceMemory) { m.Subject = string(make([]byte, 41)) }},
		{"field 不在词表", func(m *PreferenceMemory) { m.Field = "brand_pref.motherboard" }},
		{"field 超长", func(m *PreferenceMemory) { m.Field = "free." + string(make([]byte, 60)) }},
		{"value 非法 JSON", func(m *PreferenceMemory) { m.Value = json.RawMessage(`"broken`) }},
		{"strength 非法", func(m *PreferenceMemory) { m.Strength = "optional" }},
		{"evidence uncertain 拒绝", func(m *PreferenceMemory) { m.Evidence = "uncertain" }},
		{"evidence inferred 暂不允许", func(m *PreferenceMemory) { m.Evidence = "inferred" }},
		{"携带服务端 id", func(m *PreferenceMemory) { m.ID = "uuid" }},
		{"携带墓碑状态", func(m *PreferenceMemory) { m.Status = "retracted" }},
		{"携带 supersedes", func(m *PreferenceMemory) { m.Supersedes = "uuid" }},
		{"携带时间戳", func(m *PreferenceMemory) { m.CreatedAt = time.Now() }},
		{"source.kind 非法", func(m *PreferenceMemory) { m.Source.Kind = "model" }},
		{"session 缺失", func(m *PreferenceMemory) { m.Source.SessionID = "" }},
		{"quote 缺失", func(m *PreferenceMemory) { m.Source.Quote = "" }},
		{"易失缺 observed_at", func(m *PreferenceMemory) { m.Volatile = true }},
		{"observed_at 格式非法", func(m *PreferenceMemory) { m.ObservedAt = "2026/09/01" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validPreferenceMemory()
			tc.mutate(&m)
			if err := ValidatePreferenceMemory(m); !errors.Is(err, ErrPreferenceMemoryInvalid) {
				t.Fatalf("期望 ErrPreferenceMemoryInvalid,得到 %v", err)
			}
		})
	}
}

func TestValidPreferenceField(t *testing.T) {
	for field, want := range map[string]bool{
		"brand_pref.gpu": true, "noise_pref": true, "budget_cny": true,
		"free.quiet_case": true, "unknown_field": false, "": false,
		"free.": false,
	} {
		if got := ValidPreferenceField(field); got != want {
			t.Fatalf("ValidPreferenceField(%q) = %v,期望 %v", field, got, want)
		}
	}
}

func TestPreferenceStale(t *testing.T) {
	today := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	stable := PreferenceMemory{Volatile: false, ObservedAt: "2025-01-01"}
	if PreferenceStale(stable, today, week) {
		t.Fatal("非易失偏好不过期")
	}
	fresh := PreferenceMemory{Volatile: true, ObservedAt: "2026-09-25"}
	if PreferenceStale(fresh, today, week) {
		t.Fatal("窗口内易失事实不算过期")
	}
	old := PreferenceMemory{Volatile: true, ObservedAt: "2026-09-01"}
	if !PreferenceStale(old, today, week) {
		t.Fatal("超出窗口的易失事实必须过期")
	}
	broken := PreferenceMemory{Volatile: true, ObservedAt: "09-01-2026"}
	if !PreferenceStale(broken, today, week) {
		t.Fatal("观察日期无法解析按过期处理")
	}
}

func TestFilterPreferenceSuggestions(t *testing.T) {
	today := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	records := []PreferenceMemory{
		{Field: "brand_pref.gpu", Status: PreferenceStatusActive},
		{Field: "noise_pref", Status: PreferenceStatusRetract},
		{Field: "brand_pref.cpu", Status: PreferenceStatusSupersede},
		{Field: "free.quiet_case", Status: PreferenceStatusActive, Volatile: true, ObservedAt: "2026-01-01"},
		{Field: "appearance", Status: PreferenceStatusActive, Volatile: true, ObservedAt: "2026-09-26"},
	}
	got := FilterPreferenceSuggestions(records, today, week)
	if len(got) != 2 || got[0].Field != "brand_pref.gpu" || got[1].Field != "appearance" {
		t.Fatalf("召回应只保留 active 且未过期的建议,得到 %+v", got)
	}
	if empty := FilterPreferenceSuggestions(nil, today, week); len(empty) != 0 {
		t.Fatal("空输入应返回空切片")
	}
}

func TestEqualPreferenceValue(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{`"nvidia"`, `"nvidia"`, true},
		{`{ "b":1, "a":2 }`, `{"a":2,"b":1}`, true},
		{`8000`, `8000.0`, true},
		{`"nvidia"`, `"amd"`, false},
		{`8000`, `9000`, false},
	}
	for _, tc := range cases {
		if got := EqualPreferenceValue(json.RawMessage(tc.a), json.RawMessage(tc.b)); got != tc.want {
			t.Fatalf("EqualPreferenceValue(%s, %s) = %v,期望 %v", tc.a, tc.b, got, tc.want)
		}
	}
	if EqualPreferenceValue(nil, nil) != true || EqualPreferenceValue(nil, json.RawMessage(`1`)) != false {
		t.Fatal("空值比较语义错误")
	}
}

func TestDecodePreferenceSourceStrict(t *testing.T) {
	if _, err := DecodePreferenceSource([]byte(`{"kind":"chat","session_id":"s","message_id":"m","quote":"q"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePreferenceSource([]byte(`{"kind":"chat","extra":1}`)); err == nil {
		t.Fatal("未知字段必须拒绝")
	}
}
