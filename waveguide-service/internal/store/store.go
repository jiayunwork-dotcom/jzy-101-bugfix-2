// Package store 进程内并发安全的参数档案存储。
// 档案记录不可变、按名原子换装：读路径（Get/List）不取任何锁，
// 高并发点名计算不会与登记、删除争抢同一把锁，无需外部数据库。
package store

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"

	"waveguide-service/internal/domain"
)

var (
	// ErrProfileExists 登记同名档案时返回，已有记录不会被覆盖。
	ErrProfileExists = errors.New("profile already exists")
	// ErrProfileNotFound 查询或删除不存在的档案时返回。
	ErrProfileNotFound = errors.New("profile not found")
)

// MemoryStore 基于 sync.Map 的内存实现。
//
// 每个名字对应一条不可变的 *domain.Record：
//   - Get 是一次原子加载，不取锁，也不被其它名字的登记/删除拖慢；
//   - Register / Delete 分别由 LoadOrStore / LoadAndDelete 原子完成，
//     同名并发写自然线性化，无需互斥锁。
//
// 删除一旦返回，之后的 Get 必然未找到；重登一旦返回，之后的 Get
// 必然拿到新记录——不存在"旧记录被换掉后仍被读到"的窗口。
type MemoryStore struct {
	seq atomic.Uint64
	m   sync.Map // name -> *domain.Record（记录不可变）
}

// NewMemoryStore 创建存储并预置内置样例档案。
func NewMemoryStore() *MemoryStore {
	s := &MemoryStore{}
	for _, p := range builtinProfiles() {
		if _, err := s.Register(p); err != nil {
			panic("builtin profile registration failed: " + err.Error())
		}
	}
	return s
}

// Register 登记新档案，返回带登记序号的记录；同名已存在时返回
// ErrProfileExists，已有记录不会被覆盖。
// 登记序号全局单调递增（冲突的登记尝试可能消耗序号，不要求连续），
// 同名删除重登后得到严格更大的序号。
func (s *MemoryStore) Register(p domain.Profile) (domain.Record, error) {
	rec := &domain.Record{Profile: p, Generation: s.seq.Add(1)}
	if _, loaded := s.m.LoadOrStore(p.Name, rec); loaded {
		return domain.Record{}, ErrProfileExists
	}
	return *rec, nil
}

// Create 是 Register 的兼容包装：只登记，不返回记录。
// 保留给不需要登记序号的旧调用方；新代码请用 Register。
func (s *MemoryStore) Create(p domain.Profile) error {
	_, err := s.Register(p)
	return err
}

// Get 按名取出当前档案记录；不存在时返回 ErrProfileNotFound。
// 返回记录副本；记录本身不可变，并发的删除/重登不会影响已取出的记录。
func (s *MemoryStore) Get(name string) (domain.Record, error) {
	v, ok := s.m.Load(name)
	if !ok {
		return domain.Record{}, ErrProfileNotFound
	}
	return *v.(*domain.Record), nil
}

// Delete 删除档案；不存在时返回 ErrProfileNotFound，不会悄悄当作成功。
func (s *MemoryStore) Delete(name string) error {
	if _, loaded := s.m.LoadAndDelete(name); !loaded {
		return ErrProfileNotFound
	}
	return nil
}

// List 返回全部档案记录的副本，按名称排序，保证输出稳定。
func (s *MemoryStore) List() []domain.Record {
	out := make([]domain.Record, 0, 16)
	s.m.Range(func(_, v any) bool {
		out = append(out, *v.(*domain.Record))
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
