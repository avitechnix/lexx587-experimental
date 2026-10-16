package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
)

// TradeIdea is a trade that helps us and plausibly helps (or barely hurts)
// the other team, so they have a reason to accept.
type TradeIdea struct {
	Partner   *espn.Team
	Give      []*espn.Player
	Get       []*espn.Player
	MyGain    float64 // change in our rest-of-season team value
	TheirGain float64 // change in theirs
	Action    Action
}

// findTrades searches 1-for-1, 2-for-1 and 1-for-2 trades with every team.
// Value is judged by each roster's best rest-of-season lineup, so a trade
// that turns our bench depth into a starter upgrade, while filling a hole
// in their lineup, rises to the top.
func (s *state) findTrades(me *espn.Team) []TradeIdea {
	cfg := s.a.Cfg
	if cfg.MaxTradeIdeas <= 0 {
		return nil
	}
	// Judge trades on the roster we actually have: pending waiver claims
	// may not clear, and their drops haven't happened yet.
	me = settled(me)
	var mine []*espn.Player
	for _, p := range s.droppable(me) {
		if tradeable(p) {
			mine = append(mine, p)
		}
	}
	myBase := s.teamValue(me.Roster, 0)

	var ideas []TradeIdea
	for _, o := range s.l.Teams {
		if o.ID == me.ID {
			continue
		}
		var theirs []*espn.Player
		for _, p := range o.Roster {
			if p.LineupSlot != espn.SlotIR && tradeable(p) {
				theirs = append(theirs, p)
			}
		}
		theirBase := s.teamValue(o.Roster, 0)
		for _, give := range combos(mine, 2) {
			for _, get := range combos(theirs, 2) {
				if len(give) == 2 && len(get) == 2 {
					continue
				}
				myAfter := s.trim(swap(me.Roster, give, get), get)
				myGain := s.teamValue(myAfter, 0) - myBase
				if myGain < cfg.MinTradeGain {
					continue
				}
				theirAfter := s.trim(swap(o.Roster, get, give), give)
				theirGain := s.teamValue(theirAfter, 0) - theirBase
				if theirGain < -cfg.TradeTolerance {
					continue
				}
				ideas = append(ideas, TradeIdea{Partner: o, Give: give, Get: get, MyGain: myGain, TheirGain: theirGain})
			}
		}
	}

	// Favor trades good for us that they also have a reason to accept.
	score := func(t TradeIdea) float64 { return t.MyGain + 0.5*min(t.TheirGain, t.MyGain) }
	sort.SliceStable(ideas, func(i, j int) bool { return score(ideas[i]) > score(ideas[j]) })

	// Don't suggest the same player in multiple offers.
	used := map[int]bool{}
	var out []TradeIdea
	for _, t := range ideas {
		if len(out) >= cfg.MaxTradeIdeas {
			break
		}
		if anyUsed(used, t.Give) || anyUsed(used, t.Get) {
			continue
		}
		markUsed(used, t.Give)
		markUsed(used, t.Get)
		t.Action = s.tradeAction(me, t)
		out = append(out, t)
	}
	return out
}

// tradeable leaves kickers and defenses out: they're streamed off waivers.
func tradeable(p *espn.Player) bool { return p.Position != "K" && p.Position != "D/ST" }

// settled returns the team without pending waiver claims and with pending
// drops restored.
func settled(t *espn.Team) *espn.Team {
	c := *t
	c.Roster = nil
	for _, p := range t.Roster {
		switch p.Status {
		case pendingClaim:
			continue
		case pendingDrop:
			pc := *p
			pc.Status = espn.StatusOnTeam
			c.Roster = append(c.Roster, &pc)
		default:
			c.Roster = append(c.Roster, p)
		}
	}
	return &c
}

func (s *state) tradeAction(me *espn.Team, t TradeIdea) Action {
	var items []espn.TxnItem
	for _, p := range t.Give {
		items = append(items, espn.TradeItem(p.ID, me.ID, t.Partner.ID))
	}
	for _, p := range t.Get {
		items = append(items, espn.TradeItem(p.ID, t.Partner.ID, me.ID))
	}
	expires := s.a.Now().Add(time.Duration(s.a.Cfg.TradeExpiryHours) * time.Hour)
	return Action{
		Kind:    ActTrade,
		Summary: fmt.Sprintf("Offer %s: give %s, get %s", t.Partner.Name, names(t.Give), names(t.Get)),
		Reason:  fmt.Sprintf("+%.1f ROS team value for us, %+.1f for them", t.MyGain, t.TheirGain),
		Txn:     espn.NewTradeProposal(s.member(), me.ID, s.l.CurrentWeek, expires, items...),
	}
}

// swap returns roster minus out plus in (moved to the bench).
func swap(roster, out, in []*espn.Player) []*espn.Player {
	skip := map[int]bool{}
	for _, p := range out {
		skip[p.ID] = true
	}
	r := make([]*espn.Player, 0, len(roster)+len(in))
	for _, p := range roster {
		if !skip[p.ID] {
			r = append(r, p)
		}
	}
	for _, p := range in {
		c := *p
		c.LineupSlot = espn.SlotBench
		c.Locked = false
		r = append(r, &c)
	}
	return r
}

// trim drops the lowest rest-of-season players (never the ones just
// received) until the roster fits, mirroring the drop a team would make.
func (s *state) trim(roster, keep []*espn.Player) []*espn.Player {
	size := s.l.RosterSize()
	keepID := map[int]bool{}
	for _, p := range keep {
		keepID[p.ID] = true
	}
	for {
		active := 0
		var worst *espn.Player
		for _, p := range roster {
			if p.LineupSlot == espn.SlotIR || p.Status == pendingDrop {
				continue
			}
			active++
			if !keepID[p.ID] && (worst == nil || s.val(p).ROSValue < s.val(worst).ROSValue) {
				worst = p
			}
		}
		if active <= size || worst == nil {
			return roster
		}
		roster = without(roster, worst.ID)
	}
}

// combos returns all subsets of ps with 1..k elements.
func combos(ps []*espn.Player, k int) [][]*espn.Player {
	var out [][]*espn.Player
	for i := range ps {
		out = append(out, []*espn.Player{ps[i]})
		if k >= 2 {
			for j := i + 1; j < len(ps); j++ {
				out = append(out, []*espn.Player{ps[i], ps[j]})
			}
		}
	}
	return out
}

func anyUsed(used map[int]bool, ps []*espn.Player) bool {
	for _, p := range ps {
		if used[p.ID] {
			return true
		}
	}
	return false
}

func markUsed(used map[int]bool, ps []*espn.Player) {
	for _, p := range ps {
		used[p.ID] = true
	}
}

func names(ps []*espn.Player) string {
	var n []string
	for _, p := range ps {
		n = append(n, fmt.Sprintf("%s (%s)", p.Name, p.Position))
	}
	return strings.Join(n, " + ")
}
