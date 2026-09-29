package validate_test

import (
	"strings"
	"testing"

	"waveguide-service/internal/domain"
	"waveguide-service/internal/validate"
)

func TestGeometry(t *testing.T) {
	valid := domain.Geometry{BroadDimension: 0.02286, NarrowDimension: 0.01016, RelPermittivity: 1, RelPermeability: 1}
	cases := []struct {
		name    string
		g       domain.Geometry
		wantMsg string
	}{
		{"negative broad", domain.Geometry{BroadDimension: -1, NarrowDimension: 0.01}, "broad_dimension"},
		{"zero narrow", domain.Geometry{BroadDimension: 0.02, NarrowDimension: 0}, "narrow_dimension"},
		{"broad equal narrow", domain.Geometry{BroadDimension: 0.01, NarrowDimension: 0.01}, "宽边必须严格大于窄边"},
		{"broad smaller than narrow", domain.Geometry{BroadDimension: 0.01, NarrowDimension: 0.02}, "宽边必须严格大于窄边"},
		{"er below vacuum", domain.Geometry{BroadDimension: 0.02, NarrowDimension: 0.01, RelPermittivity: 0.9}, "rel_permittivity"},
		{"er zero", domain.Geometry{BroadDimension: 0.02, NarrowDimension: 0.01, RelPermittivity: 0}, "rel_permittivity"},
		{"ur below vacuum", domain.Geometry{BroadDimension: 0.02, NarrowDimension: 0.01, RelPermittivity: 1, RelPermeability: 0.5}, "rel_permeability"},
	}
	if err := validate.Geometry(valid); err != nil {
		t.Fatalf("valid geometry rejected: %v", err)
	}
	// εr=4 的介质也合法。
	if err := validate.Geometry(domain.Geometry{BroadDimension: 0.02, NarrowDimension: 0.01, RelPermittivity: 4, RelPermeability: 1}); err != nil {
		t.Fatalf("er=4 should be valid: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validate.Geometry(tc.g)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.wantMsg)
			}
		})
	}
}

func TestMode(t *testing.T) {
	for _, m := range []domain.Mode{{M: 1, N: 0}, {M: 0, N: 1}, {M: 2, N: 3}} {
		if err := validate.Mode(m); err != nil {
			t.Fatalf("mode %+v should be valid: %v", m, err)
		}
	}
	for _, m := range []domain.Mode{{M: 0, N: 0}, {M: -1, N: 0}, {M: 0, N: -1}, {M: -1, N: -1}} {
		if err := validate.Mode(m); err == nil {
			t.Fatalf("mode %+v should be rejected", m)
		}
	}
	err := validate.Mode(domain.Mode{M: 0, N: 0})
	if err == nil {
		t.Fatal("(0,0) mode must be rejected")
	}
	if !strings.Contains(err.Error(), "(0,0)") {
		t.Fatalf("(0,0) rejection should state physical nonexistence, got %v", err)
	}
}

func TestFrequencyAndName(t *testing.T) {
	for _, f := range []float64{0, -1, -1e9} {
		if err := validate.Frequency(f); err == nil {
			t.Fatalf("frequency %v should be rejected", f)
		}
	}
	if err := validate.Frequency(10e9); err != nil {
		t.Fatalf("positive frequency rejected: %v", err)
	}
	if err := validate.ProfileName("   "); err == nil {
		t.Fatal("blank name should be rejected")
	}
	if err := validate.ProfileName("WR-90"); err != nil {
		t.Fatalf("valid name rejected: %v", err)
	}
}
