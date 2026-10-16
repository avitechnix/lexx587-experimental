package lineup

import (
	"math"
	"math/rand"
	"testing"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
)

func player(id int, pos string, slot int) *espn.Player {
	return &espn.Player{ID: id, Name: pos, Position: pos, EligibleSlots: espn.EligibleSlotsFor(pos), LineupSlot: slot}
}

func TestOptimizeFillsFlexWithBestRemaining(t *testing.T) {
	l := &espn.League{LineupSlotCounts: map[int]int{espn.SlotQB: 1, espn.SlotRB: 2, espn.SlotWR: 2, espn.SlotTE: 1, espn.SlotFlex: 1, espn.SlotBench: 5}}
	roster := []*espn.Player{
		player(1, "QB", espn.SlotQB),
		player(2, "RB", espn.SlotRB), player(3, "RB", espn.SlotRB), player(4, "RB", espn.SlotBench),
		player(5, "WR", espn.SlotWR), player(6, "WR", espn.SlotWR), player(7, "WR", espn.SlotFlex),
		player(8, "TE", espn.SlotTE), player(9, "TE", espn.SlotBench),
	}
	pts := map[int]float64{1: 20, 2: 15, 3: 9, 4: 12, 5: 14, 6: 11, 7: 8, 8: 6, 9: 10}
	a := Optimize(l, roster, func(p *espn.Player) float64 { return pts[p.ID] })

	want := map[int]int{1: espn.SlotQB, 2: espn.SlotRB, 3: espn.SlotRB, 5: espn.SlotWR, 6: espn.SlotWR, 9: espn.SlotTE}
	for id, slot := range want {
		if a.Slots[id] != slot {
			t.Errorf("player %d: slot %s, want %s", id, espn.SlotName(a.Slots[id]), espn.SlotName(slot))
		}
	}
	// RB 4 (12) beats WR 7 (8) and TE 8 (6) for flex. RBs 3 and 4 could swap
	// RB/FLEX at no cost; player 3 stays in the RB slot to avoid a pointless move.
	if a.Slots[4] != espn.SlotFlex {
		t.Errorf("flex should be player 4, got slot %s", espn.SlotName(a.Slots[4]))
	}
	if a.Slots[7] != espn.SlotBench || a.Slots[8] != espn.SlotBench {
		t.Errorf("players 7 and 8 should be benched")
	}
	if got, want := a.Total, 20.0+15+12+14+11+10+9; math.Abs(got-want) > 1e-3 {
		t.Errorf("total %.2f, want %.2f", got, want)
	}

	moves := Moves(roster, a)
	if len(moves) == 0 || moves[0].To != espn.SlotBench {
		t.Errorf("benchings should come first: %+v", moves)
	}
}

func TestOptimizeRespectsLocksAndIR(t *testing.T) {
	l := &espn.League{LineupSlotCounts: map[int]int{espn.SlotRB: 1, espn.SlotBench: 3, espn.SlotIR: 1}}
	locked := player(1, "RB", espn.SlotRB)
	locked.Locked = true
	roster := []*espn.Player{locked, player(2, "RB", espn.SlotBench), player(3, "RB", espn.SlotIR)}
	pts := map[int]float64{1: 2, 2: 20, 3: 30}
	a := Optimize(l, roster, func(p *espn.Player) float64 { return pts[p.ID] })
	if a.Slots[1] != espn.SlotRB || a.Slots[2] != espn.SlotBench || a.Slots[3] != espn.SlotIR {
		t.Errorf("unexpected assignment %v", a.Slots)
	}
	if len(Moves(roster, a)) != 0 {
		t.Errorf("expected no moves")
	}
}

func TestOptimizeReportsEmptySlot(t *testing.T) {
	l := &espn.League{LineupSlotCounts: map[int]int{espn.SlotK: 1, espn.SlotQB: 1}}
	a := Optimize(l, []*espn.Player{player(1, "QB", espn.SlotBench)}, func(*espn.Player) float64 { return 10 })
	if len(a.Empty) != 1 || a.Empty[0] != espn.SlotK {
		t.Errorf("expected empty K slot, got %v", a.Empty)
	}
}

// Brute-force check of the assignment solver on random instances.
func TestMaxWeightAssignmentMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 300; iter++ {
		rows, cols := 1+r.Intn(5), 1+r.Intn(6)
		w := make([][]float64, rows)
		for i := range w {
			w[i] = make([]float64, cols)
			for j := range w[i] {
				if r.Float64() < 0.3 {
					w[i][j] = math.Inf(-1)
				} else {
					w[i][j] = float64(r.Intn(30))
				}
			}
		}
		got := score(w, maxWeightAssignment(w))
		want := brute(w, 0, map[int]bool{})
		if math.Abs(got-want) > 1e-9 {
			t.Fatalf("iter %d: got %v want %v for %v", iter, got, want, w)
		}
	}
}

func score(w [][]float64, a []int) float64 {
	s := 0.0
	seen := map[int]bool{}
	for i, j := range a {
		if j < 0 {
			continue
		}
		if seen[j] || math.IsInf(w[i][j], -1) {
			return math.Inf(-1)
		}
		seen[j] = true
		s += w[i][j]
	}
	return s
}

func brute(w [][]float64, i int, used map[int]bool) float64 {
	if i == len(w) {
		return 0
	}
	best := brute(w, i+1, used) // leave row unassigned
	for j := range w[i] {
		if used[j] || math.IsInf(w[i][j], -1) {
			continue
		}
		used[j] = true
		best = math.Max(best, w[i][j]+brute(w, i+1, used))
		used[j] = false
	}
	return best
}
