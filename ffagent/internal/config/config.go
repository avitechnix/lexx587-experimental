// Package config loads agent settings from a JSON file and the environment.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config controls how the agent connects to ESPN and how aggressive it is.
type Config struct {
	LeagueID int    `json:"league_id"`
	Season   int    `json:"season"`
	TeamID   int    `json:"team_id"` // 0 = detect from SWID
	ESPNS2   string `json:"espn_s2"`
	SWID     string `json:"swid"`

	// ProtectedPlayers are never dropped or traded (names or ESPN player IDs).
	ProtectedPlayers []string `json:"protected_players"`
	// IREligibleStatuses are the injury designations your league allows on IR.
	IREligibleStatuses []string `json:"ir_eligible_statuses"`

	// Waivers / free agents.
	MaxAcquisitionsPerRun int     `json:"max_acquisitions_per_run"`
	MinPickupGain         float64 `json:"min_pickup_gain"`
	FreeAgentsPerPosition int     `json:"free_agents_per_position"`
	MaxFAABFraction       float64 `json:"max_faab_fraction"`

	// ThisWeekWeight is how much more this week's matchup counts than any
	// single future week when valuing roster moves.
	ThisWeekWeight float64 `json:"this_week_weight"`
	// BenchDepthWeight credits bench players' rest-of-season value.
	BenchDepthWeight float64 `json:"bench_depth_weight"`
	// RiskAware picks a riskier or safer lineup to maximize win probability.
	RiskAware bool `json:"risk_aware"`

	// Trades.
	MinTradeGain     float64 `json:"min_trade_gain"`
	TradeTolerance   float64 `json:"trade_tolerance"`
	MaxTradeIdeas    int     `json:"max_trade_ideas"`
	ProposeTrades    bool    `json:"propose_trades"`
	TradeExpiryHours int     `json:"trade_expiry_hours"`
}

// Default returns the default configuration.
func Default() Config {
	return Config{
		Season:                defaultSeason(time.Now()),
		IREligibleStatuses:    []string{"OUT", "INJURY_RESERVE"},
		MaxAcquisitionsPerRun: 2,
		MinPickupGain:         5,
		FreeAgentsPerPosition: 25,
		MaxFAABFraction:       0.35,
		ThisWeekWeight:        2,
		BenchDepthWeight:      0.15,
		RiskAware:             true,
		MinTradeGain:          8,
		TradeTolerance:        3,
		MaxTradeIdeas:         5,
		TradeExpiryHours:      48,
	}
}

// The NFL season spans two calendar years; ESPN keys it by the start year.
func defaultSeason(now time.Time) int {
	if now.Month() < time.March {
		return now.Year() - 1
	}
	return now.Year()
}

// Load reads an optional JSON file over the defaults, then applies
// environment overrides: ESPN_LEAGUE_ID, ESPN_SEASON, ESPN_TEAM_ID,
// ESPN_S2 and ESPN_SWID.
func Load(path string) (Config, error) {
	c := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return c, err
		}
		if err := json.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("config: %s: %w", path, err)
		}
	}
	envInt := func(key string, dst *int) error {
		if v := os.Getenv(key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("config: %s: %w", key, err)
			}
			*dst = n
		}
		return nil
	}
	for key, dst := range map[string]*int{"ESPN_LEAGUE_ID": &c.LeagueID, "ESPN_SEASON": &c.Season, "ESPN_TEAM_ID": &c.TeamID} {
		if err := envInt(key, dst); err != nil {
			return c, err
		}
	}
	if v := os.Getenv("ESPN_S2"); v != "" {
		c.ESPNS2 = v
	}
	if v := os.Getenv("ESPN_SWID"); v != "" {
		c.SWID = v
	}
	if c.LeagueID == 0 {
		return c, fmt.Errorf("config: league_id is required (set ESPN_LEAGUE_ID or league_id in the config file)")
	}
	return c, nil
}
