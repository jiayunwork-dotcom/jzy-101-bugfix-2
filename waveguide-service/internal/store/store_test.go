package store_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"waveguide-service/internal/domain"
	"waveguide-service/internal/store"
)

func sampleProfile(name string) domain.Profile {
	return domain.Profile{
		Name: name,
		Geometry: domain.Geometry{
			BroadDimension: 0.02286, NarrowDimension: 0.01016,
			RelPermittivity: 1, RelPermeability: 1,
		},
	}
}

// 内置 WR-90 样例档案可直接拿来核对。
func TestBuiltinWR90(t *testing.T) {
	s := store.NewMemoryStore()
	p, err := s.Get("WR-90")
	if err != nil {
		t.Fatalf("built-in WR-90 profile missing: %v", err)
	}
	if p.Geometry.BroadDimension != 0.02286 || p.Geometry.NarrowDimension != 0.01016 {
		t.Fatalf("WR-90 dimensions wrong: %+v", p.Geometry)
	}
	profiles := s.List()
	if len(profiles) != 1 || profiles[0].Name != "WR-90" {
		t.Fatalf("expected exactly built-in WR-90, got %+v", profiles)
	}
}

// 同名档案不能覆盖已有记录，必须返回冲突错误。
func TestCreateDuplicateRejected(t *testing.T) {
	s := store.NewMemoryStore()
	if err := s.Create(sampleProfile("X")); err != nil {
		t.Fatal(err)
	}
	err := s.Create(sampleProfile("X"))
	if !errors.Is(err, store.ErrProfileExists) {
		t.Fatalf("duplicate create must return ErrProfileExists, got %v", err)
	}
	p, _ := s.Get("X")
	if p.Geometry.RelPermittivity != 1 {
		t.Fatal("duplicate create must not overwrite existing record")
	}
}

// 删除不存在的档案必须给出明确的未找到错误。
func TestDeleteNotFound(t *testing.T) {
	s := store.NewMemoryStore()
	if err := s.Delete("ghost"); !errors.Is(err, store.ErrProfileNotFound) {
		t.Fatalf("deleting missing profile must return ErrProfileNotFound, got %v", err)
	}
	if _, err := s.Get("ghost"); !errors.Is(err, store.ErrProfileNotFound) {
		t.Fatalf("getting missing profile must return ErrProfileNotFound, got %v", err)
	}
}

func TestDeleteExisting(t *testing.T) {
	s := store.NewMemoryStore()
	if err := s.Create(sampleProfile("X")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("X"); err != nil {
		t.Fatalf("deleting existing profile: %v", err)
	}
	if _, err := s.Get("X"); !errors.Is(err, store.ErrProfileNotFound) {
		t.Fatal("profile should be gone after delete")
	}
}

// 并发场景：同名并发登记恰好成功一次；不同名档案互不串扰。
// 配合 -race 运行以检查数据竞争。
func TestConcurrentAccess(t *testing.T) {
	s := store.NewMemoryStore()
	const workers = 50
	var wg sync.WaitGroup
	var success, conflict int64
	var mu sync.Mutex

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			err := s.Create(sampleProfile("same-name"))
			mu.Lock()
			switch {
			case err == nil:
				success++
			case errors.Is(err, store.ErrProfileExists):
				conflict++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if success != 1 || conflict != workers-1 {
		t.Fatalf("exactly one create must succeed, got success=%d conflict=%d", success, conflict)
	}

	// 各自档案并发增查删，不应串名。
	var wg2 sync.WaitGroup
	wg2.Add(workers)
	for i := 0; i < workers; i++ {
		name := fmt.Sprintf("p-%d", i)
		go func() {
			defer wg2.Done()
			if err := s.Create(sampleProfile(name)); err != nil {
				t.Errorf("create %s: %v", name, err)
				return
			}
			if _, err := s.Get(name); err != nil {
				t.Errorf("get %s: %v", name, err)
			}
			if err := s.Delete(name); err != nil {
				t.Errorf("delete %s: %v", name, err)
			}
		}()
	}
	wg2.Wait()
}
