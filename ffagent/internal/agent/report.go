package agent

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/analysis"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
)

// PrintHeader prints the league and team summary.
func PrintHeader(w io.Writer, p *Plan) {
	l, me := p.League, p.Me
	fmt.Fprintf(w, "%s — week %d, %d\n", l.Name, l.CurrentWeek, l.Season)
	fmt.Fprintf(w, "Team: %s (%d-%d-%d, %.1f PF)", me.Name, me.Wins, me.Losses, me.Ties, me.PointsFor)
	if l.UsesFAAB {
		fmt.Fprintf(w, ", FAAB $%d", me.FAABRemaining)
	}
	fmt.Fprintf(w, ", waiver #%d\n", me.WaiverRank)
	for _, msg := range p.Warnings {
		fmt.Fprintf(w, "! %s\n", msg)
	}
	fmt.Fprintln(w)
}

// PrintRoster prints the roster analysis: projections, how each player has
// done against ESPN's projections, volatility and rest-of-season value.
func PrintRoster(w io.Writer, p *Plan) {
	fmt.Fprintln(w, "ROSTER ANALYSIS")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SLOT\tPLAYER\tPOS\tTEAM\tESPN\tADJ\t±SD\tACC\tAVG\tL3\tROS\tNOTES")
	roster := append([]*espn.Player(nil), p.Me.Roster...)
	sort.SliceStable(roster, func(i, j int) bool { return slotOrder(roster[i].LineupSlot) < slotOrder(roster[j].LineupSlot) })
	for _, pl := range roster {
		v := p.Values[pl.ID]
		if v == nil {
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%.1f\t%.1f\t%.1f\t%s\t%.1f\t%.1f\t%.0f\t%s\n",
			espn.SlotName(pl.LineupSlot), pl.Name, pl.Position, pl.ProTeam,
			v.ESPNWeekProjection, v.WeekProjection, v.WeekStdDev, accuracy(v),
			v.SeasonAvg, v.RecentAvg, v.ROSValue, strings.Join(v.Notes, ", "))
	}
	tw.Flush()
	fmt.Fprintln(w, "ESPN = ESPN projection; ADJ = adjusted for projection accuracy, injury, bye;")
	fmt.Fprintln(w, "ACC = actual/projected so far; L3 = last 3 games; ROS = rest-of-season points")
	fmt.Fprintln(w)
}

func accuracy(v *analysis.Valuation) string {
	if v.ProjectedTotal == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", v.Accuracy*100)
}

func slotOrder(s int) int {
	switch s {
	case espn.SlotBench:
		return 100
	case espn.SlotIR:
		return 101
	case espn.SlotFlex:
		return 7 // after TE
	}
	return s
}

// PrintMatchup prints this week's outlook.
func PrintMatchup(w io.Writer, p *Plan) {
	m := p.Matchup
	if m == nil {
		return
	}
	fmt.Fprintln(w, "MATCHUP")
	if m.Opponent == nil {
		fmt.Fprintf(w, "  %s\n\n", m.Strategy)
		return
	}
	fmt.Fprintf(w, "  vs %s (%d-%d-%d)\n", m.Opponent.Name, m.Opponent.Wins, m.Opponent.Losses, m.Opponent.Ties)
	fmt.Fprintf(w, "  You: %.1f ± %.1f   Opponent (best lineup): %.1f ± %.1f\n",
		m.MyProjected, math.Sqrt(m.MyVariance), m.OppProjected, math.Sqrt(m.OppVariance))
	fmt.Fprintf(w, "  Win probability: %.0f%%", m.WinProb*100)
	if m.WinProb > m.BaselineWinProb+0.001 {
		fmt.Fprintf(w, " (up from %.0f%% with a pure max-points lineup)", m.BaselineWinProb*100)
	}
	fmt.Fprintf(w, "\n  Strategy: %s\n\n", m.Strategy)
}

// PrintLineup prints the recommended lineup and the moves to get there.
func PrintLineup(w io.Writer, p *Plan) {
	fmt.Fprintln(w, "LINEUP")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	roster := append([]*espn.Player(nil), p.Roster.Roster...)
	sort.SliceStable(roster, func(i, j int) bool {
		return slotOrder(p.Lineup.Slots[roster[i].ID]) < slotOrder(p.Lineup.Slots[roster[j].ID])
	})
	for _, pl := range roster {
		slot, ok := p.Lineup.Slots[pl.ID]
		if !ok || !espn.IsStartingSlot(slot) {
			continue
		}
		v := p.Values[pl.ID]
		lock := ""
		if pl.Locked {
			lock = "locked"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%.1f\t%s\n", espn.SlotName(slot), pl.Name, pl.ProTeam, v.WeekProjection, lock)
	}
	tw.Flush()
	for _, s := range p.Lineup.Empty {
		fmt.Fprintf(w, "  ! no eligible player for %s\n", espn.SlotName(s))
	}
	if len(p.Moves) == 0 {
		fmt.Fprintln(w, "  (no changes needed)")
	}
	for _, m := range p.Moves {
		fmt.Fprintf(w, "  move %s: %s → %s\n", m.Player.Name, espn.SlotName(m.From), espn.SlotName(m.To))
	}
	fmt.Fprintln(w)
}

// PrintActions prints planned roster transactions.
func PrintActions(w io.Writer, p *Plan) {
	fmt.Fprintln(w, "ROSTER MOVES (IR / waivers / free agents)")
	if len(p.Actions) == 0 {
		fmt.Fprintln(w, "  none — no available player beats what you have by enough")
	}
	for i, a := range p.Actions {
		fmt.Fprintf(w, "  %d. [%s] %s\n", i+1, a.Kind, a.Summary)
		if a.Reason != "" {
			fmt.Fprintf(w, "     %s\n", a.Reason)
		}
	}
	fmt.Fprintln(w)
}

// PrintTrades prints trade ideas.
func PrintTrades(w io.Writer, p *Plan) {
	fmt.Fprintln(w, "TRADE IDEAS")
	if len(p.Trades) == 0 {
		fmt.Fprintln(w, "  none found that help you without obviously hurting the other team")
	}
	for i, t := range p.Trades {
		fmt.Fprintf(w, "  %d. With %s\n     give: %s\n     get:  %s\n     %s\n",
			i+1, t.Partner.Name, names(t.Give), names(t.Get), t.Action.Reason)
	}
	fmt.Fprintln(w)
}

// PrintFreeAgents prints the best available players at a position.
func PrintFreeAgents(w io.Writer, l *espn.League, players []*espn.Player, values map[int]*analysis.Valuation, limit int) {
	vs := make([]*analysis.Valuation, 0, len(players))
	for _, pl := range players {
		if v := values[pl.ID]; v != nil {
			vs = append(vs, v)
		}
	}
	analysis.SortByROS(vs)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PLAYER\tPOS\tTEAM\tSTATUS\tOWN%\tWEEK\tACC\tAVG\tL3\tROS\tNOTES")
	for i, v := range vs {
		if i >= limit {
			break
		}
		pl := v.Player
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%.1f\t%.1f\t%s\t%.1f\t%.1f\t%.0f\t%s\n",
			pl.Name, pl.Position, pl.ProTeam, pl.Status, pl.PercentOwned, v.WeekProjection,
			accuracy(v), v.SeasonAvg, v.RecentAvg, v.ROSValue, strings.Join(v.Notes, ", "))
	}
	tw.Flush()
}
