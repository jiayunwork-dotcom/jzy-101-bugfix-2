// Package validate 负责所有外部输入的合法性校验。
// 校验在任何物理计算之前完成；每个错误都携带具体字段与原因。
package validate

import (
	"fmt"
	"math"
	"strings"

	"waveguide-service/internal/domain"
)

// FieldError 描述某一项输入不合法的具体原因。
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

// Errors 一组字段校验错误，本身实现 error 接口。
type Errors []*FieldError

func (es Errors) Error() string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}

func finite(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0)
}

// Geometry 校验截面与介质参数：
// 宽边、窄边必须为正的有限值且宽边严格大于窄边；
// 相对介电常数与相对磁导率不得低于真空（>= 1）。
func Geometry(g domain.Geometry) error {
	var errs Errors

	checkDim := func(field string, v float64) {
		switch {
		case !finite(v):
			errs = append(errs, &FieldError{field, "必须是有限数值"})
		case v <= 0:
			errs = append(errs, &FieldError{field, "必须为正数"})
		}
	}
	checkDim("broad_dimension", g.BroadDimension)
	checkDim("narrow_dimension", g.NarrowDimension)

	if finite(g.BroadDimension) && finite(g.NarrowDimension) &&
		g.BroadDimension > 0 && g.NarrowDimension > 0 &&
		g.BroadDimension <= g.NarrowDimension {
		errs = append(errs, &FieldError{"broad_dimension", "宽边必须严格大于窄边"})
	}
	if !finite(g.RelPermittivity) || g.RelPermittivity < 1 {
		errs = append(errs, &FieldError{"rel_permittivity", "相对介电常数不能低于真空（必须为 >= 1 的有限值）"})
	}
	if !finite(g.RelPermeability) || g.RelPermeability < 1 {
		errs = append(errs, &FieldError{"rel_permeability", "相对磁导率不能低于真空（必须为 >= 1 的有限值）"})
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// Mode 校验模式指数：m、n 为非负整数，且 (0,0) 组合物理上不存在，必须拦截。
func Mode(m domain.Mode) error {
	var errs Errors
	if m.M < 0 {
		errs = append(errs, &FieldError{"mode.m", "模式指数不能为负"})
	}
	if m.N < 0 {
		errs = append(errs, &FieldError{"mode.n", "模式指数不能为负"})
	}
	if m.M == 0 && m.N == 0 {
		errs = append(errs, &FieldError{"mode", "m 与 n 不能同时为零：(0,0) 模在物理上不存在"})
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// ProfileName 校验档案名非空。
func ProfileName(name string) error {
	if strings.TrimSpace(name) == "" {
		return Errors{&FieldError{"name", "档案名不能为空"}}
	}
	return nil
}

// Frequency 校验单个频率：必须为正的有限值。
func Frequency(f float64) error {
	switch {
	case !finite(f):
		return Errors{&FieldError{"frequency", "必须是有限数值"}}
	case f <= 0:
		return Errors{&FieldError{"frequency", "频率必须为正数"}}
	}
	return nil
}
