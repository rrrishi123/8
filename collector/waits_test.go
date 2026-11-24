package main

import (
	"errors"
	"testing"
	"time"
)

func TestEvalPredicateWork(t *testing.T) {
	since := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	items := []workItem{
		{ID: 1, Text: "old merged note v0.0.5", Status: "done", TS: "2026-11-24T11:00:00Z", By: "x"},
		{ID: 2, Text: "cut v0.0.5 on all arms", Status: "done", TS: "2026-11-24T13:00:00Z", By: "conductor", FlippedBy: "conductor"},
		{ID: 3, Text: "still todo", Status: "todo", TS: "2026-11-24T13:30:00Z"},
	}
	if ok, ev := evalPredicate(waitPredicate{Kind: "work_done", WorkID: 2}, since, items); !ok || ev == "" {
		t.Fatal("work_done #2 should fire")
	}
	if ok, _ := evalPredicate(waitPredicate{Kind: "work_done", WorkID: 3}, since, items); ok {
		t.Fatal("todo item must not fire")
	}
	if ok, _ := evalPredicate(waitPredicate{Kind: "work_done", WorkID: 99}, since, items); ok {
		t.Fatal("missing item must not fire")
	}
	if ok, _ := evalPredicate(waitPredicate{Kind: "work_match", Regex: `v0\.0\.5`}, since, items); !ok {
		t.Fatal("work_match should fire on #2 (newer than since)")
	}
	if ok, _ := evalPredicate(waitPredicate{Kind: "work_match", Regex: `old merged`}, since, items); ok {
		t.Fatal("work_match must ignore items older than since")
	}
	if ok, _ := evalPredicate(waitPredicate{Kind: "work_match", Regex: `(`}, since, items); ok {
		t.Fatal("bad regex never fires")
	}
	if ok, _ := evalPredicate(waitPredicate{Kind: "nope"}, since, items); ok {
		t.Fatal("unknown kind never fires")
	}
}

func TestEvalPredicateGit(t *testing.T) {
	orig := gitLsRemote
	defer func() { gitLsRemote = orig }()
	calls := 0
	gitLsRemote = func(repo, ref string) (string, error) {
		calls++
		switch {
		case repo == "8" && ref == "refs/tags/collector/v0.0.5":
			return "abc123def\trefs/tags/collector/v0.0.5", nil
		case repo == "8" && ref == "refs/heads/next":
			return "9907253aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\trefs/heads/next", nil
		case repo == "pilot" && ref == "refs/heads/next":
			return "deadbeef00000000000000000000000000000000\trefs/heads/next", nil
		}
		return "", errors.New("no such ref")
	}
	since := time.Now()
	if ok, ev := evalPredicate(waitPredicate{Kind: "git_ref", Repo: "8", Ref: "refs/tags/collector/v0.0.5"}, since, nil); !ok || ev == "" {
		t.Fatal("git_ref should fire when the ref exists")
	}
	if ok, _ := evalPredicate(waitPredicate{Kind: "git_ref", Repo: "8", Ref: "refs/tags/v9"}, since, nil); ok {
		t.Fatal("missing ref must not fire")
	}
	if ok, _ := evalPredicate(waitPredicate{Kind: "git_ref", Repo: "../etc", Ref: "refs/heads/x"}, since, nil); ok {
		t.Fatal("unknown repo must never be consulted")
	}
	if ok, _ := evalPredicate(waitPredicate{Kind: "git_tip", Repo: "8", Branch: "next", Was: "9907253"}, since, nil); ok {
		t.Fatal("unchanged tip must not fire")
	}
	if ok, ev := evalPredicate(waitPredicate{Kind: "git_tip", Repo: "pilot", Branch: "next", Was: "96d4c2b"}, since, nil); !ok || ev == "" {
		t.Fatal("moved tip should fire")
	}
	if ok, _ := evalPredicate(waitPredicate{Kind: "git_tip", Repo: "8", Branch: "next"}, since, nil); ok {
		t.Fatal("git_tip without `was` has nothing to compare and must not fire")
	}
	if calls == 0 {
		t.Fatal("oracle not consulted")
	}
}

func TestWaitSummaryAndFired(t *testing.T) {
	w := waitDecl{Pane: "%8", Since: "2026-11-24T16:00:00Z", Question: "v0.0.5 cut?",
		Predicates: []waitPredicate{{ID: "tag", Kind: "git_ref"}, {ID: "tip", Kind: "git_tip"}},
		Fired:      []waitFired{{ID: "tag", At: "x", Evidence: "y"}}}
	if !w.alreadyFired("tag") || w.alreadyFired("tip") {
		t.Fatal("fired bookkeeping")
	}
	if s := w.summary(); s != "v0.0.5 cut? (1 predicate open since 2026-11-24)" {
		t.Fatalf("summary = %q", s)
	}
	w.SatisfiedAt = "2026-11-25T00:00:00Z"
	if s := w.summary(); s[:9] != "satisfied" {
		t.Fatalf("satisfied summary = %q", s)
	}
}
