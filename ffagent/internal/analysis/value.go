// Package analysis turns raw ESPN stat lines into player valuations: how a
// player has performed against projections, what to expect this week, how
// volatile they are, and their rest-of-season value.
package analysis

import (
	"fmt"
	"math"
	"sort"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
)

// Valuation is the agent's view of one player.
type Valuation struct {
	Player *espn.Player

	// History against ESPN projections.
	WeeksPlayed    int
	ActualTotal    float64 // sum of actual points in weeks with a projection
	ProjectedTotal float64 // sum of ESPN projections for those weeks
	Accuracy       float64 // ActualTotal / ProjectedTotal (1.0 = met projections)
	AccuracyFactor float64 // shrunk, clamped multiplier applied to projections
	SeasonAvg      float64 // actual points per game played
	RecentAvg      float64 // average of the last 3 games played
	WeeklyActuals  []float64
	BiggestMiss    float64 // worst actual - projected in a week
	BiggestBeat    float64 // best actual - projected in a week

	// This week.
	ESPNWeekProjection float64
	WeekProjection     float64 // adjusted expected points this week
	WeekStdDev         float64
	WeekActual         float64 // points already scored this week, when locked
	OnBye              bool
	InjuryFactor       float64

	// Rest of season.
	PerGame        float64 // expected points per game going forward
	RemainingGames int
	ROSValue       float64

	Notes []string
}

// Options tunes the valuation model.
type Options struct {
	// AccuracyPriorWeeks is how many "phantom" weeks at 100% accuracy are
	// blended in before trusting a player's projection history.
	AccuracyPriorWeeks float64
	// MinAccuracy and MaxAccuracy clamp the projection adjustment.
	MinAccuracy, MaxAccuracy float64
	// LongTermInjuryFactor scales ROS value for players on injured reserve.
	LongTermInjuryFactor float64
}

// DefaultOptions are reasonable defaults.
func DefaultOptions() Options {
	return Options{AccuracyPriorWeeks: 4, MinAccuracy: 0.8, MaxAccuracy: 1.2, LongTermInjuryFactor: 0.45}
}

// positionCV is the typical weekly coefficient of variation by position,
// used as a prior for volatility when history is thin.
var positionCV = map[string]float64{
	"QB": 0.35, "RB": 0.50, "WR": 0.55, "TE": 0.60, "K": 0.45, "D/ST": 0.65,
}

// gamesPerSeason approximates games in an NFL fantasy season.
const gamesPerSeason = 17.0

// WeekInjuryFactor is the expected fraction of a projection a player will
// score this week given their injury designation.
func WeekInjuryFactor(status string) float64 {
	switch status {
	case espn.InjuryQuestionable:
		return 0.85
	case espn.InjuryDoubtful:
		return 0.2
	case espn.InjuryOut, espn.InjuryReserve, espn.InjurySuspension:
		return 0
	case espn.InjuryDayToDay:
		return 0.9
	}
	return 1
}

// rosInjuryFactor is the expected availability over the remaining season.
func rosInjuryFactor(status string, o Options) float64 {
	switch status {
	case espn.InjuryQuestionable, espn.InjuryDayToDay:
		return 0.97
	case espn.InjuryDoubtful:
		return 0.9
	case espn.InjuryOut:
		return 0.8
	case espn.InjuryReserve:
		return o.LongTermInjuryFactor
	case espn.InjurySuspension:
		return 0.6
	}
	return 1
}

// Value computes a valuation for a player in the league's current week.
func Value(l *espn.League, p *espn.Player, o Options) *Valuation {
	v := &Valuation{Player: p, AccuracyFactor: 1, Accuracy: 1, InjuryFactor: 1}
	season, week := l.Season, l.CurrentWeek

	// Projection accuracy over completed weeks.
	minDiff, maxDiff := math.Inf(1), math.Inf(-1)
	for w := 1; w < week; w++ {
		act, okA := p.WeekActual(season, w)
		if !okA {
			continue
		}
		proj, okP := p.WeekProjection(season, w)
		if okP && proj <= 0 && act == 0 {
			continue // didn't play (bye/inactive)
		}
		v.WeeklyActuals = append(v.WeeklyActuals, act)
		if okP && proj > 0 {
			v.ActualTotal += act
			v.ProjectedTotal += proj
			d := act - proj
			minDiff = math.Min(minDiff, d)
			maxDiff = math.Max(maxDiff, d)
		}
	}
	v.WeeksPlayed = len(v.WeeklyActuals)
	if !math.IsInf(minDiff, 0) {
		v.BiggestMiss, v.BiggestBeat = minDiff, maxDiff
	}
	if v.ProjectedTotal > 0 {
		v.Accuracy = v.ActualTotal / v.ProjectedTotal
		n := float64(v.WeeksPlayed)
		shrunk := (v.ActualTotal + o.AccuracyPriorWeeks*v.ProjectedTotal/math.Max(n, 1)) /
			(v.ProjectedTotal + o.AccuracyPriorWeeks*v.ProjectedTotal/math.Max(n, 1))
		v.AccuracyFactor = clamp(shrunk, o.MinAccuracy, o.MaxAccuracy)
	}
	if v.WeeksPlayed > 0 {
		v.SeasonAvg = mean(v.WeeklyActuals)
		k := len(v.WeeklyActuals)
		v.RecentAvg = mean(v.WeeklyActuals[max(0, k-3):])
	} else if sa, ok := p.SeasonActual(season); ok && sa.Average > 0 {
		v.SeasonAvg, v.RecentAvg = sa.Average, sa.Average
	}

	// Per-game expectation going forward: blend ESPN's season projection with
	// observed production, trusting observed data more as games accumulate.
	projPerGame := 0.0
	if sp, ok := p.SeasonProjection(season); ok {
		projPerGame = sp.Average
		if projPerGame == 0 && sp.Points > 0 {
			projPerGame = sp.Points / gamesPerSeason
		}
	}
	observed := 0.6*v.SeasonAvg + 0.4*v.RecentAvg
	switch {
	case v.WeeksPlayed == 0:
		v.PerGame = projPerGame
	case projPerGame == 0:
		v.PerGame = observed
	default:
		w := float64(v.WeeksPlayed) / (float64(v.WeeksPlayed) + 4)
		v.PerGame = w*observed + (1-w)*projPerGame
	}

	// This week.
	v.OnBye = l.OnBye(p.ProTeamID, week)
	v.InjuryFactor = WeekInjuryFactor(p.InjuryStatus)
	espnProj, hasProj := p.WeekProjection(season, week)
	v.ESPNWeekProjection = espnProj
	base := espnProj
	if !hasProj {
		base = v.PerGame
	}
	if v.OnBye {
		v.WeekProjection = 0
	} else {
		v.WeekProjection = base * v.AccuracyFactor * v.InjuryFactor
	}
	if act, ok := p.WeekActual(season, week); ok && p.Locked {
		// Game is underway or done: treat banked points as the expectation.
		v.WeekActual = act
		v.WeekProjection = act
	}
	v.WeekStdDev = stdDev(v, p.Position)
	if p.Locked {
		v.WeekStdDev *= 0.3
	}

	// Rest of season.
	for w := week; w <= l.FinalWeek; w++ {
		if !l.OnBye(p.ProTeamID, w) {
			v.RemainingGames++
		}
	}
	ros := rosInjuryFactor(p.InjuryStatus, o)
	v.ROSValue = v.PerGame * v.AccuracyFactor * float64(v.RemainingGames) * ros

	v.addNotes(o)
	return v
}

func stdDev(v *Valuation, pos string) float64 {
	if v.WeekProjection <= 0 {
		return 0
	}
	cv, ok := positionCV[pos]
	if !ok {
		cv = 0.5
	}
	prior := cv * v.WeekProjection
	n := float64(len(v.WeeklyActuals))
	if n < 2 {
		return prior
	}
	m := mean(v.WeeklyActuals)
	var ss float64
	for _, x := range v.WeeklyActuals {
		ss += (x - m) * (x - m)
	}
	sample := math.Sqrt(ss / (n - 1))
	// Scale observed volatility to this week's expectation, then shrink.
	if m > 0 {
		sample = sample / m * v.WeekProjection
	}
	const k = 4.0
	return math.Sqrt((n*sample*sample + k*prior*prior) / (n + k))
}

func (v *Valuation) addNotes(o Options) {
	p := v.Player
	if v.OnBye {
		v.Notes = append(v.Notes, "on bye")
	}
	if p.InjuryStatus != espn.InjuryActive {
		v.Notes = append(v.Notes, p.InjuryStatus)
	}
	if v.WeeksPlayed >= 3 {
		switch {
		case v.Accuracy >= 1.15:
			v.Notes = append(v.Notes, fmt.Sprintf("beating projections by %.0f%%", (v.Accuracy-1)*100))
		case v.Accuracy <= 0.85:
			v.Notes = append(v.Notes, fmt.Sprintf("missing projections by %.0f%%", (1-v.Accuracy)*100))
		}
		if v.SeasonAvg > 0 {
			switch r := v.RecentAvg / v.SeasonAvg; {
			case r >= 1.25:
				v.Notes = append(v.Notes, "trending up")
			case r <= 0.75:
				v.Notes = append(v.Notes, "trending down")
			}
		}
	}
	if p.PercentChange >= 5 {
		v.Notes = append(v.Notes, fmt.Sprintf("hot pickup (+%.1f%% owned)", p.PercentChange))
	}
}

// ValueAll values a set of players and returns them keyed by player ID.
func ValueAll(l *espn.League, players []*espn.Player, o Options) map[int]*Valuation {
	out := make(map[int]*Valuation, len(players))
	for _, p := range players {
		out[p.ID] = Value(l, p, o)
	}
	return out
}

// SortByROS sorts valuations by descending rest-of-season value.
func SortByROS(vs []*Valuation) {
	sort.SliceStable(vs, func(i, j int) bool { return vs[i].ROSValue > vs[j].ROSValue })
}

// WinProbability is P(A > B) for independent normal team scores.
func WinProbability(meanA, varA, meanB, varB float64) float64 {
	sd := math.Sqrt(varA + varB)
	if sd == 0 {
		switch {
		case meanA > meanB:
			return 1
		case meanA < meanB:
			return 0
		}
		return 0.5
	}
	return 0.5 * (1 + math.Erf((meanA-meanB)/(sd*math.Sqrt2)))
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func clamp(x, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, x))
}
