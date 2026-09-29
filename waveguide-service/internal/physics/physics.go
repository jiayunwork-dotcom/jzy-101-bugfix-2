// Package physics 矩形波导核心电磁公式与传播状态核算。
// 本包只包含纯函数，不感知 HTTP、存储等任何外部概念；
// 所有输入假定已通过 validate 包校验。
package physics

import (
	"math"

	"waveguide-service/internal/domain"
)

// SpeedOfLight 真空光速 (m/s)，SI 精确值。
const SpeedOfLight = 299792458.0

// cutoffRelTol 判定"工作频率恰好等于截止频率"的相对容差。
// 浮点表示下直接 == 比较不可靠，当 |f-fc| <= cutoffRelTol*fc 时视为临界。
const cutoffRelTol = 1e-9

// CutoffFrequency 计算矩形波导 TE/TM(m,n) 模的截止频率 (Hz)：
//
//	fc = c / (2·√(εr·μr)) · √((m/a)² + (n/b)²)
//
// 前置条件（由 validate 保证）：a > b > 0，εr >= 1，μr >= 1，
// m、n 为非负整数且不同时为零。
func CutoffFrequency(a, b, er, ur float64, m, n int) float64 {
	ma := float64(m) / a
	nb := float64(n) / b
	return SpeedOfLight / (2 * math.Sqrt(er*ur)) * math.Sqrt(ma*ma+nb*nb)
}

// MediumPhaseVelocity 填充介质中的相速 (m/s)：v = c/√(εr·μr)。
func MediumPhaseVelocity(er, ur float64) float64 {
	return SpeedOfLight / math.Sqrt(er*ur)
}

// MediumWavelength 介质中的自由空间波长 (m)：λ = v/f。
func MediumWavelength(v, f float64) float64 {
	return v / f
}

// PhaseConstant 传导状态下的相位常数 β (rad/m)：
//
//	β = (2πf/v)·√(1-(fc/f)²)，要求 f > fc。
func PhaseConstant(f, fc, v float64) float64 {
	k := 2 * math.Pi * f / v
	r := fc / f
	return k * math.Sqrt(1-r*r)
}

// GuideWavelength 波导波长 (m)：λg = 2π/β，要求 f > fc。
// f 从截止点升高时 λg 单调下降并趋近介质波长 v/f。
func GuideWavelength(f, fc, v float64) float64 {
	return 2 * math.Pi / PhaseConstant(f, fc, v)
}

// AttenuationConstant 渐逝状态下的衰减常数 α (Np/m)：
//
//	α = (2πfc/v)·√(1-(f/fc)²)，要求 f < fc。
//
// 采用该形式而非 √((2πfc/v)²-(2πf/v)²)，避免近截止点处两个近等数相减
// 带来的灾难性抵消。
func AttenuationConstant(f, fc, v float64) float64 {
	kc := 2 * math.Pi * fc / v
	r := f / fc
	return kc * math.Sqrt(1-r*r)
}

// Analyze 对单个频率点做传播状态判定并计算相应物理量。
// fc 为对应模式的截止频率，v 为介质相速，均由调用方预先算好
// （批量求值时避免逐点重复开方）。
func Analyze(f, fc, v float64) domain.Analysis {
	res := domain.Analysis{
		Frequency:        f,
		MediumWavelength: MediumWavelength(v, f),
	}
	switch d := f - fc; {
	case d > cutoffRelTol*fc:
		// 传导：给出相位常数与波导波长。
		beta := PhaseConstant(f, fc, v)
		lambdaG := 2 * math.Pi / beta
		res.State = domain.StatePropagating
		res.PhaseConstant = &beta
		res.GuideWavelength = &lambdaG
	case d < -cutoffRelTol*fc:
		// 渐逝：只给出衰减常数，不存在实数波导波长。
		alpha := AttenuationConstant(f, fc, v)
		res.State = domain.StateEvanescent
		res.AttenuationConstant = &alpha
	default:
		// 临界：f ≈ fc。此时 β=0（λg→∞，无有限实数波导波长）、α=0，
		// 明确标记为临界，既不判渐逝也不输出无穷大的波导波长。
		zero := 0.0
		res.State = domain.StateCritical
		res.PhaseConstant = &zero
		res.AttenuationConstant = &zero
	}
	return res
}
