package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/cymomaker/eacg/capability"
	"github.com/cymomaker/eacg/identity"
)

type emptyInput struct{}
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

	visible := store.Visible(identity.Principal{Roles: []string{"reader"}})
	if len(visible) != 2 || visible[0].Descriptor().Name != "alpha" {
		t.Fatalf("可见能力不正确：%v", visible)
	}
	if len(store.Visible(identity.Principal{})) != 1 {
		t.Fatal("无角色身份只能看到公开能力")
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
