// Package service 编排参数档案管理与计算查询，
// 是 HTTP 接口层与物理/存储层之间的唯一入口。
package service

import (
	"sync"

	"waveguide-service/internal/domain"
	"waveguide-service/internal/physics"
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

// Service 业务服务，无请求级可变状态，可安全并发使用。
type Service struct {
	store Store
	// resolved 计算热路径上按名解析过的档案，避免高并发点名计算反复争抢存储读锁。
	resolved sync.Map // name -> domain.Profile
}

// New 构建服务。
func New(s Store) *Service {
	return &Service{store: s}
}

// ProfileView 档案对外视图，附带主模 TE10 的截止频率方便工程师核对。
type ProfileView struct {
	Name                    string          `json:"name"`
	Geometry                domain.Geometry `json:"geometry"`
	DominantMode            string          `json:"dominant_mode"`
	DominantCutoffFrequency float64         `json:"dominant_cutoff_frequency"`
}

func toView(p domain.Profile) ProfileView {
	return ProfileView{
		Name:         p.Name,
		Geometry:     p.Geometry,
		DominantMode: "TE10",
		DominantCutoffFrequency: physics.CutoffFrequency(
			p.Geometry.BroadDimension, p.Geometry.NarrowDimension,
			p.Geometry.RelPermittivity, p.Geometry.RelPermeability, 1, 0),
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
	if err := s.store.Create(p); err != nil {
		return ProfileView{}, err
	}
	// 返回构造副本而非保存的对象，避免调用方拿到内部存储引用。
	return toView(p), nil
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
	return s.store.Delete(name)
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
	return calc, nil
}

// resolve 按名取出参与计算的档案：先查已解析表，未命中再回源存储并记下。
// 同名档案登记后不可覆盖，一个名字对应的截面参数不会被改写。
func (s *Service) resolve(name string) (domain.Profile, error) {
	if v, ok := s.resolved.Load(name); ok {
		return v.(domain.Profile), nil
	}
	p, err := s.store.Get(name)
	if err != nil {
		return domain.Profile{}, err
	}
	s.resolved.Store(name, p)
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
