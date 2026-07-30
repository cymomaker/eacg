// 本文件验证能力注册、冻结、排序和可见性。
package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/cymomaker/eacg/capability"
	"github.com/cymomaker/eacg/identity"
)

// emptyInput 表示无需业务字段的测试输入。
type emptyInput struct{}

// emptyOutput 表示无需业务字段的测试输出。
type emptyOutput struct{}

// newTestCapability 创建注册表测试使用的能力。
func newTestCapability(t *testing.T, name string, roles []string) capability.Capability {
	t.Helper()
	item, err := capability.New(capability.Descriptor{
		ID:            name + ".v1",
		Name:          name,
		Version:       "v1",
		Description:   "测试能力",
		RiskLevel:     capability.RiskR0,
		ReadOnly:      true,
		RequiredRoles: roles,
	}, func(_ context.Context, _ capability.RequestContext, _ emptyInput) (emptyOutput, error) {
		return emptyOutput{}, nil
	})
	if err != nil {
		t.Fatalf("创建测试能力失败：%v", err)
	}
	return item
}

// newIdentityCapability 创建带身份类型要求的测试能力。
func newIdentityCapability(
	t *testing.T,
	name string,
	requirement capability.IdentityRequirement,
) capability.Capability {
	t.Helper()
	item, err := capability.New(capability.Descriptor{
		ID:                  name + ".v1",
		Name:                name,
		Version:             "v1",
		Description:         "身份约束测试能力",
		RiskLevel:           capability.RiskR0,
		ReadOnly:            true,
		IdentityRequirement: requirement,
	}, func(_ context.Context, _ capability.RequestContext, _ emptyInput) (emptyOutput, error) {
		return emptyOutput{}, nil
	})
	if err != nil {
		t.Fatalf("创建身份约束能力失败：%v", err)
	}
	return item
}

// TestRegistryRegisterAndVisible 验证注册、排序和权限裁剪。
func TestRegistryRegisterAndVisible(t *testing.T) {
	t.Parallel()

	store := New()
	if err := store.Register(newTestCapability(t, "zeta", nil)); err != nil {
		t.Fatalf("注册能力失败：%v", err)
	}
	if err := store.Register(newTestCapability(t, "alpha", []string{"reader"})); err != nil {
		t.Fatalf("注册能力失败：%v", err)
	}

	user := identity.Principal{
		SubjectType: identity.SubjectUser,
		TenantID:    "tenant-a",
		UserID:      "user-1",
		Roles:       []string{"reader"},
	}
	visible := store.Visible(user)
	if len(visible) != 2 || visible[0].Descriptor().Name != "alpha" {
		t.Fatalf("可见能力不正确：%v", visible)
	}
	user.Roles = nil
	if len(store.Visible(user)) != 1 {
		t.Fatal("无角色身份只能看到公开能力")
	}
}

// TestRegistryFiltersByIdentityType 验证 Tool 列表按用户和服务身份裁剪。
func TestRegistryFiltersByIdentityType(t *testing.T) {
	t.Parallel()

	store := New()
	if err := store.Register(
		newIdentityCapability(t, "any_tool", capability.IdentityAny),
	); err != nil {
		t.Fatalf("注册通用能力失败：%v", err)
	}
	if err := store.Register(
		newIdentityCapability(t, "user_tool", capability.IdentityUser),
	); err != nil {
		t.Fatalf("注册用户能力失败：%v", err)
	}
	if err := store.Register(
		newIdentityCapability(t, "service_tool", capability.IdentityService),
	); err != nil {
		t.Fatalf("注册服务能力失败：%v", err)
	}

	user := identity.Principal{
		SubjectType: identity.SubjectUser,
		TenantID:    "tenant-a",
		UserID:      "user-1",
	}
	service := identity.Principal{
		SubjectType: identity.SubjectService,
		TenantID:    "tenant-a",
		ClientID:    "service-a",
	}
	userVisible := store.Visible(user)
	serviceVisible := store.Visible(service)
	if len(userVisible) != 2 ||
		userVisible[0].Descriptor().Name != "any_tool" ||
		userVisible[1].Descriptor().Name != "user_tool" {
		t.Fatalf("用户可见能力不正确：%v", userVisible)
	}
	if len(serviceVisible) != 2 ||
		serviceVisible[0].Descriptor().Name != "any_tool" ||
		serviceVisible[1].Descriptor().Name != "service_tool" {
		t.Fatalf("服务可见能力不正确：%v", serviceVisible)
	}
}

// TestRegistryRejectsDuplicateAndFrozen 验证重复注册和冻结保护。
func TestRegistryRejectsDuplicateAndFrozen(t *testing.T) {
	t.Parallel()

	store := New()
	item := newTestCapability(t, "echo", nil)
	if err := store.Register(item); err != nil {
		t.Fatalf("首次注册失败：%v", err)
	}
	if err := store.Register(item); err == nil {
		t.Fatal("重复注册不应成功")
	}
	store.Freeze()
	if err := store.Register(newTestCapability(t, "next", nil)); !errors.Is(err, ErrFrozen) {
		t.Fatalf("冻结后应返回 ErrFrozen，实际为：%v", err)
	}
}
