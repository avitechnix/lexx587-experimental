package agent

import (
	"context"
	"fmt"
	"io"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/approval"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
)

// ExecuteApproved runs approved items, in order, after re-checking each one
// against fresh league data: the situation may have changed while the move
// waited for approval. Items whose dependencies are still undecided wait;
// items whose dependencies didn't run are skipped. Each item's Status and
// Result are updated in place.
func (a *Agent) ExecuteApproved(ctx context.Context, store *approval.Store, w io.Writer) ([]*approval.Item, error) {
	items := store.Approved()
	if len(items) == 0 {
		return nil, nil
	}
	l, err := a.ESPN.FetchLeague(ctx)
	if l == nil {
		return nil, err
	}
	me, err := a.myTeam(l)
	if err != nil {
		return nil, err
	}
	s := a.newState(l, me)
	now := a.Now()

	var touched []*approval.Item
	for _, it := range items {
		wait, why := depsStatus(store, it)
		if wait {
			continue
		}
		touched = append(touched, it)
		finish := func(st approval.Status, result string) {
			it.Status, it.Result, it.Done = st, result, now
		}
		if why != "" {
			finish(approval.Skipped, why)
			continue
		}
		if !it.Expires.IsZero() && now.After(it.Expires) {
			finish(approval.Skipped, "approved after it expired")
			continue
		}
		if problem := s.validate(it, me); problem != "" {
			finish(approval.Skipped, problem)
			continue
		}
		it.Txn.MemberID = s.member()
		act := Action{Kind: ActionKind(it.Kind), Summary: it.Summary, Txn: it.Txn}
		if err := a.submit(ctx, act, w); err != nil {
			finish(approval.Failed, err.Error())
			continue
		}
		applyLocally(me, it.Txn)
		switch ActionKind(it.Kind) {
		case ActClaim:
			finish(approval.Executed, "waiver claim filed; ESPN awards it when waivers process")
		case ActTrade:
			finish(approval.Executed, "trade offer sent; waiting on the other team")
		default:
			finish(approval.Executed, "done")
		}
	}
	return touched, nil
}

// depsStatus says whether an item must wait for undecided dependencies, or
// why it can't run because a dependency didn't happen.
func depsStatus(store *approval.Store, it *approval.Item) (wait bool, skip string) {
	for _, id := range it.Depends {
		d := store.Item(id)
		if d == nil {
			return false, fmt.Sprintf("depends on move #%d, which is gone", id)
		}
		switch d.Status {
		case approval.Executed:
		case approval.Pending, approval.Approved:
			return true, ""
		default:
			return false, fmt.Sprintf("depends on move %d (%s), which was %s", d.Number, d.Summary, d.Status)
		}
	}
	return false, ""
}

// validate re-checks an approved move against the current league.
func (s *state) validate(it *approval.Item, me *espn.Team) string {
	now := s.a.Now()
	locked := func(p *espn.Player) bool {
		return p.Locked || s.l.GameStarted(p.ProTeamID, s.l.CurrentWeek, now)
	}
	owner := func(id int) *espn.Team {
		for _, t := range s.l.Teams {
			if t.Player(id) != nil {
				return t
			}
		}
		return nil
	}
	adds, drops := 0, 0
	for _, x := range it.Txn.Items {
		switch x.Type {
		case espn.ItemAdd:
			adds++
			if t := owner(x.PlayerID); t != nil {
				return fmt.Sprintf("the player to add is no longer available (now on %s)", t.Name)
			}
			if inj, ok := s.a.Injuries[x.PlayerID]; ok {
				switch inj.Status {
				case espn.InjuryOut, espn.InjuryReserve, espn.InjurySuspension, espn.InjuryDoubtful:
					return fmt.Sprintf("the player you'd add is now %s", inj.Status)
				}
			}
		case espn.ItemDrop:
			drops++
			p := me.Player(x.PlayerID)
			if p == nil {
				return "a player in this move is no longer on your roster"
			}
			if locked(p) {
				return fmt.Sprintf("%s's game has started, so he can't be dropped until it ends", p.Name)
			}
		case espn.ItemLineup:
			p := me.Player(x.PlayerID)
			if p == nil {
				return "a player in this move is no longer on your roster"
			}
			if locked(p) {
				return fmt.Sprintf("%s's game has started; his lineup slot is locked", p.Name)
			}
			to := *x.ToLineupSlotID
			if to == espn.SlotIR {
				if !s.irEligible(p) {
					return fmt.Sprintf("ESPN no longer lists %s as IR-eligible (%s)", p.Name, p.InjuryStatus)
				}
				used := 0
				for _, q := range me.Roster {
					if q.LineupSlot == espn.SlotIR {
						used++
					}
				}
				if used >= s.l.LineupSlotCounts[espn.SlotIR] {
					return "your IR slots are full"
				}
				from := p.LineupSlot // the player may have moved; stash from wherever he is
				x.FromLineupSlotID = &from
				continue
			}
			if p.LineupSlot != *x.FromLineupSlotID {
				return "your lineup changed since this was proposed; a fresh lineup will be proposed"
			}
			if *x.FromLineupSlotID == espn.SlotIR && activeCount(me) >= s.l.RosterSize() {
				return "no open roster spot to activate him into"
			}
		case espn.ItemTrade:
			t := owner(x.PlayerID)
			if t == nil || x.FromTeamID == nil || t.ID != *x.FromTeamID {
				return "a player in this trade has changed teams"
			}
		}
	}
	if adds > drops && activeCount(me)+adds-drops > s.l.RosterSize() {
		return "your roster is full"
	}
	return ""
}

// applyLocally mirrors an executed transaction in the local model, so the
// next approved item is validated against the updated roster.
func applyLocally(me *espn.Team, t *espn.Transaction) {
	if t.Type == espn.TxnWaiver || t.Type == espn.TxnTrade {
		return // nothing changes until ESPN processes it
	}
	for _, x := range t.Items {
		switch x.Type {
		case espn.ItemAdd:
			me.Roster = append(me.Roster, &espn.Player{ID: x.PlayerID, LineupSlot: espn.SlotBench, OnTeamID: me.ID, Status: espn.StatusOnTeam})
		case espn.ItemDrop:
			me.Remove(x.PlayerID)
		case espn.ItemLineup:
			if p := me.Player(x.PlayerID); p != nil {
				p.LineupSlot = *x.ToLineupSlotID
			}
		}
	}
}
