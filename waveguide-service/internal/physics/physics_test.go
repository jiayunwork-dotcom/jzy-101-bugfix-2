package physics_test

import (
	"math"
	"testing"

	"waveguide-service/internal/domain"
	"waveguide-service/internal/physics"
)

const relTol = 1e-12

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) <= relTol*math.Max(1.0, math.Max(math.Abs(a), math.Abs(b)))
}

// WR-90 几何（m）与空气填充，供多个用例复用。
const (
	aWR90 = 0.02286
	bWR90 = 0.01016
)

// 关系一：只把宽边放大到两倍，主模（TE10，以及一般的 TE_m0）截止频率必须精确减半。
// fc = c/(2a)·√(...)，a→2a 时主模项恰为 1/2（2 的幂缩放，浮点意义上逐位相等）。
func TestCutoffFrequencyHalvesWhenBroadDimensionDoubles(t *testing.T) {
	for _, m := range []int{1, 2, 3} {
		fc1 := physics.CutoffFrequency(aWR90, bWR90, 1, 1, m, 0)
		fc2 := physics.CutoffFrequency(2*aWR90, bWR90, 1, 1, m, 0)
		if fc2 != fc1/2 {
			t.Fatalf("TE%d0: doubling broad dimension must halve cutoff exactly: fc1=%.17g fc2=%.17g want=%.17g",
				m, fc1, fc2, fc1/2)
		}
	}

	// 对照：对含 n≠0 分量的模式，宽边加倍不保证减半（公式里还有 (n/b)² 项），
	// 但截止频率必然下降。
	g1 := physics.CutoffFrequency(aWR90, bWR90, 1, 1, 2, 1)
	g2 := physics.CutoffFrequency(2*aWR90, bWR90, 1, 1, 2, 1)
	if !(g2 < g1) {
		t.Fatalf("widening guide must not raise any mode cutoff: g1=%v g2=%v", g1, g2)
	}
}

// 关系二：相对介电常数从真空（1）换成 4，截止频率必须精确减半。
// fc ∝ 1/√εr，而 √4=2 且除以 2 均为浮点精确操作。
func TestCutoffFrequencyHalvesWhenPermittivityQuadruples(t *testing.T) {
	for _, mn := range [][2]int{{1, 0}, {2, 1}, {3, 2}} {
		fc1 := physics.CutoffFrequency(aWR90, bWR90, 1, 1, mn[0], mn[1])
		fc4 := physics.CutoffFrequency(aWR90, bWR90, 4, 1, mn[0], mn[1])
		if fc4 != fc1/2 {
			t.Fatalf("mode %v: εr 1→4 must halve cutoff exactly: fc1=%.17g fc4=%.17g want=%.17g",
				mn, fc1, fc4, fc1/2)
		}
	}
}

// 关系三：同一截面下，模式指数更高的模式截止频率不低于主模 TE10。
func TestHigherModeCutoffNotBelowDominant(t *testing.T) {
	fcDom := physics.CutoffFrequency(aWR90, bWR90, 1, 1, 1, 0)
	modes := [][2]int{{2, 0}, {0, 1}, {1, 1}, {2, 1}, {3, 0}, {3, 2}, {5, 4}}
	for _, mn := range modes {
		fc := physics.CutoffFrequency(aWR90, bWR90, 1, 1, mn[0], mn[1])
		if fc < fcDom {
			t.Errorf("mode (%d,%d) cutoff %.6g below dominant %.6g", mn[0], mn[1], fc, fcDom)
		}
	}
}

// 关系四：工作频率正好等于截止频率 => 临界状态。
// 既不判渐逝，也不输出无穷大波导波长；β=0、α=0，波导波长字段必须为空。
func TestCriticalAtCutoff(t *testing.T) {
	v := physics.MediumPhaseVelocity(1, 1)
	fc := physics.CutoffFrequency(aWR90, bWR90, 1, 1, 1, 0)

	res := physics.Analyze(fc, fc, v)
	if res.State != domain.StateCritical {
		t.Fatalf("f == fc must be critical, got %v", res.State)
	}
	if res.GuideWavelength != nil {
		t.Fatalf("critical state must not fabricate a guide wavelength, got %v", *res.GuideWavelength)
	}
	if res.PhaseConstant == nil || *res.PhaseConstant != 0 {
		t.Fatalf("critical phase constant must be 0, got %v", res.PhaseConstant)
	}
	if res.AttenuationConstant == nil || *res.AttenuationConstant != 0 {
		t.Fatalf("critical attenuation constant must be 0, got %v", res.AttenuationConstant)
	}
	if !math.IsInf(1/(*res.PhaseConstant), 0) {
		t.Fatalf("2π/β at critical must diverge physically, confirming λg is undefined")
	}

	// 临界两侧状态明确：下方渐逝、上方传导。
	below := physics.Analyze(fc*(1-1e-6), fc, v)
	if below.State != domain.StateEvanescent {
		t.Errorf("just below cutoff must be evanescent, got %v", below.State)
	}
	above := physics.Analyze(fc*(1+1e-6), fc, v)
	if above.State != domain.StatePropagating {
		t.Errorf("just above cutoff must be propagating, got %v", above.State)
	}
}

// 关系五：频率从截止点升高，波导波长单调下降并逼近介质波长。
func TestGuideWavelengthMonotonicApproachesMediumWavelength(t *testing.T) {
	v := physics.MediumPhaseVelocity(1, 1)
	fc := physics.CutoffFrequency(aWR90, bWR90, 1, 1, 1, 0)

	prev := math.Inf(1)
	for _, ratio := range []float64{1.0001, 1.001, 1.01, 1.1, 1.5, 2, 3, 5, 10, 100} {
		f := fc * ratio
		res := physics.Analyze(f, fc, v)
		if res.State != domain.StatePropagating || res.GuideWavelength == nil {
			t.Fatalf("f/fc=%v must be propagating with guide wavelength", ratio)
		}
		lg := *res.GuideWavelength
		if lg >= prev {
			t.Errorf("guide wavelength must decrease as f rises: f/fc=%v λg=%v prev=%v", ratio, lg, prev)
		}
		if lg <= res.MediumWavelength {
			t.Errorf("guide wavelength %v must stay above medium wavelength %v", lg, res.MediumWavelength)
		}
		prev = lg
	}

	// 高频极限：λg → λ（介质波长）。
	res := physics.Analyze(fc*1e6, fc, v)
	if !almostEqual(*res.GuideWavelength, res.MediumWavelength) {
		t.Errorf("guide wavelength %v should approach medium wavelength %v", *res.GuideWavelength, res.MediumWavelength)
	}
}

// 截止频率之下：渐逝，只给衰减常数，不能出现波导波长。
func TestEvanescentBelowCutoff(t *testing.T) {
	v := physics.MediumPhaseVelocity(1, 1)
	fc := physics.CutoffFrequency(aWR90, bWR90, 1, 1, 1, 0)

	res := physics.Analyze(0.5*fc, fc, v)
	if res.State != domain.StateEvanescent {
		t.Fatalf("f < fc must be evanescent, got %v", res.State)
	}
	if res.GuideWavelength != nil {
		t.Fatalf("evanescent state must not carry a guide wavelength, got %v", *res.GuideWavelength)
	}
	if res.PhaseConstant != nil {
		t.Fatalf("evanescent state must not carry a real phase constant, got %v", *res.PhaseConstant)
	}
	if res.AttenuationConstant == nil || *res.AttenuationConstant <= 0 {
		t.Fatalf("evanescent state must give positive attenuation constant, got %v", res.AttenuationConstant)
	}
	// 与经典式 α = 2π√((fc/v)²-(f/v)²) 对照。
	f := 0.5 * fc
	want := 2 * math.Pi * math.Sqrt(math.Pow(fc/v, 2)-math.Pow(f/v, 2))
	if !almostEqual(*res.AttenuationConstant, want) {
		t.Fatalf("attenuation constant %v != classic formula %v", *res.AttenuationConstant, want)
	}

	// 离截止越远（频率越低），衰减常数越大。
	more := physics.Analyze(0.25*fc, fc, v)
	if *more.AttenuationConstant <= *res.AttenuationConstant {
		t.Errorf("attenuation must grow farther below cutoff: %v vs %v", *more.AttenuationConstant, *res.AttenuationConstant)
	}
}

// 手册核对：WR-90 主模截止频率约 6.557 GHz；10 GHz 处 λg 约 39.7 mm。
func TestWR90ReferenceValues(t *testing.T) {
	fc := physics.CutoffFrequency(aWR90, bWR90, 1, 1, 1, 0)
	if math.Abs(fc-6.557e9)/6.557e9 > 1e-3 {
		t.Fatalf("WR-90 TE10 cutoff ~6.557 GHz expected, got %v Hz", fc)
	}

	v := physics.MediumPhaseVelocity(1, 1)
	res := physics.Analyze(10e9, fc, v)
	if res.State != domain.StatePropagating {
		t.Fatalf("WR-90 at 10 GHz must propagate TE10, got %v", res.State)
	}
	if math.Abs(*res.GuideWavelength-0.0397)/0.0397 > 1e-2 {
		t.Fatalf("WR-90 λg @10 GHz ~39.7 mm expected, got %v m", *res.GuideWavelength)
	}
}
