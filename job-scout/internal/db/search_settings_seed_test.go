package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
)

func TestSeedSearchSettings_DoesNotOverwriteLiveRow(t *testing.T) {
	pool := pgtestOpenMigrated(t)
	ctx := context.Background()
	if err := SeedSettings(ctx, pool); err != nil {
		t.Fatalf("seed: %v", err)
	}

	first, err := GetSearchSettings(ctx, pool)
	if err != nil || first == nil {
		t.Fatalf("GetSearchSettings after seed: %v %#v", err, first)
	}
	if len(first.TitleInclude) == 0 {
		t.Fatal("seeded title_include must not be empty")
	}

	if _, err := pool.Exec(`UPDATE search_settings SET title_include = '["ZZZ-live"]'`); err != nil {
		t.Fatalf("operator SQL edit: %v", err)
	}

	if err := SeedSettings(ctx, pool); err != nil {
		t.Fatalf("second seed: %v", err)
	}

	got, err := GetSearchSettings(ctx, pool)
	if err != nil || got == nil {
		t.Fatalf("GetSearchSettings after second seed: %v %#v", err, got)
	}
	if len(got.TitleInclude) != 1 || got.TitleInclude[0] != "ZZZ-live" {
		t.Errorf("search_settings seed-once overwrote live row, title_include=%v", got.TitleInclude)
	}
}

func TestGetSearchSettings_ReturnsCurrentDatabaseLists(t *testing.T) {
	pool := pgtestOpenMigrated(t)
	ctx := context.Background()
	if err := SeedSettings(ctx, pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	raw, err := json.Marshal([]string{"only-from-db"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := pool.Exec(`UPDATE search_settings SET company_exclude = $1::json`, raw); err != nil {
		t.Fatalf("update company_exclude: %v", err)
	}

	got, err := GetSearchSettings(ctx, pool)
	if err != nil || got == nil {
		t.Fatalf("GetSearchSettings: %v %#v", err, got)
	}
	if len(got.CompanyExclude) != 1 || got.CompanyExclude[0] != "only-from-db" {
		t.Errorf("Notify must load filter lists from DB, company_exclude=%v", got.CompanyExclude)
	}
}

func pgtestOpenMigrated(t *testing.T) *sql.DB {
	t.Helper()
	return migratedPool(t)
}
