package actor

import "sync"

// Registry 是 Actor 注册表，提供 Actor 的查找和注册功能。
// 它维护两个索引：按 ID 查找和按名称查找，支持并发安全访问。
// Registry 是 System 的核心组件，用于本地 Actor 的发现和路由。
type Registry struct {
	// mu 保护并发访问的读写锁
	mu sync.RWMutex
	// byID 按 Actor ID 索引的 Actor 映射
	byID map[string]IActor
	// byName 按名称到 ID 的映射，支持通过名称查找 Actor
	byName map[string]string
}

// NewRegistry 创建一个新的空注册表。
func NewRegistry() *Registry {
	return &Registry{
		byID:   make(map[string]IActor),
		byName: make(map[string]string),
	}
}

// Register 将 Actor 注册到注册表中。
// 如果 name 非空，则同时建立名称到 ID 的映射。
// 参数 id 为 Actor 的唯一标识符，name 为可选的人类可读名称，a 为要注册的 Actor 实例。
func (r *Registry) Register(id, name string, a IActor) {
	r.mu.Lock()
	r.byID[id] = a
	if name != "" {
		r.byName[name] = id
	}
	r.mu.Unlock()
}

// Unregister 从注册表中移除 Actor。
// 同时移除 ID 索引和名称索引（如果存在）。
func (r *Registry) Unregister(id, name string) {
	r.mu.Lock()
	delete(r.byID, id)
	if name != "" {
		delete(r.byName, name)
	}
	r.mu.Unlock()
}

// Get 通过 ID 查找 Actor。
// 返回 Actor 实例和一个布尔值表示是否找到。
func (r *Registry) Get(id string) (IActor, bool) {
	r.mu.RLock()
	a, ok := r.byID[id]
	r.mu.RUnlock()
	return a, ok
}

// GetByName 通过名称查找 Actor。
// 首先通过名称找到 ID，再通过 ID 找到 Actor 实例。
// 返回 Actor 实例和一个布尔值表示是否找到。
func (r *Registry) GetByName(name string) (IActor, bool) {
	r.mu.RLock()
	id, ok := r.byName[name]
	if !ok {
		r.mu.RUnlock()
		return nil, false
	}
	a := r.byID[id]
	r.mu.RUnlock()
	return a, a != nil
}

// Snapshot 返回当前所有已注册 Actor 的快照。
// 返回一个新的映射，包含所有 ID 到 Actor 的映射关系。
// 此方法可用于遍历所有 Actor 而不阻塞注册操作。
func (r *Registry) Snapshot() map[string]IActor {
	r.mu.RLock()
	out := make(map[string]IActor, len(r.byID))
	for k, v := range r.byID {
		out[k] = v
	}
	r.mu.RUnlock()
	return out
}
