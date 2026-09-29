// Package domain 定义贯穿各层共享的核心领域类型：
// 波导截面几何、模式指数、参数档案与单频点分析结果。
// 本包不依赖任何其它内部包，以避免循环依赖。
package domain

// Geometry 描述矩形波导截面与填充介质。
// 单位约定：宽边/窄边为米（m），相对介电常数与相对磁导率无量纲。
type Geometry struct {
	BroadDimension  float64 `json:"broad_dimension"`  // 宽边 a，单位 m
	NarrowDimension float64 `json:"narrow_dimension"` // 窄边 b，单位 m
	RelPermittivity float64 `json:"rel_permittivity"` // 相对介电常数 εr
	RelPermeability float64 `json:"rel_permeability"` // 相对磁导率 μr
}

// Mode 矩形波导模式指数 (m, n)。
type Mode struct {
	M int `json:"m"`
	N int `json:"n"`
}

// Profile 具名参数档案：一个名字绑定一套截面与介质参数。
type Profile struct {
	Name     string   `json:"name"`
	Geometry Geometry `json:"geometry"`
}

// State 传播状态。
type State string

const (
	// StatePropagating 传导传播：f > fc，存在实数波导波长。
	StatePropagating State = "propagating"
	// StateCritical 临界：f == fc（在浮点容差内）。β=0、α=0，
	// 波导波长趋于无穷，不存在有限的实数波导波长。
	StateCritical State = "critical"
	// StateEvanescent 渐逝衰减：f < fc，只能给出衰减常数。
	StateEvanescent State = "evanescent"
)

// Analysis 单个频率点的核算结果。
// 指针字段仅在对应状态下有物理意义时才赋值：
//   - propagating：PhaseConstant、GuideWavelength 有值；
//   - evanescent：AttenuationConstant 有值，绝不硬凑波导波长；
//   - critical：PhaseConstant、AttenuationConstant 均为 0，GuideWavelength 为空。
type Analysis struct {
	Frequency           float64  `json:"frequency"`                      // 工作频率 (Hz)
	State               State    `json:"state"`                          // 传播状态
	MediumWavelength    float64  `json:"medium_wavelength"`              // 介质中的自由空间波长 (m)
	PhaseConstant       *float64 `json:"phase_constant,omitempty"`       // 相位常数 β (rad/m)
	GuideWavelength     *float64 `json:"guide_wavelength,omitempty"`     // 波导波长 λg (m)
	AttenuationConstant *float64 `json:"attenuation_constant,omitempty"` // 衰减常数 α (Np/m)
}
