// Package agent decides what to do with a fantasy team: who to start, who
// to stash on IR, who to pick up, and which trades to offer.
package agent

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/analysis"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/config"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/lineup"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/secrets"
)

// ESPN is the subset of the ESPN client the agent uses.
type ESPN interface {
	FetchLeague(ctx context.Context) (*espn.League, error)
	FreeAgents(ctx context.Context, l *espn.League, position string, limit int) ([]*espn.Player, error)
	Submit(ctx context.Context, t *espn.Transaction) (*espn.TransactionResult, error)
}

// Agent manages one fantasy team.
type Agent struct {
	ESPN ESPN
	Cfg  config.Config
	Opts analysis.Options
	Now  func() time.Time
}

// New returns an agent with default valuation options.
func New(c ESPN, cfg config.Config) *Agent {
	return &Agent{ESPN: c, Cfg: cfg, Opts: analysis.DefaultOptions(), Now: time.Now}
}

// ActionKind classifies a planned action.
type ActionKind string

const (
	ActIRActivate ActionKind = "IR-ACTIVATE"
	ActIRStash    ActionKind = "IR-STASH"
	ActDrop       ActionKind = "DROP"
	ActAdd        ActionKind = "ADD"
	ActClaim      ActionKind = "WAIVER-CLAIM"
	ActLineup     ActionKind = "LINEUP"
	ActTrade      ActionKind = "TRADE-OFFER"
)

// Action is one change the agent wants to make.
type Action struct {
	Kind    ActionKind
	Summary string
	Reason  string
	Txn     *espn.Transaction
}

// Plan is everything the agent decided for this run.
type Plan struct {
	League   *espn.League
	Me       *espn.Team
	Roster   *espn.Team // my roster after planned IR moves and free-agent adds
	Values   map[int]*analysis.Valuation
	Matchup  *MatchupReport
	Lineup   lineup.Assignment
	Moves    []lineup.Move
	Actions  []Action // roster actions (IR, drops, adds, claims), in order
	Trades   []TradeIdea
	Warnings []string
}

// state is the working context for building a plan.
type state struct {
	a      *Agent
	l      *espn.League
	me     *espn.Team
	values map[int]*analysis.Valuation
	pool   []*espn.Player // available free agents / waiver players
	lambda float64        // risk preference for this week's lineup

	added          map[int]bool   // players acquired this run (never dropped again)
	irReplacements []*espn.Player // players moved to IR who need replacing
	warnings       []string
}

func (s *state) warn(msg string) { s.warnings = append(s.warnings, msg) }

func (a *Agent) myTeam(l *espn.League) (*espn.Team, error) {
	if a.Cfg.TeamID != 0 {
		if t := l.Team(a.Cfg.TeamID); t != nil {
			return t, nil
		}
		return nil, fmt.Errorf("team %d not found in league %d", a.Cfg.TeamID, l.ID)
	}
	if t := l.TeamByOwner(espn.NormalizeSWID(a.Cfg.SWID.Reveal())); t != nil {
		return t, nil
	}
	return nil, fmt.Errorf("couldn't find your team: set team_id (ESPN_TEAM_ID) or a valid SWID")
}

// Plan builds a plan without changing anything on ESPN.
func (a *Agent) Plan(ctx context.Context) (*Plan, error) {
	l, err := a.ESPN.FetchLeague(ctx)
	if err != nil && l == nil {
		return nil, err
	}
	p := &Plan{League: l}
	if err != nil {
		p.Warnings = append(p.Warnings, err.Error())
	}
	me, err := a.myTeam(l)
	if err != nil {
		return nil, err
	}
	p.Me = me

	s := &state{a: a, l: l, me: me, values: map[int]*analysis.Valuation{}, added: map[int]bool{}}
	for _, t := range l.Teams {
		for _, pl := range t.Roster {
			s.values[pl.ID] = analysis.Value(l, pl, a.Opts)
		}
	}
	s.pool, err = a.loadPool(ctx, l)
	if err != nil {
		p.Warnings = append(p.Warnings, "free agents: "+err.Error())
	}
	for _, pl := range s.pool {
		s.values[pl.ID] = analysis.Value(l, pl, a.Opts)
	}

	work := me.Clone()
	// Set the risk preference from the current roster first so roster moves
	// are judged the same way the lineup will be.
	s.lambda = s.analyzeMatchup(work).Lambda
	p.Actions = append(p.Actions, s.planIR(work)...)
	p.Actions = append(p.Actions, s.planPickups(work)...)
	p.Roster = work

	p.Matchup = s.analyzeMatchup(work)
	s.lambda = p.Matchup.Lambda
	p.Lineup = lineup.Optimize(l, startable(work.Roster), s.weekScorer(s.lambda))
	p.Moves = lineup.Moves(work.Roster, p.Lineup)

	p.Trades = s.findTrades(work)
	p.Values = s.values
	p.Warnings = append(p.Warnings, s.warnings...)
	return p, nil
}

func (a *Agent) loadPool(ctx context.Context, l *espn.League) ([]*espn.Player, error) {
	seen := map[int]bool{}
	var out []*espn.Player
	var firstErr error
	for pos := range espn.PositionSlot {
		if l.LineupSlotCounts[espn.PositionSlot[pos]] == 0 && !flexEligible(l, pos) {
			continue // league doesn't start this position
		}
		ps, err := a.ESPN.FreeAgents(ctx, l, pos, a.Cfg.FreeAgentsPerPosition)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, p := range ps {
			if !seen[p.ID] && p.OnTeamID == 0 {
				seen[p.ID] = true
				out = append(out, p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID }) // deterministic ties
	return out, firstErr
}

func flexEligible(l *espn.League, pos string) bool {
	switch pos {
	case "RB", "WR", "TE":
		return l.LineupSlotCounts[espn.SlotFlex]+l.LineupSlotCounts[espn.SlotOP]+
			l.LineupSlotCounts[espn.SlotRBWR]+l.LineupSlotCounts[espn.SlotWRTE] > 0
	case "QB":
		return l.LineupSlotCounts[espn.SlotOP] > 0
	}
	return false
}

// startable excludes players who can't help this week's lineup because
// they are pending (e.g., a waiver claim that hasn't processed).
func startable(roster []*espn.Player) []*espn.Player {
	out := make([]*espn.Player, 0, len(roster))
	for _, p := range roster {
		if p.Status != pendingClaim {
			out = append(out, p)
		}
	}
	return out
}

// Local-only statuses for waiver claims that haven't processed yet: the
// claimed player can't start this week, and the player we'd drop still can.
const (
	pendingClaim = "PENDING_CLAIM"
	pendingDrop  = "PENDING_DROP"
)

func (s *state) val(p *espn.Player) *analysis.Valuation {
	if v, ok := s.values[p.ID]; ok {
		return v
	}
	v := analysis.Value(s.l, p, s.a.Opts)
	s.values[p.ID] = v
	return v
}

// weekScorer scores players for this week's lineup: expected points plus
// lambda standard deviations (lambda > 0 favors boom/bust upside).
func (s *state) weekScorer(lambda float64) lineup.Scorer {
	return func(p *espn.Player) float64 {
		v := s.val(p)
		return v.WeekProjection + lambda*v.WeekStdDev
	}
}

// rosScorer scores players by expected points per remaining week.
func (s *state) rosScorer() lineup.Scorer {
	weeks := float64(max(1, len(s.l.RemainingWeeks())))
	return func(p *espn.Player) float64 { return s.val(p).ROSValue / weeks }
}

// teamValue scores a roster: this week's best lineup (weighted), the best
// rest-of-season lineup over future weeks, and a little credit for depth.
func (s *state) teamValue(roster []*espn.Player, weekWeight float64) float64 {
	week := 0.0
	if weekWeight > 0 {
		week = lineup.Optimize(s.l, startable(roster), s.weekScorer(s.lambda)).Total
	}
	// Rest of season: ignore this week's locks; pending claims count.
	future := make([]*espn.Player, 0, len(roster))
	var irStash float64
	for _, p := range roster {
		if p.Status == pendingDrop {
			continue
		}
		if p.LineupSlot == espn.SlotIR {
			irStash += s.val(p).ROSValue
			continue
		}
		c := *p
		c.Locked = false
		future = append(future, &c)
	}
	ros := lineup.Optimize(s.l, future, s.rosScorer())
	var bench float64
	for _, p := range future {
		if ros.Slots[p.ID] == espn.SlotBench {
			bench += s.val(p).ROSValue
		}
	}
	futureWeeks := float64(max(0, len(s.l.RemainingWeeks())-1))
	return weekWeight*week + futureWeeks*ros.Total + s.a.Cfg.BenchDepthWeight*(bench+irStash)
}

// protected reports whether the user asked us never to move this player.
func (s *state) protected(p *espn.Player) bool {
	for _, x := range s.a.Cfg.ProtectedPlayers {
		x = strings.TrimSpace(x)
		if strings.EqualFold(x, p.Name) || x == strconv.Itoa(p.ID) {
			return true
		}
	}
	return false
}

func (s *state) irEligible(p *espn.Player) bool {
	for _, st := range s.a.Cfg.IREligibleStatuses {
		if strings.EqualFold(st, p.InjuryStatus) {
			return true
		}
	}
	return false
}

func activeCount(t *espn.Team) int {
	n := 0
	for _, p := range t.Roster {
		if p.LineupSlot != espn.SlotIR && p.Status != pendingDrop {
			n++
		}
	}
	return n
}

func (s *state) member() secrets.Secret { return espn.NormalizeSWIDSecret(s.a.Cfg.SWID) }
