package service_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"waveguide-service/internal/domain"
	"waveguide-service/internal/physics"
	"waveguide-service/internal/service"
	"waveguide-service/internal/store"
)

func benchGeometry(a float64) domain.Geometry {
	return domain.Geometry{
		BroadDimension:  a,
		NarrowDimension: 0.01,
		RelPermittivity: 1,
		RelPermeability: 1,
	}
}

func te10At6GHz(t *testing.T, svc *service.Service, name string) *service.Calculation {
	t.Helper()
	calc, err := svc.CalculateWithProfile(name, domain.Mode{M: 1, N: 0}, []float64{6e9})
	if err != nil {
		t.Fatalf("calculate %s: %v", name, err)
	}
	return calc
}

// adHocTE10At6GHz 走公共临时现算路径，作为点名档案结果的逐位对照。
func adHocTE10At6GHz(g domain.Geometry) (*service.Calculation, error) {
	svc := service.New(store.NewMemoryStore())
	return svc.CalculateAdHoc(g, domain.Mode{M: 1, N: 0}, []float64{6e9})
}

func assertAdHocBitEqual(t *testing.T, got *service.Calculation, g domain.Geometry) {
	t.Helper()
	want, err := adHocTE10At6GHz(g)
	if err != nil {
		t.Fatal(err)
	}
	if got.Geometry != want.Geometry || got.CutoffFrequency != want.CutoffFrequency {
		t.Fatalf("named calculation does not match ad hoc: geometry %+v vs %+v, cutoff %v vs %v",
			got.Geometry, want.Geometry, got.CutoffFrequency, want.CutoffFrequency)
	}
	if len(got.Results) != len(want.Results) {
		t.Fatalf("result count mismatch: %d vs %d", len(got.Results), len(want.Results))
	}
	for i := range want.Results {
		if !reflect.DeepEqual(got.Results[i], want.Results[i]) {
			t.Fatalf("result %d does not bit-match ad hoc:\n got  %+v\n want %+v", i, got.Results[i], want.Results[i])
		}
	}
}

// 删除已计算过的档案后，后续点名计算必须和查看档案一样返回未找到。
func TestCalculateAfterDeleteNotFound(t *testing.T) {
	svc := newTestService()
	g := benchGeometry(0.03)
	view, err := svc.RegisterProfile(domain.Profile{Name: "bench-7", Geometry: g})
	if err != nil {
		t.Fatal(err)
	}
	first := te10At6GHz(t, svc, "bench-7")
	wantFirstCutoff := physics.CutoffFrequency(g.BroadDimension, g.NarrowDimension, 1, 1, 1, 0)
	if first.CutoffFrequency != wantFirstCutoff || first.RegistrationRevision != view.RegistrationRevision {
		t.Fatalf("initial calculation mismatch: cutoff=%v rev=%d, want cutoff=%v rev=%d",
			first.CutoffFrequency, first.RegistrationRevision, wantFirstCutoff, view.RegistrationRevision)
	}

	if err := svc.DeleteProfile("bench-7"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetProfile("bench-7"); !errors.Is(err, store.ErrProfileNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	if _, err := svc.CalculateWithProfile("bench-7", domain.Mode{M: 1, N: 0}, []float64{6e9}); !errors.Is(err, store.ErrProfileNotFound) {
		t.Fatalf("calculate after delete must be ErrProfileNotFound, got %v", err)
	}
}

// 同名删除后重登，点名计算必须使用新快照；几何、截止频率、逐频点结果与临时现算一致。
func TestCalculateAfterSameNameReregistrationUsesNewRevision(t *testing.T) {
	svc := newTestService()
	oldG := benchGeometry(0.03)
	newG := benchGeometry(0.02)

	oldView, err := svc.RegisterProfile(domain.Profile{Name: "bench-7", Geometry: oldG})
	if err != nil {
		t.Fatal(err)
	}
	oldCalc := te10At6GHz(t, svc, "bench-7")
	if oldCalc.RegistrationRevision != oldView.RegistrationRevision {
		t.Fatalf("old revision mismatch: calc=%d view=%d", oldCalc.RegistrationRevision, oldView.RegistrationRevision)
	}

	if err := svc.DeleteProfile("bench-7"); err != nil {
		t.Fatal(err)
	}
	newView, err := svc.RegisterProfile(domain.Profile{Name: "bench-7", Geometry: newG})
	if err != nil {
		t.Fatal(err)
	}
	if newView.RegistrationRevision <= oldView.RegistrationRevision {
		t.Fatalf("new registration revision %d must be greater than old %d",
			newView.RegistrationRevision, oldView.RegistrationRevision)
	}
	stored, err := svc.GetProfile("bench-7")
	if err != nil {
		t.Fatal(err)
	}
	if stored.RegistrationRevision != newView.RegistrationRevision {
		t.Fatalf("view revision mismatch: get=%d register=%d", stored.RegistrationRevision, newView.RegistrationRevision)
	}

	newCalc := te10At6GHz(t, svc, "bench-7")
	if newCalc.RegistrationRevision != newView.RegistrationRevision {
		t.Fatalf("calculation used revision %d, want %d", newCalc.RegistrationRevision, newView.RegistrationRevision)
	}
	if newCalc.Geometry.BroadDimension != 0.02 || newCalc.CutoffFrequency != stored.DominantCutoffFrequency {
		t.Fatalf("new calculation not based on new profile: %+v, cutoff %v vs view %v",
			newCalc.Geometry, newCalc.CutoffFrequency, stored.DominantCutoffFrequency)
	}
	if newCalc.CutoffFrequency == oldCalc.CutoffFrequency {
		t.Fatal("new cutoff must differ from cached old cutoff")
	}
	if state := newCalc.Results[0].Result.State; state != domain.StateEvanescent {
		t.Fatalf("6 GHz must be evanescent for a=0.02m, got %s", state)
	}
	assertAdHocBitEqual(t, newCalc, newG)
}

// 冷缓存路径同样必须识别同名删除后重登（这是旧缺陷的另一半常见时序）。
func TestSameNameReregistrationWithoutPriorCalculation(t *testing.T) {
	svc := newTestService()
	oldG := benchGeometry(0.03)
	newG := benchGeometry(0.02)
	if _, err := svc.RegisterProfile(domain.Profile{Name: "cold-bench", Geometry: oldG}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteProfile("cold-bench"); err != nil {
		t.Fatal(err)
	}
	newView, err := svc.RegisterProfile(domain.Profile{Name: "cold-bench", Geometry: newG})
	if err != nil {
		t.Fatal(err)
	}
	calc := te10At6GHz(t, svc, "cold-bench")
	if calc.RegistrationRevision != newView.RegistrationRevision ||
		calc.CutoffFrequency != physics.CutoffFrequency(0.02, 0.01, 1, 1, 1, 0) {
		t.Fatalf("cold resolve returned wrong revision/cutoff: rev=%d cutoff=%v",
			calc.RegistrationRevision, calc.CutoffFrequency)
	}
}

type countingStore struct {
	service.Store
	gets atomic.Int64
}

func (s *countingStore) Get(name string) (domain.Profile, error) {
	s.gets.Add(1)
	return s.Store.Get(name)
}

// 快照预热后，高频点名计算只命中进程内不可变快照，不再访问存储，也就不争抢存储锁。
func TestHotCalculationsDoNotTouchStore(t *testing.T) {
	st := &countingStore{Store: store.NewMemoryStore()}
	svc := service.New(st)
	g := benchGeometry(0.03)
	if _, err := svc.RegisterProfile(domain.Profile{Name: "hot-bench", Geometry: g}); err != nil {
		t.Fatal(err)
	}
	// Register 成功时已发布快照；从这里开始的点名计算都应完全命中缓存。
	_ = te10At6GHz(t, svc, "hot-bench")
	st.gets.Store(0)

	const clients = 64
	const scans = 50
	var wg sync.WaitGroup
	errCh := make(chan error, clients)
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < scans; j++ {
				calc, err := svc.CalculateWithProfile("hot-bench", domain.Mode{M: 1, N: 0}, []float64{6e9, 8e9, 10e9})
				if err != nil {
					errCh <- err
					return
				}
				if calc.Geometry != g {
					errCh <- errors.New("hot calculation returned wrong geometry")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if got := st.gets.Load(); got != 0 {
		t.Fatalf("hot calculations made %d store Get calls; cached path must not contend on store", got)
	}
}

// 同名删除、重登与大量计算交错时，每次成功计算都必须完整对应一次真实登记；
// 删除返回后的未找到也是合法结果。请用 go test -race 运行本用例。
func TestConcurrentDeleteReregisterAndCalculate(t *testing.T) {
	svc := newTestService()
	const name = "racing-bench"
	geometries := []domain.Geometry{benchGeometry(0.03), benchGeometry(0.02)}

	first, err := svc.RegisterProfile(domain.Profile{Name: name, Geometry: geometries[0]})
	if err != nil {
		t.Fatal(err)
	}
	var revisions sync.Map // int64 revision -> domain.Geometry
	revisions.Store(first.RegistrationRevision, geometries[0])

	stop := make(chan struct{})
	var writerWg, readerWg sync.WaitGroup
	var failuresMu sync.Mutex
	var failures []error
	addFailure := func(err error) {
		failuresMu.Lock()
		failures = append(failures, err)
		failuresMu.Unlock()
	}
	lastRevision := first.RegistrationRevision

	writerWg.Add(1)
	go func() {
		defer writerWg.Done()
		for round := 0; round < 200; round++ {
			if err := svc.DeleteProfile(name); err != nil {
				addFailure(err)
				return
			}
			g := geometries[(round+1)%len(geometries)]
			expectedRevision := first.RegistrationRevision + int64(round+1)
			// 先登记测试侧将要接受的下一个序号；该序号只有在 Register 成功发布后
			// 才可能出现在计算响应里。这样不会把“登记返回与测试记账之间”的窗口误判成失败。
			revisions.Store(expectedRevision, g)
			view, err := svc.RegisterProfile(domain.Profile{Name: name, Geometry: g})
			if err != nil {
				addFailure(err)
				return
			}
			if view.RegistrationRevision != expectedRevision {
				addFailure(fmt.Errorf("registration got revision %d, want %d", view.RegistrationRevision, expectedRevision))
				return
			}
			lastRevision = view.RegistrationRevision
		}
		close(stop)
	}()

	mode := domain.Mode{M: 1, N: 0}
	freqs := []float64{6e9}
	for worker := 0; worker < 12; worker++ {
		readerWg.Add(1)
		go func() {
			defer readerWg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				calc, err := svc.CalculateWithProfile(name, mode, freqs)
				switch {
				case errors.Is(err, store.ErrProfileNotFound):
					// 只可能出现在删除已经返回、下一次重登尚未返回之间。
					continue
				case err != nil:
					addFailure(err)
					return
				}

				if calc.ProfileName == nil || *calc.ProfileName != name {
					addFailure(errors.New("calculation missing or mismatched profile name"))
					return
				}
				expectedGeometryValue, ok := revisions.Load(calc.RegistrationRevision)
				if !ok {
					addFailure(fmt.Errorf("calculation used unregistered revision %d", calc.RegistrationRevision))
					return
				}
				expectedGeometry := expectedGeometryValue.(domain.Geometry)
				expectedCutoff := physics.CutoffFrequency(
					expectedGeometry.BroadDimension, expectedGeometry.NarrowDimension,
					1, 1, mode.M, mode.N)
				if calc.Geometry != expectedGeometry {
					addFailure(errors.New("calculation echoed geometry from a different revision"))
					return
				}
				if calc.CutoffFrequency != expectedCutoff {
					addFailure(errors.New("cutoff was calculated from a different revision than the echoed geometry"))
					return
				}
				if len(calc.Results) != 1 || !calc.Results[0].OK {
					addFailure(errors.New("valid 6 GHz calculation returned an invalid frequency outcome"))
					return
				}
				wantState := physics.Analyze(6e9, expectedCutoff, physics.SpeedOfLight).State
				if calc.Results[0].Result.State != wantState {
					addFailure(errors.New("propagation state does not match selected revision"))
					return
				}
			}
		}()
	}

	writerWg.Wait()
	readerWg.Wait()
	for _, err := range failures {
		t.Error(err)
	}

	finalView, err := svc.GetProfile(name)
	if err != nil {
		t.Fatal(err)
	}
	if finalView.RegistrationRevision != lastRevision {
		t.Fatalf("final view revision %d != %d", finalView.RegistrationRevision, lastRevision)
	}
	finalCalc := te10At6GHz(t, svc, name)
	if finalCalc.RegistrationRevision != finalView.RegistrationRevision {
		t.Fatalf("final calculation revision %d != view revision %d",
			finalCalc.RegistrationRevision, finalView.RegistrationRevision)
	}
}
