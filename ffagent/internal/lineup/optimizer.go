// Package lineup picks the best starting lineup for a roster.
package lineup

import (
	"math"
	"sort"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
)

// Scorer returns how much a player is worth in a starting slot.
type Scorer func(p *espn.Player) float64

// Assignment is the chosen lineup.
type Assignment struct {
	Slots map[int]int // player ID -> lineup slot
	Total float64     // sum of starter scores
	Empty []int       // starting slots that could not be filled
}

// Move is a single lineup change.
type Move struct {
	Player *espn.Player
	From   int
	To     int
}

// Optimize assigns players to the league's starting slots to maximize the
// total score. Players already in the IR slot stay there. Locked players
// keep their current slot. Everyone else not starting goes to the bench.
func Optimize(l *espn.League, roster []*espn.Player, score Scorer) Assignment {
	a := Assignment{Slots: map[int]int{}}
	slots := l.StartingSlots()

	// Fixed players: IR occupants and anyone whose game has started.
	var free []*espn.Player
	for _, p := range roster {
		switch {
		case p.LineupSlot == espn.SlotIR:
			a.Slots[p.ID] = espn.SlotIR
		case p.Locked:
			a.Slots[p.ID] = p.LineupSlot
			if espn.IsStartingSlot(p.LineupSlot) {
				slots = removeOne(slots, p.LineupSlot)
				a.Total += score(p)
			}
		default:
			free = append(free, p)
		}
	}
	// Deterministic order for stable tie-breaking.
	sort.SliceStable(free, func(i, j int) bool { return free[i].ID < free[j].ID })

	w := make([][]float64, len(slots))
	for i, s := range slots {
		w[i] = make([]float64, len(free))
		for j, p := range free {
			if !p.CanPlay(s) {
				w[i][j] = math.Inf(-1)
				continue
			}
			// Tiny bonus for staying put avoids pointless swaps between equals.
			bonus := 0.0
			if p.LineupSlot == s {
				bonus = 1e-6
			}
			w[i][j] = math.Max(score(p), 0) + bonus
		}
	}
	assign := maxWeightAssignment(w)
	used := map[int]bool{}
	for i, j := range assign {
		if j < 0 {
			a.Empty = append(a.Empty, slots[i])
			continue
		}
		p := free[j]
		used[p.ID] = true
		a.Slots[p.ID] = slots[i]
		a.Total += score(p)
	}
	for _, p := range free {
		if !used[p.ID] {
			a.Slots[p.ID] = espn.SlotBench
		}
	}
	return a
}

// Moves returns the lineup changes needed to go from the roster's current
// slots to the assignment. Benchings are listed before promotions so a
// sequential executor never overfills a slot.
func Moves(roster []*espn.Player, a Assignment) []Move {
	var out []Move
	for _, p := range roster {
		to, ok := a.Slots[p.ID]
		if !ok || to == p.LineupSlot {
			continue
		}
		out = append(out, Move{Player: p, From: p.LineupSlot, To: to})
	}
	sort.SliceStable(out, func(i, j int) bool {
		bi, bj := out[i].To == espn.SlotBench, out[j].To == espn.SlotBench
		if bi != bj {
			return bi
		}
		return out[i].Player.ID < out[j].Player.ID
	})
	return out
}

// Apply updates roster lineup slots in place to match the assignment.
func Apply(roster []*espn.Player, a Assignment) {
	for _, p := range roster {
		if s, ok := a.Slots[p.ID]; ok {
			p.LineupSlot = s
		}
	}
}

func removeOne(xs []int, v int) []int {
	for i, x := range xs {
		if x == v {
			return append(xs[:i:i], xs[i+1:]...)
		}
	}
	return xs
}
