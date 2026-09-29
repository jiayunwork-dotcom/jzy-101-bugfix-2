package store_test

import (
	"errors"
	"testing"

	"waveguide-service/internal/store"
)

// 登记序号：每次成功登记得到全局更大的序号；同名删除重登后序号严格增大；
// 通过 Get 取回的记录携带与登记时一致的序号。
func TestRegistrationGeneration(t *testing.T) {
	s := store.NewMemoryStore()

	builtin, err := s.Get("WR-90")
	if err != nil {
		t.Fatalf("built-in WR-90 missing: %v", err)
	}
	if builtin.Generation == 0 {
		t.Fatal("built-in profile must carry a non-zero generation")
	}

	r1, err := s.Register(sampleProfile("g-1"))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := s.Register(sampleProfile("g-2"))
	if err != nil {
		t.Fatal(err)
	}
	if r2.Generation <= r1.Generation {
		t.Fatalf("generations must increase: %d -> %d", r1.Generation, r2.Generation)
	}

	// 删除重登：新记录序号严格大于旧记录。
	if err := s.Delete("g-1"); err != nil {
		t.Fatal(err)
	}
	r3, err := s.Register(sampleProfile("g-1"))
	if err != nil {
		t.Fatal(err)
	}
	if r3.Generation <= r1.Generation {
		t.Fatalf("re-registration must get a larger generation: %d -> %d", r1.Generation, r3.Generation)
	}
	got, err := s.Get("g-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Generation != r3.Generation {
		t.Fatalf("Get must return the record registered last: gen %d, want %d", got.Generation, r3.Generation)
	}

	// 重名冲突：不覆盖原记录，序号不变。
	if _, err := s.Register(sampleProfile("g-1")); !errors.Is(err, store.ErrProfileExists) {
		t.Fatalf("duplicate register must conflict, got %v", err)
	}
	again, err := s.Get("g-1")
	if err != nil {
		t.Fatal(err)
	}
	if again.Generation != r3.Generation {
		t.Fatalf("conflict must not touch existing record: gen %d, want %d", again.Generation, r3.Generation)
	}

	// 兼容方法 Create 仍可用（只登记、不返回记录）。
	if err := s.Create(sampleProfile("legacy")); err != nil {
		t.Fatalf("compat Create: %v", err)
	}
	if _, err := s.Get("legacy"); err != nil {
		t.Fatalf("profile registered via Create must be retrievable: %v", err)
	}
}
