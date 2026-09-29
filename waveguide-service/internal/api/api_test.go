package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"waveguide-service/internal/api"
	"waveguide-service/internal/service"
	"waveguide-service/internal/store"
)

func setupRouter() http.Handler {
	gin.SetMode(gin.TestMode)
	return api.NewRouter(service.New(store.NewMemoryStore()))
}

func fail(t *testing.T, format string, args ...any) {
	if t != nil {
		t.Helper()
		t.Fatalf(format, args...)
	}
	panic(fmt.Sprintf(format, args...))
}

func doJSON(t *testing.T, rtr http.Handler, method, path string, body any) (int, map[string]any) {
	if t != nil {
		t.Helper()
	}
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			fail(t, "marshal request: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, req)

	var decoded map[string]any
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
			fail(t, "non-JSON response %q: %v", w.Body.String(), err)
		}
	}
	return w.Code, decoded
}

func TestHealthAndBuiltinProfile(t *testing.T) {
	rtr := setupRouter()
	if code, body := doJSON(t, rtr, http.MethodGet, "/healthz", nil); code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("health: code=%d body=%v", code, body)
	}
	code, body := doJSON(t, rtr, http.MethodGet, "/api/v1/profiles", nil)
	if code != http.StatusOK {
		t.Fatalf("list profiles: %d %v", code, body)
	}
	profiles := body["profiles"].([]any)
	if len(profiles) != 1 || profiles[0].(map[string]any)["name"] != "WR-90" {
		t.Fatalf("expected built-in WR-90 only, got %v", profiles)
	}
}

func TestProfileLifecycle(t *testing.T) {
	rtr := setupRouter()
	create := map[string]any{
		"name": "custom-1", "broad_dimension": 0.03, "narrow_dimension": 0.015,
	}
	code, body := doJSON(t, rtr, http.MethodPost, "/api/v1/profiles", create)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, body)
	}
	if fc := body["dominant_cutoff_frequency"].(float64); fc < 4.99e9 || fc > 5.01e9 {
		t.Fatalf("a=30mm TE10 cutoff ~5 GHz, got %v", fc)
	}

	// 重名 => 409 冲突，且不覆盖。
	if code, body = doJSON(t, rtr, http.MethodPost, "/api/v1/profiles", create); code != http.StatusConflict {
		t.Fatalf("duplicate must be 409, got %d %v", code, body)
	}

	// 非法几何（宽边不大于窄边）=> 400，带字段级原因。
	bad := map[string]any{"name": "bad", "broad_dimension": 0.01, "narrow_dimension": 0.02}
	if code, body = doJSON(t, rtr, http.MethodPost, "/api/v1/profiles", bad); code != http.StatusBadRequest {
		t.Fatalf("bad geometry must be 400, got %d %v", code, body)
	}
	details, ok := body["details"].([]any)
	if !ok || len(details) == 0 {
		t.Fatalf("validation response must contain field details, got %v", body)
	}

	// 缺字段 => 400。
	if code, _ = doJSON(t, rtr, http.MethodPost, "/api/v1/profiles", map[string]any{"name": "x"}); code != http.StatusBadRequest {
		t.Fatalf("missing dimensions must be 400, got %d", code)
	}

	// εr < 1 => 400。
	erBad := map[string]any{"name": "er-bad", "broad_dimension": 0.03, "narrow_dimension": 0.015, "rel_permittivity": 0.5}
	if code, _ = doJSON(t, rtr, http.MethodPost, "/api/v1/profiles", erBad); code != http.StatusBadRequest {
		t.Fatalf("εr<1 must be 400, got %d", code)
	}

	// 查询 / 删除 / 删除不存在。
	if code, body = doJSON(t, rtr, http.MethodGet, "/api/v1/profiles/custom-1", nil); code != http.StatusOK || body["name"] != "custom-1" {
		t.Fatalf("get profile: %d %v", code, body)
	}
	if code, _ = doJSON(t, rtr, http.MethodGet, "/api/v1/profiles/ghost", nil); code != http.StatusNotFound {
		t.Fatalf("missing profile must be 404, got %d", code)
	}
	if code, _ = doJSON(t, rtr, http.MethodDelete, "/api/v1/profiles/ghost", nil); code != http.StatusNotFound {
		t.Fatalf("delete missing must be 404, got %d", code)
	}
	if code, _ = doJSON(t, rtr, http.MethodDelete, "/api/v1/profiles/custom-1", nil); code != http.StatusOK {
		t.Fatalf("delete existing must be 200, got %d", code)
	}
	if code, _ = doJSON(t, rtr, http.MethodGet, "/api/v1/profiles/custom-1", nil); code != http.StatusNotFound {
		t.Fatalf("deleted profile must be gone, got %d", code)
	}
}

// 定义与服务 JSON 对齐的强类型响应，便于断言可选字段是否存在。
type freqResult struct {
	Frequency float64 `json:"frequency"`
	OK        bool    `json:"ok"`
	Error     string  `json:"error"`
	Result    *struct {
		State               string   `json:"state"`
		MediumWavelength    float64  `json:"medium_wavelength"`
		GuideWavelength     *float64 `json:"guide_wavelength"`
		PhaseConstant       *float64 `json:"phase_constant"`
		AttenuationConstant *float64 `json:"attenuation_constant"`
	} `json:"result"`
}

type calcResponse struct {
	CutoffFrequency float64      `json:"cutoff_frequency"`
	ProfileName     *string      `json:"profile_name"`
	Results         []freqResult `json:"results"`
}

func doCalc(t *testing.T, rtr http.Handler, path string, body map[string]any) (int, calcResponse) {
	if t != nil {
		t.Helper()
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, req)
	var calc calcResponse
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &calc)
	}
	return w.Code, calc
}

func modeBody(m, n int, freqs []float64) map[string]any {
	return map[string]any{"mode": map[string]int{"m": m, "n": n}, "frequencies": freqs}
}

// 点名档案计算：传导 / 渐逝 / 临界三种状态的字段形状。
func TestCalculateWithProfileStates(t *testing.T) {
	rtr := setupRouter()

	// 先拿到 WR-90 主模截止频率。
	code, profileBody := doJSON(t, rtr, http.MethodGet, "/api/v1/profiles/WR-90", nil)
	if code != http.StatusOK {
		t.Fatalf("get WR-90: %d", code)
	}
	fc := profileBody["dominant_cutoff_frequency"].(float64)

	body := modeBody(1, 0, []float64{10e9, 5e9, fc})
	code, calc := doCalc(t, rtr, "/api/v1/profiles/WR-90/calculate", body)
	if code != http.StatusOK {
		t.Fatalf("calculate: %d", code)
	}
	if len(calc.Results) != 3 {
		t.Fatalf("want 3 results, got %d", len(calc.Results))
	}
	if calc.CutoffFrequency != fc {
		t.Fatalf("cutoff echo mismatch: %v vs %v", calc.CutoffFrequency, fc)
	}

	// 10 GHz：传导，有波导波长，无衰减常数。
	r0 := calc.Results[0]
	if !r0.OK || r0.Result.State != "propagating" || r0.Result.GuideWavelength == nil {
		t.Fatalf("10 GHz must propagate with guide wavelength: %+v", r0)
	}
	if r0.Result.AttenuationConstant != nil {
		t.Fatalf("propagating result must omit attenuation constant")
	}

	// 5 GHz：渐逝，有衰减常数，绝不能出现波导波长键。
	r1 := calc.Results[1]
	if !r1.OK || r1.Result.State != "evanescent" || r1.Result.AttenuationConstant == nil {
		t.Fatalf("5 GHz must be evanescent with attenuation: %+v", r1)
	}
	if r1.Result.GuideWavelength != nil || r1.Result.PhaseConstant != nil {
		t.Fatalf("evanescent result must omit guide wavelength/phase constant: %+v", r1.Result)
	}

	// f == fc：临界，无波导波长，β/α 为 0。
	r2 := calc.Results[2]
	if !r2.OK || r2.Result.State != "critical" {
		t.Fatalf("f == fc must be critical: %+v", r2)
	}
	if r2.Result.GuideWavelength != nil {
		t.Fatalf("critical must not carry guide wavelength: %v", *r2.Result.GuideWavelength)
	}
	if r2.Result.PhaseConstant == nil || *r2.Result.PhaseConstant != 0 {
		t.Fatalf("critical β must be 0: %+v", r2.Result)
	}
	if r2.Result.AttenuationConstant == nil || *r2.Result.AttenuationConstant != 0 {
		t.Fatalf("critical α must be 0: %+v", r2.Result)
	}
}

// 批量：一个频率非法不拖累其它点。
func TestBatchPartialInvalid(t *testing.T) {
	rtr := setupRouter()
	body := modeBody(1, 0, []float64{10e9, -7, 8e9})
	code, calc := doCalc(t, rtr, "/api/v1/profiles/WR-90/calculate", body)
	if code != http.StatusOK {
		t.Fatalf("batch request should be accepted: %d", code)
	}
	if calc.Results[0].OK != true || calc.Results[2].OK != true {
		t.Fatalf("valid points must still succeed: %+v", calc.Results)
	}
	if calc.Results[1].OK || calc.Results[1].Error == "" {
		t.Fatalf("negative frequency point must fail with reason: %+v", calc.Results[1])
	}
}

// 单个 frequency 字段与批量 frequencies 均可用。
func TestSingleFrequencyField(t *testing.T) {
	rtr := setupRouter()
	body := map[string]any{"mode": map[string]int{"m": 1, "n": 0}, "frequency": 10e9}
	code, calc := doCalc(t, rtr, "/api/v1/profiles/WR-90/calculate", body)
	if code != http.StatusOK || len(calc.Results) != 1 || !calc.Results[0].OK {
		t.Fatalf("single frequency query failed: %d %+v", code, calc.Results)
	}
	// 两个频率字段都不给 => 400。
	code, _ = doCalc(t, rtr, "/api/v1/profiles/WR-90/calculate",
		map[string]any{"mode": map[string]int{"m": 1, "n": 0}})
	if code != http.StatusBadRequest {
		t.Fatalf("missing frequency must be 400, got %d", code)
	}
}

// 非法模式（0,0）与不存在档案。
func TestCalculateRejections(t *testing.T) {
	rtr := setupRouter()
	if code, _ := doCalc(t, rtr, "/api/v1/profiles/WR-90/calculate", modeBody(0, 0, []float64{10e9})); code != http.StatusBadRequest {
		t.Fatalf("(0,0) mode must be 400, got %d", code)
	}
	if code, _ := doCalc(t, rtr, "/api/v1/profiles/ghost/calculate", modeBody(1, 0, []float64{10e9})); code != http.StatusNotFound {
		t.Fatalf("unknown profile must be 404, got %d", code)
	}
}

// 临时提交路径与档案路径给出同一套结果；非法几何 400。
func TestAdHocPath(t *testing.T) {
	rtr := setupRouter()
	adhoc := map[string]any{
		"broad_dimension": 0.02286, "narrow_dimension": 0.01016,
		"mode": map[string]int{"m": 1, "n": 0}, "frequencies": []float64{10e9, 5e9},
	}
	code, calcA := doCalc(t, rtr, "/api/v1/calculate", adhoc)
	if code != http.StatusOK {
		t.Fatalf("ad-hoc calc: %d", code)
	}
	if calcA.ProfileName != nil {
		t.Fatal("ad-hoc response must not carry a profile name")
	}
	_, calcP := doCalc(t, rtr, "/api/v1/profiles/WR-90/calculate", modeBody(1, 0, []float64{10e9, 5e9}))
	if calcA.CutoffFrequency != calcP.CutoffFrequency {
		t.Fatalf("cutoff must match across paths: %v vs %v", calcA.CutoffFrequency, calcP.CutoffFrequency)
	}
	for i := range calcA.Results {
		a, b := calcA.Results[i], calcP.Results[i]
		if a.Result.State != b.Result.State {
			t.Fatalf("state at point %d differs: %s vs %s", i, a.Result.State, b.Result.State)
		}
	}

	// εr=4：截止频率减半。
	er4 := map[string]any{
		"broad_dimension": 0.02286, "narrow_dimension": 0.01016, "rel_permittivity": 4,
		"mode": map[string]int{"m": 1, "n": 0}, "frequency": 10e9,
	}
	_, calc4 := doCalc(t, rtr, "/api/v1/calculate", er4)
	if calc4.CutoffFrequency != calcA.CutoffFrequency/2 {
		t.Fatalf("εr=4 cutoff %v should be half of %v", calc4.CutoffFrequency, calcA.CutoffFrequency)
	}

	bad := map[string]any{
		"broad_dimension": 0.01, "narrow_dimension": 0.02,
		"mode": map[string]int{"m": 1, "n": 0}, "frequency": 10e9,
	}
	if code, _ := doCalc(t, rtr, "/api/v1/calculate", bad); code != http.StatusBadRequest {
		t.Fatalf("ad-hoc bad geometry must be 400, got %d", code)
	}
}

// 并发请求冒烟：大量并发查询/临时计算/增删，状态不串客户端。
func TestConcurrentRequests(t *testing.T) {
	rtr := setupRouter()
	var wg sync.WaitGroup
	errCh := make(chan error, 300)
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("p-%d", i)
			code, _ := doJSON(nil, rtr, http.MethodPost, "/api/v1/profiles", map[string]any{
				"name": name, "broad_dimension": 0.03 + float64(i)*1e-9, "narrow_dimension": 0.01,
			})
			if code != http.StatusCreated {
				errCh <- fmt.Errorf("create %s: %d", name, code)
			}
		}(i)
		go func() {
			defer wg.Done()
			code, calc := doCalc(nil, rtr, "/api/v1/profiles/WR-90/calculate",
				modeBody(1, 0, []float64{8e9, 10e9, 12e9}))
			if code != http.StatusOK || len(calc.Results) != 3 {
				errCh <- fmt.Errorf("profile calc: code=%d n=%d", code, len(calc.Results))
			}
		}()
		go func(i int) {
			defer wg.Done()
			code, _ := doCalc(nil, rtr, "/api/v1/calculate", map[string]any{
				"broad_dimension": 0.03 + float64(i)*1e-9, "narrow_dimension": 0.01,
				"mode": map[string]int{"m": 1, "n": 0}, "frequency": 11e9,
			})
			if code != http.StatusOK {
				errCh <- fmt.Errorf("ad-hoc calc %d: %d", i, code)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
