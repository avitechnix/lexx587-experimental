package agent

import (
	"fmt"
	"math"
	"strings"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
)

// planPickups finds the free-agent adds and waiver claims that most improve
// the team, one at a time, each paired with the best player to drop (or an
// open roster spot, e.g. one freed by an IR move).
func (s *state) planPickups(t *espn.Team) []Action {
	var acts []Action
	cfg := s.a.Cfg
	size := s.l.RosterSize()
	faab := t.FAABRemaining
	taken := map[int]bool{}

	for n := 0; n < cfg.MaxAcquisitionsPerRun; n++ {
		base := s.teamValue(t.Roster, cfg.ThisWeekWeight)
		open := size - activeCount(t)
		drops := s.droppable(t)

		var best struct {
			add, drop *espn.Player
			roster    []*espn.Player
			gain      float64
		}
		best.gain = math.Inf(-1)
		try := func(c, d *espn.Player) {
			r := s.withMove(t.Roster, c, d)
			if g := s.teamValue(r, cfg.ThisWeekWeight) - base; g > best.gain {
				best.add, best.drop, best.roster, best.gain = c, d, r, g
			}
		}
		for _, fa := range s.pool {
			if taken[fa.ID] {
				continue
			}
			c := *fa
			c.OnTeamID = t.ID
			c.LineupSlot = espn.SlotBench
			if fa.Status == espn.StatusWaivers {
				c.Status = pendingClaim
			} else {
				c.Status = espn.StatusOnTeam
			}
			if open > 0 {
				try(&c, nil)
			}
			for _, d := range drops {
				try(&c, d)
			}
		}
		if best.add == nil || best.gain < cfg.MinPickupGain {
			break
		}

		act := s.pickupAction(t, best.add, best.drop, best.gain, &faab)
		if best.drop == nil && len(s.freedBy) > 0 {
			act.Depends = []int{s.freedBy[0]} // uses the spot an IR move opened
			s.freedBy = s.freedBy[1:]
		}
		acts = append(acts, act)
		taken[best.add.ID] = true
		s.added[best.add.ID] = true
		t.Roster = best.roster
	}
	return acts
}

// withMove returns a new roster with c added and d (if any) removed. For a
// waiver claim the dropped player stays (as a pending drop) until it clears.
func (s *state) withMove(roster []*espn.Player, c, d *espn.Player) []*espn.Player {
	out := make([]*espn.Player, 0, len(roster)+1)
	for _, p := range roster {
		if d != nil && p.ID == d.ID {
			if c.Status == pendingClaim {
				pd := *p
				pd.Status = pendingDrop
				out = append(out, &pd)
			}
			continue
		}
		out = append(out, p)
	}
	return append(out, c)
}

func (s *state) pickupAction(t *espn.Team, c, d *espn.Player, gain float64, faab *int) Action {
	week := s.l.CurrentWeek
	v := s.val(c)
	items := []espn.TxnItem{espn.AddItem(c.ID, t.ID)}
	summary := fmt.Sprintf("Add %s (%s, %s)", c.Name, c.Position, c.ProTeam)
	if d != nil {
		items = append(items, espn.DropItem(d.ID, t.ID))
		summary += fmt.Sprintf(", drop %s (%s)", d.Name, d.Position)
	}
	// Lead with the reason in plain words, then the numbers behind it.
	var why []string
	if len(v.Notes) > 0 {
		why = append(why, strings.Join(v.Notes, ", "))
	}
	if d == nil {
		if r := s.replacementFor(c); r != nil {
			why = append(why, "replaces "+r.Name+" (IR)")
		}
	}
	proj := fmt.Sprintf("%s projects %.1f pts this week and %.0f rest of season", c.Name, v.WeekProjection, v.ROSValue)
	if d != nil {
		dv := s.val(d)
		proj += fmt.Sprintf(" (vs %.1f and %.0f for %s)", dv.WeekProjection, dv.ROSValue, d.Name)
	}
	why = append(why, proj, fmt.Sprintf("team value +%.1f", gain))
	if c.Status != pendingClaim {
		return Action{
			Kind:    ActAdd,
			Summary: summary,
			Reason:  strings.Join(why, "; "),
			Txn:     espn.NewTransaction(s.member(), espn.TxnFreeAgent, t.ID, week, items...),
		}
	}
	bid := -1
	if s.l.UsesFAAB {
		bid = s.faabBid(c, gain, *faab)
		*faab -= bid
		summary += fmt.Sprintf(" — bid $%d", bid)
	}
	return Action{
		Kind:    ActClaim,
		Summary: summary,
		Reason:  strings.Join(why, "; "),
		Txn:     espn.NewWaiverClaim(s.member(), t.ID, week, bid, items...),
	}
}

// faabBid scales the bid with the size of the upgrade: small upgrades get
// token bids, big ones approach MaxFAABFraction of the remaining budget.
// Kickers and defenses are streamed week to week, so they get a fraction.
func (s *state) faabBid(c *espn.Player, gain float64, remaining int) int {
	if remaining <= 0 {
		return 0
	}
	frac := math.Min(s.a.Cfg.MaxFAABFraction, gain/(gain+100))
	if c.Position == "K" || c.Position == "D/ST" {
		frac *= 0.15
	}
	return min(remaining, int(math.Ceil(float64(remaining)*frac)))
}

// replacementFor returns the IR-stashed player a pickup at the same position
// is replacing, if any.
func (s *state) replacementFor(c *espn.Player) *espn.Player {
	for i, r := range s.irReplacements {
		if r.Position == c.Position {
			s.irReplacements = append(s.irReplacements[:i:i], s.irReplacements[i+1:]...)
			return r
		}
	}
	return nil
}
