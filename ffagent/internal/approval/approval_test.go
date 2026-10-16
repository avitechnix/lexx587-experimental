package approval

import (
	"path/filepath"
	"testing"
	"time"
)

func TestParseReply(t *testing.T) {
	type want struct {
		approve, decline []int
		undecided        []int
	}
	cases := map[string]want{
		"approve all": {approve: []int{1, 2, 3}},
		"Yes":         {approve: []int{1, 2, 3}},
		"yes!\n\nOn Fri, Oct 16, ffagent <x@y> wrote:\n> 1. ADD ...\n> decline 2": {approve: []int{1, 2, 3}},
		"approve 1 3, decline 2":              {approve: []int{1, 3}, decline: []int{2}},
		"yes 1,3":                             {approve: []int{1, 3}, undecided: []int{2}},
		"#2 no":                               {decline: []int{2}, undecided: []int{1, 3}},
		"no":                                  {decline: []int{1, 2, 3}},
		"decline all":                         {decline: []int{1, 2, 3}},
		"approve all except decline 2":        {approve: []int{1, 3}, decline: []int{2}},
		"thanks":                              {undecided: []int{1, 2, 3}},
		"approve 1\n-- \nSent from my iPhone": {approve: []int{1}, undecided: []int{2, 3}},
		// Ambiguous: a stray "no" makes it decline (the safe direction).
		"yes, no rush": {decline: []int{1, 2, 3}},
	}
	for text, w := range cases {
		d := ParseReply(text)
		check := func(ns []int, wantApprove, wantOK bool) {
			for _, n := range ns {
				a, ok := d.For(n)
				if ok != wantOK || (ok && a != wantApprove) {
					t.Errorf("%q: item %d = (approve=%v, decided=%v), want (approve=%v, decided=%v) [%s]", text, n, a, ok, wantApprove, wantOK, d.Describe())
				}
			}
		}
		check(w.approve, true, true)
		check(w.decline, false, true)
		check(w.undecided, false, false)
	}
}

func TestTokenIn(t *testing.T) {
	tok := NewToken()
	if len(tok) != 16 {
		t.Fatalf("token %q", tok)
	}
	if got := TokenIn("Re: RE: [ffagent #" + tok + "] 2 moves to review"); got != tok {
		t.Errorf("TokenIn = %q", got)
	}
	if TokenIn("Re: [ffagent #short] x") != "" {
		t.Error("accepted a malformed token")
	}
}

func TestStoreLifecycle(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "approvals.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	items := []*Item{
		{Kind: "IR-STASH", Signature: "a", Expires: now.Add(time.Hour)},
		{Kind: "ADD", Signature: "b", Depends: []int{0}, Expires: now.Add(time.Hour)},
		{Kind: "LINEUP", Signature: "c", Expires: now.Add(time.Minute)},
	}
	b := s.AddBatch("moves", items, now)
	if items[1].Depends[0] != items[0].ID || items[2].Number != 3 {
		t.Fatalf("ids not assigned: %+v", items)
	}
	s.Decide(b, ParseReply("approve 1 2, decline 3"), "email", now)
	if items[0].Status != Approved || items[1].Status != Approved || items[2].Status != Declined {
		t.Fatalf("statuses: %s %s %s", items[0].Status, items[1].Status, items[2].Status)
	}
	if !s.RecentlyDeclined("c", now.Add(time.Hour), 24*time.Hour) {
		t.Error("decline not remembered")
	}
	// A second reply can't flip an already decided item.
	s.Decide(b, ParseReply("decline all"), "email", now)
	if items[0].Status != Approved {
		t.Error("decided item changed")
	}
	b2 := s.AddBatch("moves", []*Item{{Kind: "ADD", Signature: "d", Expires: now.Add(time.Minute)}}, now)
	if exp := s.Expire(now.Add(2 * time.Minute)); len(exp) != 1 || s.BatchItems(b2)[0].Status != Expired {
		t.Errorf("expiry: %+v", exp)
	}
	if err := s.Save(now); err != nil {
		t.Fatal(err)
	}
	s2, err := Load(path)
	if err != nil || len(s2.Items) != 4 || len(s2.Approved()) != 2 || s2.NextID != 5 {
		t.Fatalf("reload: %v %+v", err, s2)
	}
}
