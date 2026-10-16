package agent

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/analysis"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/news"
)

type fakeNews struct{ reports map[int]news.Report }

func (f *fakeNews) Injuries(context.Context) (map[int]news.Report, error)  { return f.reports, nil }
func (f *fakeNews) Headlines(context.Context, int) ([]news.Article, error) { return nil, nil }

const starTeam = 7

// handcuffLeague: an opponent rosters a star RB; his backup is a free agent
// who looks unremarkable until the star gets hurt.
func handcuffLeague() (*fakeESPN, *espn.Player, *espn.Player) {
	nextID = 700
	at := func(p *espn.Player, team int) *espn.Player { p.ProTeamID = team; return p }
	me := team(1, "Me",
		at(mk("My QB", "QB", espn.SlotQB, 20), 1),
		at(mk("My RB1", "RB", espn.SlotRB, 10), 2),
		at(mk("My RB2", "RB", espn.SlotRB, 9), 3),
		at(mk("My RB3", "RB", espn.SlotBench, 4), 4),
		at(mk("Backup QB", "QB", espn.SlotBench, 5), 5),
	)
	star := at(mk("Star RB", "RB", espn.SlotRB, 20), starTeam)
	opp := team(2, "Rival", at(mk("Opp QB", "QB", espn.SlotQB, 18), 8), star, at(mk("Opp RB", "RB", espn.SlotRB, 8), 9))
	backup := freeAgent(at(mk("Backup RB", "RB", espn.SlotBench, 6), starTeam), espn.StatusFreeAgent)
	f := &fakeESPN{pool: []*espn.Player{backup}, league: func() *espn.League {
		return &espn.League{
			ID: 1, Season: season, CurrentWeek: week, CurrentMatchupPeriod: week, FinalWeek: 17,
			LineupSlotCounts: map[int]int{espn.SlotQB: 1, espn.SlotRB: 2, espn.SlotBench: 2},
			Teams:            []*espn.Team{me.Clone(), opp.Clone()},
			Schedule:         []espn.Matchup{{Period: week, HomeTeamID: 1, AwayTeamID: 2}},
		}
	}}
	return f, star, backup
}

func TestWatchGrabsBackupWhenStarterRuledOut(t *testing.T) {
	f, star, backup := handcuffLeague()
	a := testAgent(f)
	a.Cfg.ApprovalMode = "auto"
	now := a.Now()
	nf := &fakeNews{reports: map[int]news.Report{
		star.ID: {PlayerID: star.ID, Name: star.Name, ProTeamID: starTeam, Position: "RB", Status: espn.InjuryQuestionable, Date: now.Add(-24 * time.Hour)},
	}}
	w := &Watcher{Agent: a, News: nf, Apply: true, Out: io.Discard, StatePath: filepath.Join(t.TempDir(), "state.json")}

	// Tick 1: baseline. A questionable star doesn't make his backup worth a move.
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := countAdds(f, backup.ID); n != 0 {
		t.Fatalf("backup added before any injury news (%d)", n)
	}

	// Tick 2: the star is ruled out. The agent should grab the backup now.
	nf.reports[star.ID] = news.Report{PlayerID: star.ID, Name: star.Name, ProTeamID: starTeam, Position: "RB", Status: espn.InjuryOut, Date: now.Add(-10 * time.Minute)}
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := countAdds(f, backup.ID); n != 1 {
		t.Fatalf("expected an immediate FREEAGENT add of the backup, got %d", n)
	}
	if len(w.state.Acquisitions) != 1 || w.state.Acquisitions[0].PlayerID != backup.ID {
		t.Errorf("acquisition not recorded: %+v", w.state.Acquisitions)
	}

	// Tick 3: nothing new; no further transactions.
	before := len(f.submitted)
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.submitted) != before {
		t.Errorf("acted again without new news")
	}

	// A restarted watcher picks up the saved state (no re-reaction).
	w2 := &Watcher{Agent: a, News: nf, Apply: true, Out: io.Discard, StatePath: w.StatePath}
	if err := w2.load(); err != nil || len(w2.state.Acquisitions) != 1 || len(w2.state.Statuses) != 1 {
		t.Errorf("state not persisted: %+v %v", w2.state, err)
	}
}

func TestStaleInjuryNewsDoesNotBoost(t *testing.T) {
	f, star, backup := handcuffLeague()
	a := testAgent(f)
	a.Cfg.ApprovalMode = "auto"
	a.Injuries = nil
	nf := &fakeNews{reports: map[int]news.Report{
		// Out for days: ESPN's projections already reflect it.
		star.ID: {PlayerID: star.ID, ProTeamID: starTeam, Position: "RB", Status: espn.InjuryOut, Date: a.Now().Add(-5 * 24 * time.Hour)},
	}}
	w := &Watcher{Agent: a, News: nf, Apply: true, Out: io.Discard, StatePath: filepath.Join(t.TempDir(), "s.json")}
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := countAdds(f, backup.ID); n != 0 {
		t.Errorf("stale news shouldn't trigger a boosted pickup")
	}
}

func TestDailyCapStopsPickups(t *testing.T) {
	f, star, backup := handcuffLeague()
	a := testAgent(f)
	a.Cfg.ApprovalMode = "auto"
	a.Cfg.MaxAcquisitionsPerDay = 0
	nf := &fakeNews{reports: map[int]news.Report{
		star.ID: {PlayerID: star.ID, ProTeamID: starTeam, Position: "RB", Status: espn.InjuryOut, Date: a.Now().Add(-time.Hour)},
	}}
	w := &Watcher{Agent: a, News: nf, Apply: true, Out: io.Discard, StatePath: filepath.Join(t.TempDir(), "s.json")}
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := countAdds(f, backup.ID); n != 0 {
		t.Errorf("daily cap ignored")
	}
}

func TestRecentPickupsAreNotDropped(t *testing.T) {
	f, _ := scenario()
	a := testAgent(f)
	a.Cfg.ApprovalMode = "auto"
	p0, _ := a.Plan(context.Background())
	// Protect whoever the agent would drop for the kicker claim.
	var dropped int
	for _, act := range p0.Actions {
		for _, it := range act.Txn.Items {
			if it.Type == espn.ItemDrop {
				dropped = it.PlayerID
			}
		}
	}
	if dropped == 0 {
		t.Skip("scenario has no drop")
	}
	a.RecentlyAcquired = map[int]time.Time{dropped: a.Now().Add(-48 * time.Hour)}
	p1, _ := a.Plan(context.Background())
	for _, act := range p1.Actions {
		for _, it := range act.Txn.Items {
			if it.Type == espn.ItemDrop && it.PlayerID == dropped {
				t.Errorf("dropped a player picked up 2 days ago")
			}
		}
	}
}

func countAdds(f *fakeESPN, id int) int {
	n := 0
	for _, t := range f.submitted {
		for _, it := range t.Items {
			if it.Type == espn.ItemAdd && it.PlayerID == id {
				n++
			}
		}
	}
	return n
}

func TestReturningPlayerOpensNoOpportunity(t *testing.T) {
	f, star, backup := handcuffLeague()
	a := testAgent(f)
	a.Cfg.ApprovalMode = "auto"
	nf := &fakeNews{reports: map[int]news.Report{
		// Designated to return from IR: the backup's role is shrinking, not growing.
		star.ID: {PlayerID: star.ID, ProTeamID: starTeam, Position: "RB", Status: espn.InjuryReserve, Returning: true, Date: a.Now().Add(-time.Hour)},
	}}
	w := &Watcher{Agent: a, News: nf, Apply: true, Out: io.Discard, StatePath: filepath.Join(t.TempDir(), "s.json")}
	if err := w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := countAdds(f, backup.ID); n != 0 {
		t.Errorf("returning starter shouldn't boost his backup")
	}
}

func TestNewsStatusDoesNotDriveIRMoves(t *testing.T) {
	f, me := scenario()
	// ESPN Fantasy still lists this RB as active; the injury report says OUT.
	rb := me.Roster[2]
	a := testAgent(f)
	a.Cfg.ApprovalMode = "auto"
	a.Injuries = map[int]analysis.Injury{rb.ID: {PlayerID: rb.ID, ProTeamID: rb.ProTeamID, Status: espn.InjuryOut, Date: a.Now().Add(-time.Hour)}}
	p, err := a.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, act := range p.Actions {
		if act.Kind == ActIRStash && act.Txn.Items[0].PlayerID == rb.ID {
			t.Errorf("IR move based on news ESPN Fantasy hasn't confirmed would be rejected")
		}
	}
	if slot := p.Lineup.Slots[rb.ID]; espn.IsStartingSlot(slot) {
		t.Errorf("player reported OUT should be benched, got %s", espn.SlotName(slot))
	}
}
