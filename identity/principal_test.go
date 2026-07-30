// 本文件验证用户和服务 Principal 的有效性。
package identity

import "testing"

// TestPrincipalValidatesSubjectTypes 验证不同身份类型要求对应字段。
func TestPrincipalValidatesSubjectTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		principal Principal
		valid     bool
	}{
		{
			name: "有效用户",
			principal: Principal{
				SubjectType: SubjectUser,
				TenantID:    "tenant-a",
				UserID:      "user-1",
			},
			valid: true,
		},
		{
			name: "用户缺少 UserID",
			principal: Principal{
				SubjectType: SubjectUser,
				TenantID:    "tenant-a",
			},
		},
		{
			name: "有效服务",
			principal: Principal{
				SubjectType: SubjectService,
				TenantID:    "tenant-a",
				ClientID:    "service-a",
			},
			valid: true,
		},
		{
			name: "服务缺少 ClientID",
			principal: Principal{
				SubjectType: SubjectService,
				TenantID:    "tenant-a",
			},
		},
		{
			name: "缺少身份类型",
			principal: Principal{
				TenantID: "tenant-a",
				UserID:   "user-1",
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			if test.principal.Valid() != test.valid {
				t.Fatalf("Principal 有效性不正确：%+v", test.principal)
			}
		})
	}
}
