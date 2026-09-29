// Package service 编排参数档案管理与计算查询，
// 是 HTTP 接口层与物理/存储层之间的唯一入口。
package service

import (
	"errors"
	"sync"

	"waveguide-service/internal/domain"
	"waveguide-service/internal/physics"
	"waveguide-service/internal/store"
	"waveguide-service/internal/validate"
)

// Store 档案存取接口，由 store.MemoryStore 实现。
// 面向接口编程，便于用替身实现做单测。
type Store interface {
	Create(p domain.Profile) error
	Get(name string) (domain.Profile, error)
	Delete(name string) error
	List() []domain.Profile
}

// cachedProfile 是计算热路径复用的不可变快照。
// exists=false 表示已经在同名协调锁内确认过的“未找到/已删除”标记。
type cachedProfile struct {
	profile domain.Profile
	exists  bool
}

// Service 业务服务，可安全并发使用。
type Service struct {
	store Store

	// resolved 保存按名复用的档案快照或删除标记。值整体不可变；
	// 热路径只做 sync.Map.Load，不进入存储锁，也不进入同名协调锁。
	resolved sync.Map // name -> cachedProfile

	// gates 按名串行化缓存冷装载与登记/删除，防止“删除夹在读取和回填之间”。
	// 不同名字使用不同锁；已经命中缓存的高频计算不会访问这里。
	gatesMu sync.RWMutex
	gates   map[string]*sync.Mutex
}

// New 构建服务。
func New(s Store) *Service {
	svc := &Service{
		store: s,
		gates: make(map[string]*sync.Mutex),
	}
	// 预置档案在启动时即可进入只读快照，后续点名不需要争抢存储读锁。
	for _, p := range s.List() {
		svc.resolved.Store(p.Name, cachedProfile{profile: p, exists: true})
	}
	return svc
}

// gateFor 返回某个档案名自己的协调锁，不把不同名字的写操作串到一起。
func (s *Service) gateFor(name string) *sync.Mutex {
	s.gatesMu.RLock()
	g, ok := s.gates[name]
	s.gatesMu.RUnlock()
	if ok {
		return g
	}

	s.gatesMu.Lock()
	defer s.gatesMu.Unlock()
	if g, ok = s.gates[name]; ok {
		return g
	}
	g = &sync.Mutex{}
	s.gates[name] = g
	return g
}

// ProfileView 档案对外视图，附带主模 TE10 的截止频率方便工程师核对。
type ProfileView struct {
	Name                    string          `json:"name"`
	Geometry                domain.Geometry `json:"geometry"`
	DominantMode            string          `json:"dominant_mode"`
	DominantCutoffFrequency float64         `json:"dominant_cutoff_frequency"`
	RegistrationRevision    int64           `json:"registration_revision"`
}

func toView(p domain.Profile) ProfileView {
	return ProfileView{
		Name:         p.Name,
		Geometry:     p.Geometry,
		DominantMode: "TE10",
		DominantCutoffFrequency: physics.CutoffFrequency(
			p.Geometry.BroadDimension, p.Geometry.NarrowDimension,
			p.Geometry.RelPermittivity, p.Geometry.RelPermeability, 1, 0),
		RegistrationRevision: p.Revision,
	}
}

// RegisterProfile 登记新档案：先做输入校验，再交由存储层做重名冲突检查。
func (s *Service) RegisterProfile(p domain.Profile) (ProfileView, error) {
	if err := validate.ProfileName(p.Name); err != nil {
		return ProfileView{}, err
	}
	if err := validate.Geometry(p.Geometry); err != nil {
		return ProfileView{}, err
	}

	// 与同名删除/冷装载互斥；冲突时不能替换缓存，成功时把完整新快照原子发布。
	gate := s.gateFor(p.Name)
	gate.Lock()
	defer gate.Unlock()

	if err := s.store.Create(p); err != nil {
		return ProfileView{}, err
	}
	created, err := s.store.Get(p.Name)
	if err != nil {
		return ProfileView{}, err
	}
	s.resolved.Store(created.Name, cachedProfile{profile: created, exists: true})

	// 返回构造副本而非保存的对象，避免调用方拿到内部存储引用。
	return toView(created), nil
}

// ListProfiles 列出全部已登记档案（含内置样例）。
func (s *Service) ListProfiles() []ProfileView {
	profiles := s.store.List()
	views := make([]ProfileView, len(profiles))
	for i, p := range profiles {
		views[i] = toView(p)
	}
	return views
}

// GetProfile 点名查看单个档案。
func (s *Service) GetProfile(name string) (ProfileView, error) {
	p, err := s.store.Get(name)
	if err != nil {
		return ProfileView{}, err
	}
	return toView(p), nil
}

// DeleteProfile 删除档案；不存在时透传 ErrProfileNotFound。
func (s *Service) DeleteProfile(name string) error {
	// 与同名登记/冷装载互斥。存储删除成功返回之前，删除标记必须已经发布，
	// 这样删除响应后开始的计算不可能再回填或读到旧快照。
	gate := s.gateFor(name)
	gate.Lock()
	defer gate.Unlock()

	if err := s.store.Delete(name); err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			s.resolved.Store(name, cachedProfile{})
		}
		return err
	}
	s.resolved.Store(name, cachedProfile{})
	return nil
}

// CalculateWithProfile 路径一：点名已登记档案，配上模式指数与一个或多个频率求值。
func (s *Service) CalculateWithProfile(name string, mode domain.Mode, freqs []float64) (*Calculation, error) {
	if err := validate.Mode(mode); err != nil {
		return nil, err
	}
	p, err := s.resolve(name)
	if err != nil {
		return nil, err
	}
	calc := dispatch(p.Geometry, mode, freqs)
	calc.ProfileName = &p.Name
	calc.RegistrationRevision = p.Revision
	return calc, nil
}

// resolve 按名取出参与计算的档案。
//
// 命中缓存时无锁、不访问存储，因此高频频率扫描不会和登记/删除争用存储锁。
// 未命中时只在“同一个名字”的协调锁内回源，并在同一临界区回填完整快照或删除标记。
// 写操作也持有同名锁，所以不会出现先拿到旧几何、删除后再把旧值回填的竞态。
func (s *Service) resolve(name string) (domain.Profile, error) {
	if v, ok := s.resolved.Load(name); ok {
		entry := v.(cachedProfile)
		if !entry.exists {
			return domain.Profile{}, store.ErrProfileNotFound
		}
		return entry.profile, nil
	}

	gate := s.gateFor(name)
	gate.Lock()
	defer gate.Unlock()

	if v, ok := s.resolved.Load(name); ok {
		entry := v.(cachedProfile)
		if !entry.exists {
			return domain.Profile{}, store.ErrProfileNotFound
		}
		return entry.profile, nil
	}

	p, err := s.store.Get(name)
	if errors.Is(err, store.ErrProfileNotFound) {
		s.resolved.Store(name, cachedProfile{})
	}
	if err != nil {
		return domain.Profile{}, err
	}
	entry := cachedProfile{profile: p, exists: true}
	s.resolved.Store(name, entry)
	return p, nil
}

// CalculateAdHoc 路径二：不登记档案，直接提交截面尺寸与频率现算一次。
// 与路径一调用同一个 dispatch，保证核算逻辑完全一致。
func (s *Service) CalculateAdHoc(g domain.Geometry, mode domain.Mode, freqs []float64) (*Calculation, error) {
	if err := validate.Geometry(g); err != nil {
		return nil, err
	}
	if err := validate.Mode(mode); err != nil {
		return nil, err
	}
	return dispatch(g, mode, freqs), nil
}
