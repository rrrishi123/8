package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBudgetWorkingDir(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "Work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("EIGHT_REPO", "")
	t.Setenv("EIGHT_BUDGET_DIR", "")
	if got, err := budgetWorkingDir(); err != nil || got != home {
		t.Fatalf("standalone fallback = %q, %v; want home", got, err)
	}
	t.Setenv("EIGHT_REPO", work)
	if got, err := budgetWorkingDir(); err != nil || got != work {
		t.Fatalf("omarchy root = %q, %v", got, err)
	}
	t.Setenv("EIGHT_BUDGET_DIR", home)
	if got, err := budgetWorkingDir(); err != nil || got != home {
		t.Fatalf("override = %q, %v", got, err)
	}
	t.Setenv("EIGHT_BUDGET_DIR", filepath.Join(home, "missing"))
	if _, err := budgetWorkingDir(); err == nil {
		t.Fatal("missing explicit override accepted")
	}
	file := filepath.Join(home, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EIGHT_BUDGET_DIR", file)
	if _, err := budgetWorkingDir(); err == nil {
		t.Fatal("file accepted as working directory")
	}
}
