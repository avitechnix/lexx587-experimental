package analysis

import (
	"math"
	"testing"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
)

const season = 2026

func league(week int) *espn.League {
	return &espn.League{Season: season, CurrentWeek: week, FinalWeek: 17, ByeWeeks: map[int]int{1: 9}}
}

// withHistory builds a player with weekly projections and actuals for
// weeks 1..len(actual), plus a projection for the next week.
func withHistory(pos string, proj, actual []float64, next float64) *espn.Player {
	p := &espn.Player{ID: 1, Name: "Test", Position: pos, ProTeamID: 1, InjuryStatus: espn.InjuryActive}
	for i := range actual {
		w := i + 1
		p.Stats = append(p.Stats,
			espn.StatLine{Season: season, Week: w, Weekly: true, Projected: true, Points: proj[i]},
			espn.StatLine{Season: season, Week: w, Weekly: true, Points: actual[i]})
	}
	p.Stats = append(p.Stats,
		espn.StatLine{Season: season, Week: len(actual) + 1, Weekly: true, Projected: true, Points: next},
		espn.StatLine{Season: season, Projected: true, Points: 17 * next, Average: next})
	return p
}

func TestOutperformerGetsBoostedButShrunk(t *testing.T) {
	// Projected 10 every week, scored 15: 150% accuracy over 5 weeks.
	p := withHistory("WR", []float64{10, 10, 10, 10, 10}, []float64{15, 15, 15, 15, 15}, 10)
	v := Value(league(6), p, DefaultOptions())
	if math.Abs(v.Accuracy-1.5) > 1e-9 {
		t.Fatalf("accuracy %.3f, want 1.5", v.Accuracy)
	}
	if v.AccuracyFactor <= 1 || v.AccuracyFactor > 1.2 {
		t.Errorf("accuracy factor %.3f should be in (1, 1.2]", v.AccuracyFactor)
	}
	if v.WeekProjection <= v.ESPNWeekProjection {
		t.Errorf("adjusted %.1f should beat ESPN %.1f", v.WeekProjection, v.ESPNWeekProjection)
	}
	if v.PerGame <= 10 {
		t.Errorf("per-game %.1f should lean toward observed 15", v.PerGame)
	}
}

func TestInjuryAndBye(t *testing.T) {
	p := withHistory("RB", []float64{12, 12}, []float64{12, 12}, 12)
	p.InjuryStatus = espn.InjuryOut
	v := Value(league(3), p, DefaultOptions())
	if v.WeekProjection != 0 {
		t.Errorf("OUT player projected %.1f, want 0", v.WeekProjection)
	}
	if v.ROSValue <= 0 {
		t.Errorf("OUT player should keep ROS value")
	}

	p2 := withHistory("RB", nil, nil, 12)
	v2 := Value(league(9), p2, DefaultOptions())
	if !v2.OnBye || v2.WeekProjection != 0 {
		t.Errorf("bye week not detected: %+v", v2)
	}
	if v2.RemainingGames != 8 { // weeks 9..17 minus the bye
		t.Errorf("remaining games %d, want 8", v2.RemainingGames)
	}
}

func TestWinProbability(t *testing.T) {
	if p := WinProbability(100, 100, 100, 100); math.Abs(p-0.5) > 1e-9 {
		t.Errorf("even matchup = %.3f", p)
	}
	if p := WinProbability(110, 50, 100, 50); p < 0.8 || p > 0.9 {
		t.Errorf("10 pt favorite with sd 10 = %.3f, want ~0.84", p)
	}
}
