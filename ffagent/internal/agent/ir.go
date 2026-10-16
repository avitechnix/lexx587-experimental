package agent

import (
	"fmt"
	"sort"
	"time"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
)

// planIR keeps the IR slots legal and full:
//  1. Players in IR who are healthy again must come off (ESPN freezes all
//     other roster moves while an IR slot holds an ineligible player). If
//     the roster is full, the least valuable player is dropped to make room.
//  2. Injured, IR-eligible players are moved into open IR slots, most
//     valuable first, which frees bench spots for replacements.
func (s *state) planIR(t *espn.Team) []Action {
	var acts []Action
	week := s.l.CurrentWeek
	capIR := s.l.LineupSlotCounts[espn.SlotIR]

	for _, p := range append([]*espn.Player(nil), t.Roster...) {
		if p.LineupSlot != espn.SlotIR || s.irEligible(p) {
			continue
		}
		if activeCount(t) >= s.l.RosterSize() {
			d := s.cheapestDrop(t, p.ID)
			if d == nil {
				s.warn(fmt.Sprintf("%s is healthy (%s) but stuck on IR and nobody is safe to drop; ESPN will block moves until fixed", p.Name, p.InjuryStatus))
				continue
			}
			acts = append(acts, Action{
				Kind:    ActDrop,
				Summary: fmt.Sprintf("Drop %s (%s) to activate %s from IR", d.Name, d.Position, p.Name),
				Reason:  fmt.Sprintf("lowest-impact player on the roster (%.1f ROS pts)", s.val(d).ROSValue),
				Txn:     espn.NewTransaction(s.member(), espn.TxnFreeAgent, t.ID, week, espn.DropItem(d.ID, t.ID)),
			})
			t.Remove(d.ID)
		}
		var deps []int
		if n := len(acts); n > 0 && acts[n-1].Kind == ActDrop {
			deps = []int{n - 1}
		}
		acts = append(acts, Action{
			Depends: deps,
			Kind:    ActIRActivate,
			Summary: fmt.Sprintf("Activate %s (%s) from IR to bench", p.Name, p.Position),
			Reason:  fmt.Sprintf("status is %s, no longer IR-eligible", p.InjuryStatus),
			Txn:     espn.NewTransaction(s.member(), espn.TxnRoster, t.ID, week, espn.LineupMove(p.ID, espn.SlotIR, espn.SlotBench)),
		})
		p.LineupSlot = espn.SlotBench
	}

	used := 0
	var cands []*espn.Player
	for _, p := range t.Roster {
		switch {
		case p.LineupSlot == espn.SlotIR:
			used++
		case s.irEligible(p) && !p.Locked:
			cands = append(cands, p)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return s.val(cands[i]).ROSValue > s.val(cands[j]).ROSValue })
	for _, p := range cands {
		if used >= capIR {
			s.warn(fmt.Sprintf("%s is %s but IR slots are full; consider dropping if the injury is long-term", p.Name, p.InjuryStatus))
			continue
		}
		used++
		if activeCount(t) >= s.l.RosterSize() {
			s.freedBy = append(s.freedBy, len(acts)) // a full roster gains a spot
		}
		acts = append(acts, Action{
			Kind:    ActIRStash,
			Summary: fmt.Sprintf("Move %s (%s) from %s to IR", p.Name, p.Position, espn.SlotName(p.LineupSlot)),
			Reason:  fmt.Sprintf("%s; keeps %.1f ROS pts stashed and frees a roster spot for a replacement", p.InjuryStatus, s.val(p).ROSValue),
			Txn:     espn.NewTransaction(s.member(), espn.TxnRoster, t.ID, week, espn.LineupMove(p.ID, p.LineupSlot, espn.SlotIR)),
		})
		s.irReplacements = append(s.irReplacements, p)
		p.LineupSlot = espn.SlotIR
	}
	return acts
}

// droppable returns players we may drop or trade away.
func (s *state) droppable(t *espn.Team, exclude ...int) []*espn.Player {
	skip := map[int]bool{}
	for _, id := range exclude {
		skip[id] = true
	}
	var out []*espn.Player
	for _, p := range t.Roster {
		if skip[p.ID] || p.LineupSlot == espn.SlotIR || p.Locked || s.protected(p) ||
			p.Status == pendingClaim || p.Status == pendingDrop || s.added[p.ID] || s.recentPickup(p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// cheapestDrop returns the player whose removal costs the team the least.
func (s *state) cheapestDrop(t *espn.Team, exclude ...int) *espn.Player {
	base := s.teamValue(t.Roster, s.a.Cfg.ThisWeekWeight)
	var best *espn.Player
	bestLoss := 0.0
	for _, d := range s.droppable(t, exclude...) {
		loss := base - s.teamValue(without(t.Roster, d.ID), s.a.Cfg.ThisWeekWeight)
		if best == nil || loss < bestLoss {
			best, bestLoss = d, loss
		}
	}
	return best
}

func without(roster []*espn.Player, id int) []*espn.Player {
	out := make([]*espn.Player, 0, len(roster))
	for _, p := range roster {
		if p.ID != id {
			out = append(out, p)
		}
	}
	return out
}

// recentPickup reports whether the agent acquired the player recently; such
// players are kept so the agent doesn't churn its own pickups.
func (s *state) recentPickup(p *espn.Player) bool {
	at, ok := s.a.RecentlyAcquired[p.ID]
	return ok && s.a.Now().Sub(at) < time.Duration(s.a.Cfg.KeepNewPickupsDays)*24*time.Hour
}
