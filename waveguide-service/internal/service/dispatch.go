package service

import (
	"fmt"

	"waveguide-service/internal/domain"
	"waveguide-service/internal/physics"
	"waveguide-service/internal/validate"
)

// FrequencyOutcome 批量求值中单个频率点的结果：
// 要么给出核算结果，要么给出该频率点自身的错误，互不影响。
type FrequencyOutcome struct {
	Frequency float64          `json:"frequency"`
	OK        bool             `json:"ok"`
	Result    *domain.Analysis `json:"result,omitempty"`
	Error     string           `json:"error,omitempty"`
}

// Calculation 一次计算查询的完整结果。
type Calculation struct {
	ProfileName     *string            `json:"profile_name,omitempty"` // 仅"点名档案"路径携带
	Geometry        domain.Geometry    `json:"geometry"`
	Mode            domain.Mode        `json:"mode"`
	CutoffFrequency float64            `json:"cutoff_frequency"` // 该模式截止频率 (Hz)
	Results         []FrequencyOutcome `json:"results"`
}

// dispatch 是"点名档案计算"与"临时提交计算"两条路径共用的唯一核算入口。
// 两条路径最终都汇聚到这里走同一套公式，不允许为临时提交另写简化版。
// 截止频率与介质相速只算一次，逐频率点独立求值。
func dispatch(g domain.Geometry, mode domain.Mode, freqs []float64) *Calculation {
	fc := physics.CutoffFrequency(g.BroadDimension, g.NarrowDimension,
		g.RelPermittivity, g.RelPermeability, mode.M, mode.N)
	v := physics.MediumPhaseVelocity(g.RelPermittivity, g.RelPermeability)

	calc := &Calculation{
		Geometry:        g,
		Mode:            mode,
		CutoffFrequency: fc,
		Results:         make([]FrequencyOutcome, len(freqs)),
	}
	for i, f := range freqs {
		calc.Results[i] = evalOne(f, fc, v)
	}
	return calc
}

// evalOne 求值单个频率点。任何内部异常都被隔离在该点之内，
// 不会拖累同批其它频率点正常出结果。
func evalOne(f, fc, v float64) (out FrequencyOutcome) {
	out.Frequency = f
	defer func() {
		if r := recover(); r != nil {
			out.OK = false
			out.Result = nil
			out.Error = fmt.Sprintf("internal error: %v", r)
		}
	}()
	if err := validate.Frequency(f); err != nil {
		out.OK = false
		out.Error = err.Error()
		return out
	}
	analysis := physics.Analyze(f, fc, v)
	out.OK = true
	out.Result = &analysis
	return out
}
