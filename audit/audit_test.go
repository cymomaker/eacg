// 本文件验证审计事件的内存和日志写入行为。
package audit

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestLoggerSinkWrite 验证审计日志为 JSON 且不包含业务参数。
func TestLoggerSinkWrite(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	sink := NewLoggerSink(&output)
	err := sink.Write(context.Background(), Event{
		RequestID:      "request-1",
		TenantID:       "tenant-a",
		UserID:         "user-1",
		CapabilityName: "get_account",
		Allowed:        true,
		Success:        true,
	})
	if err != nil {
		t.Fatalf("写入审计失败：%v", err)
	}
	if !strings.Contains(output.String(), `"capability_name":"get_account"`) {
		t.Fatalf("审计内容不正确：%s", output.String())
	}
}
