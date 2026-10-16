package agent

import (
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/analysis"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/lineup"
)

// MatchupReport summarizes this week's head-to-head outlook.
type MatchupReport struct {
	Opponent        *espn.Team
	MyProjected     float64
	MyVariance      float64
	OppProjected    float64
	OppVariance     float64
	WinProb         float64 // with the chosen lineup
	BaselineWinProb float64 // with the max-projected-points lineup
	Lambda          float64 // risk preference used for the lineup
	Strategy        string
	OppLineup       lineup.Assignment
}

// lambdas are the risk preferences tried when building the lineup; 0 is
// first so ties keep the plain max-points lineup.
var lambdas = []float64{0, -0.5, -0.25, 0.25, 0.5, 0.75, 1}

// analyzeMatchup projects both teams and picks the lineup risk profile that
// maximizes the probability of winning, not just expected points: an
// underdog wants variance (boom/bust starters), a favorite wants floor.
func (s *state) analyzeMatchup(me *espn.Team) *MatchupReport {
	r := &MatchupReport{}
	mine := startable(me.Roster)
	base := lineup.Optimize(s.l, mine, s.weekScorer(0))
	r.MyProjected, r.MyVariance = s.lineupStats(mine, base)

	oppID, ok := s.l.Opponent(me.ID, s.l.CurrentMatchupPeriod)
	if !ok {
		r.Strategy = "no opponent this week; maximizing projected points"
		return r
	}
	r.Opponent = s.l.Team(oppID)
	// Plan against the opponent's best possible lineup, not their current one.
	r.OppLineup = lineup.Optimize(s.l, r.Opponent.Roster, s.weekScorer(0))
	r.OppProjected, r.OppVariance = s.lineupStats(r.Opponent.Roster, r.OppLineup)
	r.BaselineWinProb = analysis.WinProbability(r.MyProjected, r.MyVariance, r.OppProjected, r.OppVariance)
	r.WinProb = r.BaselineWinProb

	if s.a.Cfg.RiskAware {
		for _, lam := range lambdas[1:] {
			a := lineup.Optimize(s.l, mine, s.weekScorer(lam))
			m, v := s.lineupStats(mine, a)
			if p := analysis.WinProbability(m, v, r.OppProjected, r.OppVariance); p > r.WinProb+0.002 {
				r.WinProb, r.Lambda, r.MyProjected, r.MyVariance = p, lam, m, v
			}
		}
	}
	switch {
	case r.Lambda > 0:
		r.Strategy = "underdog: favoring high-ceiling starters to boost win chances"
	case r.Lambda < 0:
		r.Strategy = "favorite: favoring high-floor starters to protect the lead"
	default:
		r.Strategy = "maximizing projected points"
	}
	return r
}

// lineupStats returns expected points and variance for a lineup's starters.
func (s *state) lineupStats(roster []*espn.Player, a lineup.Assignment) (mean, variance float64) {
	for _, p := range roster {
		if slot, ok := a.Slots[p.ID]; ok && espn.IsStartingSlot(slot) {
			v := s.val(p)
			mean += v.WeekProjection
			variance += v.WeekStdDev * v.WeekStdDev
		}
	}
	return mean, variance
}

// currentOutlook projects the lineup exactly as it's set on ESPN now.
func (s *state) currentOutlook(me *espn.Team) (proj, winProb float64) {
	cur := lineup.Assignment{Slots: map[int]int{}}
	for _, p := range me.Roster {
		cur.Slots[p.ID] = p.LineupSlot
	}
	proj, v := s.lineupStats(me.Roster, cur)
	oppID, ok := s.l.Opponent(me.ID, s.l.CurrentMatchupPeriod)
	if !ok {
		return proj, 0
	}
	opp := s.l.Team(oppID)
	oa := lineup.Optimize(s.l, opp.Roster, s.weekScorer(0))
	om, ov := s.lineupStats(opp.Roster, oa)
	return proj, analysis.WinProbability(proj, v, om, ov)
}
