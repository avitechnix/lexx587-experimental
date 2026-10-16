package analysis

import (
	"fmt"
	"time"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
)

// Injury is the injury news the valuation model needs about one player.
type Injury struct {
	PlayerID  int
	ProTeamID int
	Status    string // espn.Injury* constant
	Date      time.Time
	// Returning marks players designated to return (IR-R, PUP-R, NFI-R):
	// they're coming back, so they open no opportunity for teammates.
	Returning bool
}

// opportunityShare is roughly how much of an injured starter's production
// flows to the next player up at the same position.
var opportunityShare = map[string]float64{"QB": 0.9, "RB": 0.55, "TE": 0.45, "WR": 0.3}

// gamesMissed is a conservative estimate of games lost for a designation.
func gamesMissed(status string) float64 {
	switch status {
	case espn.InjuryOut:
		return 1
	case espn.InjuryDoubtful:
		return 0.8
	case espn.InjuryReserve:
		return 4 // NFL minimum IR stay
	case espn.InjurySuspension:
		return 2
	}
	return 0
}

// ApplyOpportunity credits backups when a teammate ahead of them is newly
// injured. It only considers reports newer than freshFor: ESPN's own
// projections catch up within a day or two, and boosting after that would
// count the same opportunity twice.
func ApplyOpportunity(l *espn.League, players []*espn.Player, vals map[int]*Valuation, injuries map[int]Injury, now time.Time, freshFor time.Duration) {
	type key struct {
		team int
		pos  string
	}
	byTeamPos := map[key][]*espn.Player{}
	for _, p := range players {
		if p.ProTeamID != 0 {
			k := key{p.ProTeamID, p.Position}
			byTeamPos[k] = append(byTeamPos[k], p)
		}
	}
	for _, inj := range injuries {
		missed := gamesMissed(inj.Status)
		if missed == 0 || inj.Returning || now.Sub(inj.Date) > freshFor || inj.Date.IsZero() {
			continue
		}
		hurtV := vals[inj.PlayerID]
		if hurtV == nil {
			continue
		}
		hurt := hurtV.Player
		share, ok := opportunityShare[hurt.Position]
		if !ok || hurtV.PerGame <= 0 {
			continue
		}
		// The next healthy player up is the best remaining teammate who was
		// behind the injured player.
		var next *Valuation
		for _, p := range byTeamPos[key{hurt.ProTeamID, hurt.Position}] {
			v := vals[p.ID]
			if v == nil || p.ID == hurt.ID || WeekInjuryFactor(p.EffectiveInjury()) < 0.5 || v.PerGame >= hurtV.PerGame {
				continue
			}
			if next == nil || v.PerGame > next.PerGame {
				next = v
			}
		}
		if next == nil {
			continue
		}
		gain := share * hurtV.PerGame
		if !next.OnBye {
			next.WeekProjection += gain * next.InjuryFactor
		}
		next.ROSValue += gain * missed
		next.Notes = append(next.Notes, fmt.Sprintf("%s is %s, so he's next man up (+%.1f pts/wk)", hurt.Name, inj.Status, gain))
	}
}
