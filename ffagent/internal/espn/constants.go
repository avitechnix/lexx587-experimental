package espn

// Lineup slot IDs used by ESPN fantasy football.
const (
	SlotQB      = 0
	SlotTQB     = 1
	SlotRB      = 2
	SlotRBWR    = 3
	SlotWR      = 4
	SlotWRTE    = 5
	SlotTE      = 6
	SlotOP      = 7 // offensive player / superflex
	SlotDT      = 8
	SlotDE      = 9
	SlotLB      = 10
	SlotDL      = 11
	SlotCB      = 12
	SlotS       = 13
	SlotDB      = 14
	SlotDP      = 15
	SlotDST     = 16
	SlotK       = 17
	SlotP       = 18
	SlotHC      = 19
	SlotBench   = 20
	SlotIR      = 21
	SlotFlex    = 23 // RB/WR/TE
	SlotUnknown = -1
)

// SlotNames maps lineup slot IDs to display names.
var SlotNames = map[int]string{
	SlotQB: "QB", SlotTQB: "TQB", SlotRB: "RB", SlotRBWR: "RB/WR", SlotWR: "WR",
	SlotWRTE: "WR/TE", SlotTE: "TE", SlotOP: "OP", SlotDT: "DT", SlotDE: "DE",
	SlotLB: "LB", SlotDL: "DL", SlotCB: "CB", SlotS: "S", SlotDB: "DB", SlotDP: "DP",
	SlotDST: "D/ST", SlotK: "K", SlotP: "P", SlotHC: "HC", SlotBench: "BE",
	SlotIR: "IR", SlotFlex: "FLEX", 24: "ER",
}

// SlotName returns the display name for a slot.
func SlotName(id int) string {
	if n, ok := SlotNames[id]; ok {
		return n
	}
	return "?"
}

// IsStartingSlot reports whether a slot counts toward the weekly score.
func IsStartingSlot(id int) bool {
	return id != SlotBench && id != SlotIR && id != 22 && id != SlotUnknown
}

// positionNames maps ESPN defaultPositionId to a position name.
var positionNames = map[int]string{
	1: "QB", 2: "RB", 3: "WR", 4: "TE", 5: "K", 7: "P", 9: "DT", 10: "DE",
	11: "LB", 12: "CB", 13: "S", 14: "HC", 16: "D/ST",
}

// PositionSlot maps a position name to its primary lineup slot, which is
// also the slot filter ESPN uses when searching free agents.
var PositionSlot = map[string]int{
	"QB": SlotQB, "RB": SlotRB, "WR": SlotWR, "TE": SlotTE, "K": SlotK, "D/ST": SlotDST,
}

// PositionName returns a display name for an ESPN defaultPositionId.
func PositionName(id int) string {
	if n, ok := positionNames[id]; ok {
		return n
	}
	return "UNK"
}

// proTeamAbbrevs is a fallback for NFL team abbreviations; the pro schedule
// endpoint is the primary source.
var proTeamAbbrevs = map[int]string{
	0: "FA", 1: "ATL", 2: "BUF", 3: "CHI", 4: "CIN", 5: "CLE", 6: "DAL", 7: "DEN",
	8: "DET", 9: "GB", 10: "TEN", 11: "IND", 12: "KC", 13: "LV", 14: "LAR", 15: "MIA",
	16: "MIN", 17: "NE", 18: "NO", 19: "NYG", 20: "NYJ", 21: "PHI", 22: "ARI", 23: "PIT",
	24: "LAC", 25: "SF", 26: "SEA", 27: "TB", 28: "WSH", 29: "CAR", 30: "JAX", 33: "BAL",
	34: "HOU",
}

// Injury statuses reported by ESPN.
const (
	InjuryActive       = "ACTIVE"
	InjuryQuestionable = "QUESTIONABLE"
	InjuryDoubtful     = "DOUBTFUL"
	InjuryOut          = "OUT"
	InjuryReserve      = "INJURY_RESERVE"
	InjurySuspension   = "SUSPENSION"
	InjuryDayToDay     = "DAY_TO_DAY"
)

// Stat source and split identifiers in ESPN stat lines.
const (
	statSourceActual    = 0
	statSourceProjected = 1
	statSplitSeason     = 0
	statSplitWeek       = 1
)

// Player pool statuses.
const (
	StatusFreeAgent = "FREEAGENT"
	StatusWaivers   = "WAIVERS"
	StatusOnTeam    = "ONTEAM"
)
