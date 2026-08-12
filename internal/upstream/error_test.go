package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		status int
		code   string
		err    error
		want   Kind
	}{
		{status: 403, code: "AllocationQuota.FreeTierOnly", want: KindQuota},
		{status: 401, want: KindAuthentication},
		{status: 429, want: KindRateLimit},
		{status: 503, want: KindUnavailable},
		{status: 400, want: KindInvalidRequest},
		{err: context.DeadlineExceeded, want: KindTimeout},
		{err: &json.SyntaxError{}, want: KindProtocol},
	}
	for _, tc := range tests {
		if got := Classify(tc.status, tc.code, tc.err); got != tc.want {
			t.Errorf("Classify(%d,%q,%v)=%s want %s", tc.status, tc.code, tc.err, got, tc.want)
		}
	}
}

func TestErrorTextDoesNotExposeCauseOrUnsafeCode(t *testing.T) {
	err := New("bailian", "screening", KindAuthentication, 403, "bad code: secret",
		errors.New("provider response contained a secret"))
	if got := err.Error(); got != "model upstream: provider=bailian role=screening kind=authentication status=403 code=redacted" {
		t.Fatalf("Error()=%q", got)
	}
	if !errors.Is(fmt.Errorf("wrapped: %w", err), err) {
		t.Fatal("错误应支持 errors.Is")
	}
}
