package service_test

import (
	"errors"
	"math"
	"testing"

	"waveguide-service/internal/domain"
	"waveguide-service/internal/service"
	"waveguide-service/internal/store"
)

const (
	aWR90 = 0.02286
	bWR90 = 0.01016
)

func wr90Geometry(er float64) domain.Geometry {
	return domain.Geometry{BroadDimension: aWR90, NarrowDimension: bWR90, RelPermittivity: er, RelPermeability: 1}
}

func newTestService() *service.Service {
	return service.New(store.NewMemoryStore())
}

// 两条计算路径（点名档案 / 临时提交）必须走同一套核算逻辑，结果逐字段一致。
func TestTwoPathsShareSameLogic(t *testing.T) {
	svc := newTestService()
	g := wr90Geometry(1)
	if _, err := svc.RegisterProfile(domain.Profile{Name: "copy-WR90", Geometry: g}); err != nil {
		t.Fatal(err)
	}
	mode := domain.Mode{M: 2, N: 1}
	freqs := []float64{5e9, 10e9, 15e9, 20e9}

	viaProfile, err := svc.CalculateWithProfile("copy-WR90", mode, freqs)
	if err != nil {
		t.Fatal(err)
	}
	adHoc, err := svc.CalculateAdHoc(g, mode, freqs)
	if err != nil {
		t.Fatal(err)
	}
	if viaProfile.CutoffFrequency != adHoc.CutoffFrequency {
		t.Fatalf("cutoff mismatch between paths: %v vs %v", viaProfile.CutoffFrequency, adHoc.CutoffFrequency)
	}
	if len(viaProfile.Results) != len(adHoc.Results) {
		t.Fatalf("result length mismatch: %d vs %d", len(viaProfile.Results), len(adHoc.Results))
	}
	for i := range freqs {
		r1, r2 := viaProfile.Results[i], adHoc.Results[i]
		if r1.OK != r2.OK || (r1.Result == nil) != (r2.Result == nil) {
			t.Fatalf("outcome %d differs: %+v vs %+v", i, r1, r2)
		}
		if r1.Result != nil {
			a, b := *r1.Result, *r2.Result
			if a.State != b.State || a.MediumWavelength != b.MediumWavelength {
				t.Fatalf("analysis %d differs: %+v vs %+v", i, a, b)
			}
			if (a.GuideWavelength == nil) != (b.GuideWavelength == nil) {
				t.Fatalf("guide wavelength presence differs at %d", i)
			}
			if (a.AttenuationConstant == nil) != (b.AttenuationConstant == nil) {
				t.Fatalf("attenuation presence differs at %d", i)
			}
		}
	}
}

// 批量求值：非法频率点只影响自身，其余点正常出结果。
func TestBatchFrequencyIsolation(t *testing.T) {
	svc := newTestService()
	freqs := []float64{10e9, -5, 0, 12e9}
	calc, err := svc.CalculateWithProfile("WR-90", domain.Mode{M: 1, N: 0}, freqs)
	if err != nil {
		t.Fatal(err)
	}
	if len(calc.Results) != 4 {
		t.Fatalf("want 4 outcomes, got %d", len(calc.Results))
	}
	wantOK := []bool{true, false, false, true}
	for i, want := range wantOK {
		if calc.Results[i].OK != want {
			t.Errorf("outcome %d: want ok=%v got ok=%v err=%q", i, want, calc.Results[i].OK, calc.Results[i].Error)
		}
	}
	if calc.Results[0].Result.State != domain.StatePropagating {
		t.Errorf("10 GHz should propagate: %v", calc.Results[0].Result.State)
	}
	if calc.Results[3].Result.State != domain.StatePropagating {
		t.Errorf("12 GHz should propagate: %v", calc.Results[3].Result.State)
	}
	if calc.Results[1].Error == "" || calc.Results[2].Error == "" {
		t.Error("invalid frequency points must carry specific error messages")
	}
}

// 精确等于截止频率的频率点经服务查询后必须是 critical，且不携带波导波长。
func TestCriticalFrequencyThroughService(t *testing.T) {
	svc := newTestService()
	fc := svc.ListProfiles()[0].DominantCutoffFrequency // WR-90 TE10
	calc, err := svc.CalculateWithProfile("WR-90", domain.Mode{M: 1, N: 0}, []float64{fc})
	if err != nil {
		t.Fatal(err)
	}
	r := calc.Results[0]
	if !r.OK || r.Result.State != domain.StateCritical {
		t.Fatalf("f == fc must be critical, got %+v", r)
	}
	if r.Result.GuideWavelength != nil {
		t.Fatalf("critical result must not carry guide wavelength: %v", *r.Result.GuideWavelength)
	}
	if r.Result.AttenuationConstant == nil || *r.Result.AttenuationConstant != 0 {
		t.Fatalf("critical attenuation must be 0")
	}
}

// 渐逝点不允许硬凑波导波长，只给衰减常数。
func TestEvanescentResultFields(t *testing.T) {
	svc := newTestService()
	calc, err := svc.CalculateAdHoc(wr90Geometry(1), domain.Mode{M: 1, N: 0}, []float64{3e9})
	if err != nil {
		t.Fatal(err)
	}
	r := calc.Results[0].Result
	if r.State != domain.StateEvanescent {
		t.Fatalf("3 GHz below cutoff must be evanescent, got %v", r.State)
	}
	if r.GuideWavelength != nil || r.PhaseConstant != nil {
		t.Fatal("evanescent result must omit guide wavelength and phase constant")
	}
	if r.AttenuationConstant == nil || *r.AttenuationConstant <= 0 {
		t.Fatal("evanescent result must carry positive attenuation constant")
	}
}

// 宽边加倍 => 截止频率减半；εr=4 => 截止频率减半（服务层端到端再验一次）。
func TestCutoffScalingEndToEnd(t *testing.T) {
	svc := newTestService()
	mode := domain.Mode{M: 1, N: 0}

	base, err := svc.CalculateAdHoc(wr90Geometry(1), mode, []float64{1e10})
	if err != nil {
		t.Fatal(err)
	}
	doubled, err := svc.CalculateAdHoc(
		domain.Geometry{BroadDimension: 2 * aWR90, NarrowDimension: bWR90, RelPermittivity: 1, RelPermeability: 1},
		mode, []float64{1e10})
	if err != nil {
		t.Fatal(err)
	}
	if doubled.CutoffFrequency != base.CutoffFrequency/2 {
		t.Fatalf("doubling broad side: %v != %v/2", doubled.CutoffFrequency, base.CutoffFrequency)
	}

	er4, err := svc.CalculateAdHoc(wr90Geometry(4), mode, []float64{1e10})
	if err != nil {
		t.Fatal(err)
	}
	if er4.CutoffFrequency != base.CutoffFrequency/2 {
		t.Fatalf("εr=4: %v != %v/2", er4.CutoffFrequency, base.CutoffFrequency)
	}
}

// 错误路径：不存在档案、非法模式、非法几何、重复登记。
func TestErrorPaths(t *testing.T) {
	svc := newTestService()

	if _, err := svc.CalculateWithProfile("no-such-profile", domain.Mode{M: 1, N: 0}, []float64{1e10}); !errors.Is(err, store.ErrProfileNotFound) {
		t.Fatalf("expected ErrProfileNotFound, got %v", err)
	}
	if _, err := svc.CalculateWithProfile("WR-90", domain.Mode{M: 0, N: 0}, []float64{1e10}); err == nil {
		t.Fatal("(0,0) mode must be rejected")
	}
	if _, err := svc.CalculateAdHoc(
		domain.Geometry{BroadDimension: 0.01, NarrowDimension: 0.02, RelPermittivity: 1, RelPermeability: 1},
		domain.Mode{M: 1, N: 0}, []float64{1e10}); err == nil {
		t.Fatal("broad <= narrow geometry must be rejected")
	}
	_, err := svc.RegisterProfile(domain.Profile{Name: "WR-90", Geometry: wr90Geometry(1)})
	if !errors.Is(err, store.ErrProfileExists) {
		t.Fatalf("duplicate name must conflict, got %v", err)
	}
	if err := svc.DeleteProfile("ghost"); !errors.Is(err, store.ErrProfileNotFound) {
		t.Fatalf("delete missing must be ErrProfileNotFound, got %v", err)
	}
}

// 内置 WR-90 在 X 波段常见工作频段内为单模：
// TE10 传导，而紧邻的高阶模 TE20、TE01 均处于截止以下。
func TestWR90SingleModeWindow(t *testing.T) {
	svc := newTestService()
	for _, f := range []float64{8.2e9, 10e9, 12.4e9} {
		te10, err := svc.CalculateWithProfile("WR-90", domain.Mode{M: 1, N: 0}, []float64{f})
		if err != nil {
			t.Fatal(err)
		}
		if te10.Results[0].Result.State != domain.StatePropagating {
			t.Errorf("%.1f GHz: TE10 must propagate", f/1e9)
		}
		for _, higher := range []domain.Mode{{M: 2, N: 0}, {M: 0, N: 1}} {
			res, err := svc.CalculateWithProfile("WR-90", higher, []float64{f})
			if err != nil {
				t.Fatal(err)
			}
			if got := res.Results[0].Result.State; got != domain.StateEvanescent {
				t.Errorf("%.1f GHz: mode %v must be evanescent, got %v", f/1e9, higher, got)
			}
			if math.Abs(res.CutoffFrequency-te10.CutoffFrequency) < 1 {
				t.Errorf("mode %v cutoff must differ from TE10", higher)
			}
		}
	}
}
