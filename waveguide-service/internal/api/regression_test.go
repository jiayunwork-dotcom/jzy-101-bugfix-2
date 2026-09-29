package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"waveguide-service/internal/domain"
	"waveguide-service/internal/physics"
)

// 本文件是"删档 / 同名重登后点名计算仍用旧截面"问题的回归用例。
// 复现路径（bench-7，空气填充，TE10，6 GHz）：
//  1. 登记 a=0.03,b=0.01 → 点名 6 GHz：截止约 4.9965 GHz、传导；
//  2. 删除（返回成功、查看 404）→ 点名必须同样 404，不得再出旧结果；
//  3. 同名重登 a=0.02,b=0.01 → 点名必须按新尺寸：截止约 7.4948 GHz、渐逝，
//     且与同尺寸临时现算逐位一致。

// fullCalcResponse 回归测试用的完整计算响应：比 api_test.go 的 calcResponse
// 多带出几何回显与登记序号字段。
type fullCalcResponse struct {
	ProfileName       *string         `json:"profile_name"`
	ProfileGeneration *uint64         `json:"profile_generation"`
	Geometry          domain.Geometry `json:"geometry"`
	CutoffFrequency   float64         `json:"cutoff_frequency"`
	Results           []freqResult    `json:"results"`
}

func doFullCalc(t *testing.T, rtr http.Handler, path string, body map[string]any) (int, fullCalcResponse) {
	if t != nil {
		t.Helper()
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, req)
	var calc fullCalcResponse
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &calc)
	}
	return w.Code, calc
}

func profilePath(name string) string { return "/api/v1/profiles/" + name }
func calcPath(name string) string    { return profilePath(name) + "/calculate" }

// mustGeneration 从档案视图 JSON 中取出登记序号；缺失时直接判失败——
// 视图必须能让人分辨这是哪一次登记。
func mustGeneration(t *testing.T, body map[string]any) uint64 {
	t.Helper()
	g, ok := body["generation"].(float64)
	if !ok {
		t.Fatalf("profile view must carry generation, got %v", body)
	}
	return uint64(g)
}

func ptrFloatEq(a, b *float64) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

// assertSameOutcomes 逐点逐字段（含可选指针字段的取值）比较两批频率点结果，
// 用于"点名档案"与"同尺寸临时现算"必须逐位相同的断言。
func assertSameOutcomes(t *testing.T, got, want []freqResult) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("outcome count differs: %d vs %d", len(got), len(want))
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.Frequency != w.Frequency || g.OK != w.OK || g.Error != w.Error {
			t.Fatalf("outcome %d differs: %+v vs %+v", i, g, w)
		}
		if (g.Result == nil) != (w.Result == nil) {
			t.Fatalf("outcome %d result presence differs", i)
		}
		if g.Result == nil {
			continue
		}
		a, b := g.Result, w.Result
		if a.State != b.State || a.MediumWavelength != b.MediumWavelength {
			t.Fatalf("analysis %d differs: %+v vs %+v", i, a, b)
		}
		if !ptrFloatEq(a.GuideWavelength, b.GuideWavelength) ||
			!ptrFloatEq(a.PhaseConstant, b.PhaseConstant) ||
			!ptrFloatEq(a.AttenuationConstant, b.AttenuationConstant) {
			t.Fatalf("analysis %d optional fields differ: %+v vs %+v", i, a, b)
		}
	}
}

// 回归：点名计算过的档案被删除后，再点名必须与查看档案一样回 404，
// 不允许继续拿删除前的截面出结果。
func TestCalculateAfterDeleteReturnsNotFound(t *testing.T) {
	rtr := setupRouter()
	create := map[string]any{"name": "bench-7", "broad_dimension": 0.03, "narrow_dimension": 0.01}
	if code, body := doJSON(t, rtr, http.MethodPost, "/api/v1/profiles", create); code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, body)
	}

	// 先点名算一次（旧实现正是这次计算把旧截面按名缓存了下来）。
	code, calc := doFullCalc(t, rtr, calcPath("bench-7"), modeBody(1, 0, []float64{6e9}))
	if code != http.StatusOK {
		t.Fatalf("calculate before delete: %d", code)
	}
	wantFc := physics.CutoffFrequency(0.03, 0.01, 1, 1, 1, 0)
	if calc.CutoffFrequency != wantFc {
		t.Fatalf("a=0.03 TE10 cutoff: got %v want %v", calc.CutoffFrequency, wantFc)
	}
	if got := calc.Results[0].Result.State; got != string(domain.StatePropagating) {
		t.Fatalf("6 GHz above cutoff %.6g must propagate, got %s", wantFc, got)
	}

	if code, _ := doJSON(t, rtr, http.MethodDelete, profilePath("bench-7"), nil); code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := doJSON(t, rtr, http.MethodGet, profilePath("bench-7"), nil); code != http.StatusNotFound {
		t.Fatalf("get after delete must be 404")
	}
	// 关键断言：删除返回之后再发起的点名计算同样回未找到。
	if code, calc := doFullCalc(t, rtr, calcPath("bench-7"), modeBody(1, 0, []float64{6e9})); code != http.StatusNotFound {
		t.Fatalf("calculate after delete must be 404, got %d (stale cutoff %v)", code, calc.CutoffFrequency)
	}
	// 删除不存在的名字依旧是 404（原有语义不变）。
	if code, _ := doJSON(t, rtr, http.MethodDelete, profilePath("bench-7"), nil); code != http.StatusNotFound {
		t.Fatalf("second delete must be 404")
	}
}

// 回归：同名删除重登后，点名计算必须按新登记的尺寸来——截止频率与档案视图
// 一致，传播状态与回显尺寸和同尺寸临时现算逐位相同，且响应能区分用的是
// 哪一次登记。
func TestCalculateAfterReRegisterUsesNewGeometry(t *testing.T) {
	rtr := setupRouter()
	mk := func(a float64) map[string]any {
		return map[string]any{"name": "bench-7", "broad_dimension": a, "narrow_dimension": 0.01}
	}

	// 第一步：旧尺寸登记 + 点名（旧实现由此把旧截面缓存下来）。
	code, body := doJSON(t, rtr, http.MethodPost, "/api/v1/profiles", mk(0.03))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, body)
	}
	gen1 := mustGeneration(t, body)
	if code, _ := doFullCalc(t, rtr, calcPath("bench-7"), modeBody(1, 0, []float64{6e9})); code != http.StatusOK {
		t.Fatalf("calculate with old geometry must succeed")
	}

	// 第二步：删除。
	if code, _ := doJSON(t, rtr, http.MethodDelete, profilePath("bench-7"), nil); code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}

	// 第三步：同名重登，a 改为 0.02。
	code, body = doJSON(t, rtr, http.MethodPost, "/api/v1/profiles", mk(0.02))
	if code != http.StatusCreated {
		t.Fatalf("re-register: %d %v", code, body)
	}
	gen2 := mustGeneration(t, body)
	if gen2 <= gen1 {
		t.Fatalf("re-registration must get a strictly larger generation: %d -> %d", gen1, gen2)
	}

	// 查看档案：新尺寸、新序号、TE10 截止约 7.4948 GHz。
	code, view := doJSON(t, rtr, http.MethodGet, profilePath("bench-7"), nil)
	if code != http.StatusOK {
		t.Fatalf("get re-registered: %d", code)
	}
	if got := mustGeneration(t, view); got != gen2 {
		t.Fatalf("view generation %d != register response %d", got, gen2)
	}
	geo := view["geometry"].(map[string]any)
	if geo["broad_dimension"].(float64) != 0.02 || geo["narrow_dimension"].(float64) != 0.01 {
		t.Fatalf("view must show new geometry, got %v", geo)
	}
	fcView := view["dominant_cutoff_frequency"].(float64)
	if want := physics.CutoffFrequency(0.02, 0.01, 1, 1, 1, 0); fcView != want {
		t.Fatalf("view TE10 cutoff: got %v want %v", fcView, want)
	}

	// 点名 6 GHz：必须按新尺寸算，且标明用的是第二次登记。
	code, calc := doFullCalc(t, rtr, calcPath("bench-7"), modeBody(1, 0, []float64{6e9}))
	if code != http.StatusOK {
		t.Fatalf("calculate after re-register: %d", code)
	}
	if calc.ProfileGeneration == nil || *calc.ProfileGeneration != gen2 {
		t.Fatalf("calculation must identify the registration it used: got %v want %d",
			calc.ProfileGeneration, gen2)
	}
	if calc.Geometry.BroadDimension != 0.02 || calc.Geometry.NarrowDimension != 0.01 {
		t.Fatalf("echoed geometry must be the new one, got %+v", calc.Geometry)
	}
	if calc.CutoffFrequency != fcView {
		t.Fatalf("cutoff must match profile view bit-exactly: %v vs %v", calc.CutoffFrequency, fcView)
	}

	// 对照组：同尺寸不登记直接现算，结果必须逐位相同。
	code, adhoc := doFullCalc(t, rtr, "/api/v1/calculate", map[string]any{
		"broad_dimension": 0.02, "narrow_dimension": 0.01,
		"mode": map[string]int{"m": 1, "n": 0}, "frequencies": []float64{6e9},
	})
	if code != http.StatusOK {
		t.Fatalf("ad-hoc reference: %d", code)
	}
	if adhoc.ProfileGeneration != nil {
		t.Fatalf("ad-hoc response must not carry profile_generation")
	}
	if calc.CutoffFrequency != adhoc.CutoffFrequency {
		t.Fatalf("cutoff vs ad-hoc: %v vs %v", calc.CutoffFrequency, adhoc.CutoffFrequency)
	}
	assertSameOutcomes(t, calc.Results, adhoc.Results)
	if got := calc.Results[0].Result.State; got != string(domain.StateEvanescent) {
		t.Fatalf("6 GHz below new cutoff %.6g must be evanescent, got %s", fcView, got)
	}
}

// checkCalcInvariants 校验并发场景中一次点名计算响应的不变式：
// 结果必须完整地对应该名字的某一次真实登记（geoA 或 geoB）——
// 回显截面、截止频率、逐频点状态互相一致，且携带登记序号。
func checkCalcInvariants(name string, geoA, geoB domain.Geometry, freqs []float64, calc fullCalcResponse) error {
	if calc.ProfileName == nil || *calc.ProfileName != name {
		return fmt.Errorf("profile_name mismatch: %v, want %s", calc.ProfileName, name)
	}
	if calc.ProfileGeneration == nil || *calc.ProfileGeneration == 0 {
		return fmt.Errorf("response must carry a valid profile_generation")
	}
	g := calc.Geometry
	if g != geoA && g != geoB {
		return fmt.Errorf("geometry %+v matches no real registration of %s", g, name)
	}
	wantFc := physics.CutoffFrequency(g.BroadDimension, g.NarrowDimension,
		g.RelPermittivity, g.RelPermeability, 1, 0)
	if calc.CutoffFrequency != wantFc {
		return fmt.Errorf("torn result: echoed geometry %+v but cutoff %v, want %v",
			g, calc.CutoffFrequency, wantFc)
	}
	if len(calc.Results) != len(freqs) {
		return fmt.Errorf("got %d results, want %d", len(calc.Results), len(freqs))
	}
	v := physics.MediumPhaseVelocity(g.RelPermittivity, g.RelPermeability)
	for i, f := range freqs {
		r := calc.Results[i]
		if !r.OK || r.Result == nil {
			return fmt.Errorf("freq %v not ok: %+v", f, r)
		}
		if want := string(physics.Analyze(f, wantFc, v).State); r.Result.State != want {
			return fmt.Errorf("freq %v: state %s inconsistent with echoed geometry, want %s",
				f, r.Result.State, want)
		}
	}
	return nil
}

// 回归（并发）：多路 goroutine 同时对同一批名字做删除、同名重登与连续点名
// 计算。每个名字只有一个写 goroutine，因此写者可以做确定性断言：
//   - 登记返回后立刻点名，必须看到本次登记（序号、尺寸、截止频率与视图一致）；
//   - 删除返回后立刻点名，必须 404。
//
// 读者只校验不变式：每个 200 响应都完整对应某一次真实登记，且同一读者看到的
// 登记序号不会回退。内置 WR-90 全程不动，作为哨兵验证其它名字的删改不影响它。
// 该用例必须在 -race 下运行（make test-race / make docker-test）。
func TestConcurrentDeleteReRegisterCalculate(t *testing.T) {
	rtr := setupRouter()
	const (
		nameCount      = 6
		rounds         = 20
		readersPerName = 2
	)
	freqs := []float64{5e9, 6e9, 7e9, 8e9}
	errCh := make(chan error, 1024)
	report := func(format string, args ...any) {
		select {
		case errCh <- fmt.Errorf(format, args...):
		default: // 错误已足够多，避免阻塞压测 goroutine
		}
	}
	var done atomic.Bool

	writer := func(name string, geoA, geoB domain.Geometry) {
		for k := 0; k < rounds; k++ {
			geo := geoA
			if k%2 == 1 {
				geo = geoB
			}
			code, body := doJSON(nil, rtr, http.MethodPost, "/api/v1/profiles", map[string]any{
				"name": name, "broad_dimension": geo.BroadDimension, "narrow_dimension": geo.NarrowDimension,
			})
			if code != http.StatusCreated {
				report("register %s round %d: code %d body %v", name, k, code, body)
				return
			}
			genF, ok := body["generation"].(float64)
			if !ok {
				report("register %s: view missing generation: %v", name, body)
				return
			}
			gen := uint64(genF)

			// 登记返回后立刻点名：必须看到本次登记。
			code, calc := doFullCalc(nil, rtr, calcPath(name), modeBody(1, 0, freqs))
			if code != http.StatusOK {
				report("calc %s after register round %d: code %d", name, k, code)
				return
			}
			if calc.ProfileGeneration == nil || *calc.ProfileGeneration != gen {
				report("calc %s after register: generation %v, want %d", name, calc.ProfileGeneration, gen)
				return
			}
			if calc.Geometry != geo {
				report("calc %s after register: geometry %+v, want %+v", name, calc.Geometry, geo)
				return
			}
			// 查看档案：同一个序号、同一个 TE10 截止频率。
			code, view := doJSON(nil, rtr, http.MethodGet, profilePath(name), nil)
			if code != http.StatusOK {
				report("get %s after register: code %d", name, code)
				return
			}
			if g, ok := view["generation"].(float64); !ok || uint64(g) != gen {
				report("view %s generation %v, want %d", name, view["generation"], gen)
				return
			}
			if fc, ok := view["dominant_cutoff_frequency"].(float64); !ok || fc != calc.CutoffFrequency {
				report("view %s cutoff %v != calc cutoff %v", name, view["dominant_cutoff_frequency"], calc.CutoffFrequency)
				return
			}

			// 删除返回后立刻点名：必须 404。
			if code, _ := doJSON(nil, rtr, http.MethodDelete, profilePath(name), nil); code != http.StatusOK {
				report("delete %s round %d: code %d", name, k, code)
				return
			}
			if code, _ := doFullCalc(nil, rtr, calcPath(name), modeBody(1, 0, freqs)); code != http.StatusNotFound {
				report("calc %s after delete: code %d, want 404", name, code)
				return
			}
			if code, _ := doJSON(nil, rtr, http.MethodGet, profilePath(name), nil); code != http.StatusNotFound {
				report("get %s after delete: code %d, want 404", name, code)
				return
			}
		}
	}

	reader := func(name string, geoA, geoB domain.Geometry) {
		var lastGen uint64
		for !done.Load() {
			code, calc := doFullCalc(nil, rtr, calcPath(name), modeBody(1, 0, freqs))
			if code == http.StatusNotFound {
				continue // 名字当前不存在（尚未登记或已删除），合法
			}
			if code != http.StatusOK {
				report("calc %s: unexpected code %d", name, code)
				continue
			}
			if err := checkCalcInvariants(name, geoA, geoB, freqs, calc); err != nil {
				report("calc %s: %v", name, err)
				continue
			}
			if *calc.ProfileGeneration < lastGen {
				report("calc %s: generation went backwards %d -> %d", name, lastGen, *calc.ProfileGeneration)
			}
			lastGen = *calc.ProfileGeneration
		}
	}

	// 哨兵：内置 WR-90 全程不被删改，结果必须恒定。
	wr90Fc := physics.CutoffFrequency(0.02286, 0.01016, 1, 1, 1, 0)
	sentinel := func() {
		var gen uint64
		for !done.Load() {
			code, calc := doFullCalc(nil, rtr, calcPath("WR-90"), modeBody(1, 0, freqs))
			if code != http.StatusOK {
				report("WR-90 calc: code %d", code)
				continue
			}
			if calc.CutoffFrequency != wr90Fc {
				report("WR-90 cutoff changed: %v, want %v", calc.CutoffFrequency, wr90Fc)
			}
			if calc.ProfileGeneration == nil {
				report("WR-90 calc missing profile_generation")
				continue
			}
			if gen == 0 {
				gen = *calc.ProfileGeneration
			} else if *calc.ProfileGeneration != gen {
				report("WR-90 generation changed: %d -> %d", gen, *calc.ProfileGeneration)
			}
		}
	}

	var writers, readers sync.WaitGroup
	for i := 0; i < nameCount; i++ {
		name := fmt.Sprintf("bench-%d", i)
		geoA := domain.Geometry{BroadDimension: 0.030 + float64(i)*0.001, NarrowDimension: 0.01,
			RelPermittivity: 1, RelPermeability: 1}
		geoB := domain.Geometry{BroadDimension: 0.020 + float64(i)*0.001, NarrowDimension: 0.01,
			RelPermittivity: 1, RelPermeability: 1}
		writers.Add(1)
		go func() { defer writers.Done(); writer(name, geoA, geoB) }()
		for r := 0; r < readersPerName; r++ {
			readers.Add(1)
			go func() { defer readers.Done(); reader(name, geoA, geoB) }()
		}
	}
	readers.Add(1)
	go func() { defer readers.Done(); sentinel() }()

	writers.Wait()
	done.Store(true)
	readers.Wait()

	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
