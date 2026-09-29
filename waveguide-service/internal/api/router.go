// Package api HTTP 接口层（Gin）：只负责请求解析、参数绑定与错误映射，
// 不包含任何物理公式。每个请求都在无状态 handler 中现场组装数据，
// 不持有任何按客户端区分的可变状态，天然支持多客户端并发访问。
package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"waveguide-service/internal/domain"
	"waveguide-service/internal/service"
	"waveguide-service/internal/store"
	"waveguide-service/internal/validate"
)

// NewRouter 构建 Gin 引擎并注册全部路由。
func NewRouter(svc *service.Service) *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	h := &handler{svc: svc}

	r.GET("/healthz", h.health)

	v1 := r.Group("/api/v1")
	{
		v1.GET("/profiles", h.listProfiles)
		v1.POST("/profiles", h.createProfile)
		v1.GET("/profiles/:name", h.getProfile)
		v1.DELETE("/profiles/:name", h.deleteProfile)
		v1.POST("/profiles/:name/calculate", h.calculateWithProfile)
		v1.POST("/calculate", h.calculateAdHoc)
	}
	return r
}

type handler struct {
	svc *service.Service
}

func (h *handler) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ---- 请求体定义 ----

// 所有尺寸字段用指针接收，以便区分"未提供"（绑定阶段报 400）
// 与"提供了 0"（进入业务校验后给出具体原因）。

type createProfileRequest struct {
	Name            string   `json:"name" binding:"required"`
	BroadDimension  *float64 `json:"broad_dimension" binding:"required"`
	NarrowDimension *float64 `json:"narrow_dimension" binding:"required"`
	RelPermittivity *float64 `json:"rel_permittivity"`
	RelPermeability *float64 `json:"rel_permeability"`
}

type modeRequest struct {
	M *int `json:"m" binding:"required"`
	N *int `json:"n" binding:"required"`
}

// calculateRequest 点名档案计算；同时支持单个 frequency 与批量 frequencies。
type calculateRequest struct {
	Mode        modeRequest `json:"mode" binding:"required"`
	Frequency   *float64    `json:"frequency"`
	Frequencies []float64   `json:"frequencies"`
}

// adHocRequest 临时提交计算：自带截面与介质参数。
type adHocRequest struct {
	BroadDimension  *float64    `json:"broad_dimension" binding:"required"`
	NarrowDimension *float64    `json:"narrow_dimension" binding:"required"`
	RelPermittivity *float64    `json:"rel_permittivity"`
	RelPermeability *float64    `json:"rel_permeability"`
	Mode            modeRequest `json:"mode" binding:"required"`
	Frequency       *float64    `json:"frequency"`
	Frequencies     []float64   `json:"frequencies"`
}

// ---- 辅助函数 ----

func bindJSON(c *gin.Context, obj any) bool {
	if err := c.ShouldBindJSON(obj); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return false
	}
	return true
}

// collectFrequencies 汇总单个与批量频率字段，至少要提供一个频率点。
func collectFrequencies(single *float64, list []float64) ([]float64, error) {
	freqs := append([]float64{}, list...)
	if single != nil {
		freqs = append(freqs, *single)
	}
	if len(freqs) == 0 {
		return nil, errors.New("必须提供 frequency 或 frequencies 中的至少一个频率点")
	}
	return freqs, nil
}

func geometryFrom(broad, narrow, er, ur *float64) domain.Geometry {
	g := domain.Geometry{
		BroadDimension:  *broad,
		NarrowDimension: *narrow,
		RelPermittivity: 1.0,
		RelPermeability: 1.0,
	}
	if er != nil {
		g.RelPermittivity = *er
	}
	if ur != nil {
		g.RelPermeability = *ur
	}
	return g
}

// respondError 把业务层错误映射为合适的 HTTP 状态码。
func respondError(c *gin.Context, err error) {
	var ferr validate.Errors
	switch {
	case errors.As(err, &ferr):
		// 400：输入越界（宽边不大于窄边、介电常数低于真空、(0,0) 模等），
		// 逐项给出字段与原因。
		c.JSON(http.StatusBadRequest, gin.H{"error": "validation failed", "details": ferr})
	case errors.Is(err, store.ErrProfileExists):
		c.JSON(http.StatusConflict, gin.H{"error": "档案名已存在，拒绝覆盖: " + err.Error()})
	case errors.Is(err, store.ErrProfileNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "未找到指定档案: " + err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

// ---- 参数档管理 ----

func (h *handler) createProfile(c *gin.Context) {
	var req createProfileRequest
	if !bindJSON(c, &req) {
		return
	}
	p := domain.Profile{
		Name:     req.Name,
		Geometry: geometryFrom(req.BroadDimension, req.NarrowDimension, req.RelPermittivity, req.RelPermeability),
	}
	view, err := h.svc.RegisterProfile(p)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, view)
}

func (h *handler) listProfiles(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"profiles": h.svc.ListProfiles()})
}

func (h *handler) getProfile(c *gin.Context) {
	view, err := h.svc.GetProfile(c.Param("name"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

func (h *handler) deleteProfile(c *gin.Context) {
	if err := h.svc.DeleteProfile(c.Param("name")); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": c.Param("name")})
}

// ---- 计算查询 ----

func (h *handler) calculateWithProfile(c *gin.Context) {
	var req calculateRequest
	if !bindJSON(c, &req) {
		return
	}
	freqs, err := collectFrequencies(req.Frequency, req.Frequencies)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	mode := domain.Mode{M: *req.Mode.M, N: *req.Mode.N}
	calc, err := h.svc.CalculateWithProfile(c.Param("name"), mode, freqs)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, calc)
}

func (h *handler) calculateAdHoc(c *gin.Context) {
	var req adHocRequest
	if !bindJSON(c, &req) {
		return
	}
	freqs, err := collectFrequencies(req.Frequency, req.Frequencies)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	g := geometryFrom(req.BroadDimension, req.NarrowDimension, req.RelPermittivity, req.RelPermeability)
	mode := domain.Mode{M: *req.Mode.M, N: *req.Mode.N}
	calc, err := h.svc.CalculateAdHoc(g, mode, freqs)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, calc)
}
