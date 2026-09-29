// Package service 编排参数档案管理与计算查询，
// 是 HTTP 接口层与物理/存储层之间的唯一入口。
package service

import (
	"waveguide-service/internal/domain"
	"waveguide-service/internal/physics"
	"waveguide-service/internal/validate"
)

// Store 档案存取接口，由 store.MemoryStore 实现。
// 面向接口编程，便于用替身实现做单测。
type Store interface {
	Register(p domain.Profile) (domain.Record, error)
	Get(name string) (domain.Record, error)
	Delete(name string) error
	List() []domain.Record
}

// Service 业务服务，无请求级可变状态，可安全并发使用。
type Service struct {
	store Store
}

// New 构建服务。
func New(s Store) *Service {
	return &Service{store: s}
}

// ProfileView 档案对外视图：附带主模 TE10 的截止频率方便工程师核对，
// 以及本次登记的序号（与点名计算响应中的 profile_generation 一致，便于对账）。
type ProfileView struct {
	Name                    string          `json:"name"`
	Geometry                domain.Geometry `json:"geometry"`
	DominantMode            string          `json:"dominant_mode"`
	DominantCutoffFrequency float64         `json:"dominant_cutoff_frequency"`
	Generation              uint64          `json:"generation"`
}

func toView(rec domain.Record) ProfileView {
	return ProfileView{
		Name:         rec.Name,
		Geometry:     rec.Geometry,
		DominantMode: "TE10",
		DominantCutoffFrequency: physics.CutoffFrequency(
			rec.Geometry.BroadDimension, rec.Geometry.NarrowDimension,
			rec.Geometry.RelPermittivity, rec.Geometry.RelPermeability, 1, 0),
		Generation: rec.Generation,
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
	rec, err := s.store.Register(p)
	if err != nil {
		return ProfileView{}, err
	}
	return toView(rec), nil
}

// ListProfiles 列出全部已登记档案（含内置样例）。
func (s *Service) ListProfiles() []ProfileView {
	records := s.store.List()
	views := make([]ProfileView, len(records))
	for i, rec := range records {
		views[i] = toView(rec)
	}
	return views
}

// GetProfile 点名查看单个档案。
func (s *Service) GetProfile(name string) (ProfileView, error) {
	rec, err := s.store.Get(name)
	if err != nil {
		return ProfileView{}, err
	}
	return toView(rec), nil
}

// DeleteProfile 删除档案；不存在时透传 ErrProfileNotFound。
func (s *Service) DeleteProfile(name string) error {
	return s.store.Delete(name)
}

// CalculateWithProfile 路径一：点名已登记档案，配上模式指数与一个或多个频率求值。
//
// 取档案是一次无锁的原子加载，得到某一次真实登记的完整不可变记录：
// 几何回显、截止频率、逐频点结果与登记序号必然来自同一次登记，不会出现
// 拼凑结果；删除返回后这里立即未找到，同名重登返回后这里立即看到新记录。
// 热路径不与登记/删除争抢任何锁，也不被其它名字的写操作拖慢。
func (s *Service) CalculateWithProfile(name string, mode domain.Mode, freqs []float64) (*Calculation, error) {
	if err := validate.Mode(mode); err != nil {
		return nil, err
	}
	rec, err := s.store.Get(name)
	if err != nil {
		return nil, err
	}
	calc := dispatch(rec.Geometry, mode, freqs)
	calc.ProfileName = &rec.Name
	gen := rec.Generation
	calc.ProfileGeneration = &gen
	return calc, nil
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
