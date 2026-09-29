// Package store 进程内并发安全的参数档案存储。
// 多个客户端并发读写时通过读写锁保证一致性，无需外部数据库。
package store

import (
	"errors"
	"sort"
	"sync"

	"waveguide-service/internal/domain"
)

var (
	// ErrProfileExists 登记同名档案时返回，已有记录不会被覆盖。
	ErrProfileExists = errors.New("profile already exists")
	// ErrProfileNotFound 查询或删除不存在的档案时返回。
	ErrProfileNotFound = errors.New("profile not found")
)

// MemoryStore 基于读写锁 + map 的内存实现。
type MemoryStore struct {
	mu       sync.RWMutex
	profiles map[string]domain.Profile
}

// NewMemoryStore 创建存储并预置内置样例档案。
func NewMemoryStore() *MemoryStore {
	s := &MemoryStore{profiles: make(map[string]domain.Profile)}
	for _, p := range builtinProfiles() {
		s.profiles[p.Name] = p
	}
	return s
}

// Create 登记新档案；同名已存在时返回 ErrProfileExists，不覆盖原记录。
func (s *MemoryStore) Create(p domain.Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.profiles[p.Name]; ok {
		return ErrProfileExists
	}
	s.profiles[p.Name] = p
	return nil
}

// Get 按名取出档案；不存在时返回 ErrProfileNotFound。
func (s *MemoryStore) Get(name string) (domain.Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.profiles[name]
	if !ok {
		return domain.Profile{}, ErrProfileNotFound
	}
	return p, nil
}

// Delete 删除档案；不存在时返回 ErrProfileNotFound，不会悄悄当作成功。
func (s *MemoryStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.profiles[name]; !ok {
		return ErrProfileNotFound
	}
	delete(s.profiles, name)
	return nil
}

// List 返回全部档案的副本，按名称排序，保证输出稳定。
func (s *MemoryStore) List() []domain.Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Profile, 0, len(s.profiles))
	for _, p := range s.profiles {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
