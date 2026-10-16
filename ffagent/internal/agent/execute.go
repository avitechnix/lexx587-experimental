package agent

import (
	"context"
	"fmt"
	"io"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/lineup"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/secrets"
)

// Scope selects which parts of a plan to execute.
type Scope struct {
	Roster bool // IR moves, drops, adds, waiver claims
	Lineup bool // set the starting lineup
	Trades bool // send trade offers
}

// LineupAction builds the single ROSTER transaction for a set of moves.
func LineupAction(member secrets.Secret, teamID, week int, moves []lineup.Move) *Action {
	if len(moves) == 0 {
		return nil
	}
	var items []espn.TxnItem
	for _, m := range moves {
		items = append(items, espn.LineupMove(m.Player.ID, m.From, m.To))
	}
	return &Action{
		Kind:    ActLineup,
		Summary: fmt.Sprintf("Set lineup (%d moves)", len(moves)),
		Txn:     espn.NewTransaction(member, espn.TxnRoster, teamID, week, items...),
	}
}

// Execute submits the plan's transactions to ESPN. Roster moves run first,
// in order, stopping at the first failure since later moves depend on
// earlier ones. If any roster move changed who's on the team, the lineup
// is re-planned from fresh league data before it's submitted.
func (a *Agent) Execute(ctx context.Context, p *Plan, scope Scope, w io.Writer) error {
	member := espn.NormalizeSWIDSecret(a.Cfg.SWID)
	rosterChanged := false
	if scope.Roster {
		for _, act := range p.Actions {
			if err := a.submit(ctx, act, w); err != nil {
				return err
			}
			if act.Kind != ActClaim {
				rosterChanged = true
			}
		}
	}

	if scope.Lineup {
		moves := p.Moves
		if rosterChanged {
			fresh, err := a.PlanLineup(ctx)
			if err != nil {
				return fmt.Errorf("re-planning lineup: %w", err)
			}
			moves = fresh.Moves
		}
		if act := LineupAction(member, p.Me.ID, p.League.CurrentWeek, moves); act != nil {
			if err := a.submit(ctx, *act, w); err != nil {
				return err
			}
		} else {
			fmt.Fprintln(w, "Lineup already optimal; no changes.")
		}
	}

	if scope.Trades {
		for _, t := range p.Trades {
			if err := a.submit(ctx, t.Action, w); err != nil {
				fmt.Fprintf(w, "  trade offer failed (continuing): %v\n", err)
			}
		}
	}
	return nil
}

func (a *Agent) submit(ctx context.Context, act Action, w io.Writer) error {
	fmt.Fprintf(w, "→ %s: %s\n", act.Kind, act.Summary)
	res, err := a.ESPN.Submit(ctx, act.Txn)
	if err != nil {
		return fmt.Errorf("%s failed: %w", act.Kind, err)
	}
	if res != nil && res.Status != "" {
		fmt.Fprintf(w, "  ESPN: %s %s\n", res.Status, res.ID)
	}
	if a.OnSubmitted != nil {
		a.OnSubmitted(act)
	}
	return nil
}

// PlanLineup re-reads the league and plans only the starting lineup, using
// the same win-probability-maximizing logic as Plan.
func (a *Agent) PlanLineup(ctx context.Context) (*Plan, error) {
	l, err := a.ESPN.FetchLeague(ctx)
	if err != nil && l == nil {
		return nil, err
	}
	me, err := a.myTeam(l)
	if err != nil {
		return nil, err
	}
	s := a.newState(l, me)
	p := &Plan{League: l, Me: me, Roster: me}
	p.Matchup = s.analyzeMatchup(me)
	s.lambda = p.Matchup.Lambda
	p.Lineup = lineup.Optimize(l, me.Roster, s.weekScorer(s.lambda))
	p.Moves = lineup.Moves(me.Roster, p.Lineup)
	p.Values = s.values
	return p, nil
}
