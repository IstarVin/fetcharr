package store

import (
	"testing"

	"github.com/istarvin/fetcharr/server/internal/jobs"
)

func TestListQuery(t *testing.T) {
	s, _ := openTemp(t)
	a := newJob("https://a")
	a.Title = Ptr("Dune Part Two")
	b := newJob("https://b")
	b.Title, b.ShowName = Ptr("Hello, Ms. Cobel"), Ptr("Severance")
	c := newJob("https://c")
	c.Title = Ptr("100% Wolf_2")
	for _, j := range []*Job{a, b, c} {
		mustInsert(t, s, j)
	}
	cases := map[string][]string{
		"dune":      {a.ID},
		"SEVERANCE": {b.ID},
		"%":         {c.ID},
		"_":         {c.ID},
		"  ":        {c.ID, b.ID, a.ID},
		"nothing":   {},
	}
	for q, want := range cases {
		got, total, err := s.List(ctx, ListFilter{Query: q})
		if err != nil {
			t.Fatal(err)
		}
		if total != len(want) || len(got) != len(want) {
			t.Fatalf("query %q: got %d jobs (total %d), want %d", q, len(got), total, len(want))
		}
		for i := range want {
			if got[i].ID != want[i] {
				t.Errorf("query %q: job %d = %s, want %s", q, i, got[i].ID, want[i])
			}
		}
	}
}

func TestMoveToFront(t *testing.T) {
	s, _ := openTemp(t)
	j1 := mustInsert(t, s, newJob("https://1"))
	j2 := mustInsert(t, s, newJob("https://2"))
	j3 := mustInsert(t, s, newJob("https://3"))
	paused := newJob("https://4")
	paused.Status = jobs.StatusPaused
	mustInsert(t, s, paused)

	moved, err := s.MoveToFront(ctx, []string{j3.ID, j2.ID, paused.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 2 {
		t.Fatalf("moved %d jobs, want 2 (paused is skipped)", len(moved))
	}
	order, err := s.PendingJobs(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, j := range order {
		ids = append(ids, j.ID)
	}
	want := []string{j3.ID, j2.ID, j1.ID}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("dispatch order %v, want %v", ids, want)
		}
	}

	none, err := s.MoveToFront(ctx, []string{"missing"})
	if err != nil || len(none) != 0 {
		t.Fatalf("unknown id: %v %v", none, err)
	}
}
