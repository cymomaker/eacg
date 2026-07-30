// Package registry 提供线程安全的能力注册表。
package registry

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/cymomaker/eacg/capability"
	"github.com/cymomaker/eacg/identity"
)

// ErrFrozen 表示注册表已经冻结。
var ErrFrozen = errors.New("能力注册表已经冻结")

// Registry 保存应用中已注册的能力。
type Registry struct {
	mu      sync.RWMutex
	frozen  bool
	byID    map[string]capability.Capability
	byName  map[string]capability.Capability
	ordered []capability.Capability
}

// New 创建空的能力注册表。
func New() *Registry {
	return &Registry{
		byID:   make(map[string]capability.Capability),
		byName: make(map[string]capability.Capability),
	}
}

// Register 注册一个能力并检查 ID 和名称冲突。
func (r *Registry) Register(item capability.Capability) error {
	if item == nil {
		return fmt.Errorf("能力不能为空")
	}
	descriptor := item.Descriptor()
	if err := descriptor.Validate(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrFrozen
	}
	if _, exists := r.byID[descriptor.ID]; exists {
		return fmt.Errorf("能力 ID %q 已存在", descriptor.ID)
	}
	if _, exists := r.byName[descriptor.Name]; exists {
		return fmt.Errorf("能力名称 %q 已存在", descriptor.Name)
	}
	r.byID[descriptor.ID] = item
	r.byName[descriptor.Name] = item
	r.ordered = append(r.ordered, item)
	sort.SliceStable(r.ordered, func(i, j int) bool {
		return r.ordered[i].Descriptor().Name < r.ordered[j].Descriptor().Name
	})
	return nil
}

// Freeze 冻结注册表，避免服务运行时修改能力。
func (r *Registry) Freeze() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
}

// GetByName 根据 Tool 名称查找能力。
func (r *Registry) GetByName(name string) (capability.Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, exists := r.byName[name]
	return item, exists
}

// List 返回所有能力的有序副本。
func (r *Registry) List() []capability.Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]capability.Capability(nil), r.ordered...)
}

// Visible 返回当前身份有权查看的能力。
func (r *Registry) Visible(principal identity.Principal) []capability.Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]capability.Capability, 0, len(r.ordered))
	for _, item := range r.ordered {
		descriptor := item.Descriptor()
		if descriptor.AllowsPrincipal(principal) &&
			principal.HasAllRoles(descriptor.RequiredRoles) {
			result = append(result, item)
		}
	}
	return result
}
