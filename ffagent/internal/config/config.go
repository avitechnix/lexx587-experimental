// Package config loads agent settings from a JSON file and the environment.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // time zones work even on minimal servers

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/secrets"
)

// Config controls how the agent connects to ESPN and how aggressive it is.
type Config struct {
	LeagueID int `json:"league_id"`
	Season   int `json:"season"`
	TeamID   int `json:"team_id"` // 0 = detect from SWID

	// Credentials come only from the encrypted vault (see LoadSecrets);
	// they are never read from the config file or the environment.
	ESPNS2        secrets.Secret `json:"-"`
	SWID          secrets.Secret `json:"-"`
	EmailPassword secrets.Secret `json:"-"`

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

	// News and watch mode.
	// NewsFreshHours is how long an injury report counts as breaking news
	// (before ESPN projections catch up) for boosting the next player up.
	NewsFreshHours int `json:"news_fresh_hours"`
	// KeepNewPickupsDays protects the agent's own pickups from being dropped.
	KeepNewPickupsDays int `json:"keep_new_pickups_days"`
	// MaxAcquisitionsPerDay caps adds and claims made by watch mode.
	MaxAcquisitionsPerDay int `json:"max_acquisitions_per_day"`
	// WatchIntervalMinutes is how often watch mode polls the news.
	WatchIntervalMinutes int `json:"watch_interval_minutes"`
	// FullReplanMinutes forces a full re-plan even without news.
	FullReplanMinutes int `json:"full_replan_minutes"`

	// Approvals. In "email" mode (the default) the watcher never acts on
	// its own: it emails each move with its rationale and waits for a reply.
	// "auto" lets the watcher act without asking.
	ApprovalMode          string `json:"approval_mode"`
	ApprovalTTLHours      int    `json:"approval_ttl_hours"`
	DeclinedCooldownHours int    `json:"declined_cooldown_hours"`

	Email EmailConfig `json:"email"`

	// Trades.
	MinTradeGain     float64 `json:"min_trade_gain"`
	TradeTolerance   float64 `json:"trade_tolerance"`
	MaxTradeIdeas    int     `json:"max_trade_ideas"`
	ProposeTrades    bool    `json:"propose_trades"`
	TradeExpiryHours int     `json:"trade_expiry_hours"`
}

// EmailConfig is where proposals and summaries are sent. The account's app
// password lives only in the encrypted vault.
type EmailConfig struct {
	// Address is the mailbox the agent sends from and reads your replies in.
	// A dedicated account is recommended.
	Address string `json:"address"`
	// NotifyTo is your address. Replies are only accepted from it.
	NotifyTo   string `json:"notify_to"`
	SMTPHost   string `json:"smtp_host"`
	SMTPPort   int    `json:"smtp_port"`
	IMAPHost   string `json:"imap_host"`
	IMAPPort   int    `json:"imap_port"`
	DigestHour int    `json:"digest_hour"` // local hour for the daily summary
	TimeZone   string `json:"time_zone"`
}

// Enabled reports whether email is configured.
func (e EmailConfig) Enabled() bool { return e.Address != "" && e.NotifyTo != "" }

// Location returns the configured time zone (default US Eastern).
func (e EmailConfig) Location() *time.Location {
	if loc, err := time.LoadLocation(e.TimeZone); err == nil && e.TimeZone != "" {
		return loc
	}
	if loc, err := time.LoadLocation("America/New_York"); err == nil {
		return loc
	}
	return time.Local
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
		NewsFreshHours:        48,
		KeepNewPickupsDays:    7,
		MaxAcquisitionsPerDay: 3,
		WatchIntervalMinutes:  5,
		FullReplanMinutes:     60,
		ApprovalMode:          "email",
		ApprovalTTLHours:      12,
		DeclinedCooldownHours: 24,
		Email: EmailConfig{
			SMTPHost: "smtp.gmail.com", SMTPPort: 587,
			IMAPHost: "imap.gmail.com", IMAPPort: 993,
			DigestHour: 9, TimeZone: "America/New_York",
		},
		MinTradeGain:     8,
		TradeTolerance:   3,
		MaxTradeIdeas:    5,
		TradeExpiryHours: 48,
	}
}

// The NFL season spans two calendar years; ESPN keys it by the start year.
func defaultSeason(now time.Time) int {
	if now.Month() < time.March {
		return now.Year() - 1
	}
	return now.Year()
}

// Vault keys for the credentials.
const (
	VaultKeyESPNS2 = "espn_s2"
	VaultKeySWID   = "swid"
	VaultKeyEmail  = "email_password"
)

// plaintextKeys are config-file fields and environment variables that would
// hold credentials in plaintext. Their presence is an error, not a fallback.
var (
	plaintextKeys = []string{"espn_s2", "swid", "espn_swid", "s2"}
	plaintextEnv  = []string{"ESPN_S2", "ESPN_SWID", "SWID"}
)

// secretKey returns the first key anywhere in a JSON document that would
// hold a credential.
func secretKey(v any) string {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			lk := strings.ToLower(k)
			for _, bad := range plaintextKeys {
				if lk == bad {
					return k
				}
			}
			for _, frag := range []string{"password", "passwd", "secret", "token", "cookie", "app_pass"} {
				if strings.Contains(lk, frag) {
					return k
				}
			}
			if k := secretKey(child); k != "" {
				return k
			}
		}
	case []any:
		for _, child := range t {
			if k := secretKey(child); k != "" {
				return k
			}
		}
	}
	return ""
}

// ErrPlaintextSecret is returned when credentials appear in plaintext.
var ErrPlaintextSecret = fmt.Errorf("config: plaintext credentials are not allowed; store them encrypted with `ffagent secrets set` and remove them")

// Load reads an optional JSON file over the defaults, then applies
// environment overrides: ESPN_LEAGUE_ID, ESPN_SEASON and ESPN_TEAM_ID.
// It refuses to run if credentials appear in the file or the environment.
func Load(path string) (Config, error) {
	c := Default()
	for _, k := range plaintextEnv {
		if _, ok := os.LookupEnv(k); ok {
			os.Unsetenv(k)
			return c, fmt.Errorf("%w (found environment variable %s)", ErrPlaintextSecret, k)
		}
	}
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return c, err
		}
		var raw any
		if err := json.Unmarshal(b, &raw); err != nil {
			return c, fmt.Errorf("config: %s: %w", path, err)
		}
		if k := secretKey(raw); k != "" {
			return c, fmt.Errorf("%w (found %q in %s)", ErrPlaintextSecret, k, path)
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
	switch c.ApprovalMode {
	case "email", "auto":
	default:
		return c, fmt.Errorf(`config: approval_mode must be "email" or "auto"`)
	}
	c.Email.NotifyTo = strings.ToLower(strings.TrimSpace(c.Email.NotifyTo))
	if c.LeagueID == 0 {
		return c, fmt.Errorf("config: league_id is required (set ESPN_LEAGUE_ID or league_id in the config file)")
	}
	return c, nil
}

// LoadSecrets decrypts the vault and fills in the credentials.
func (c *Config) LoadSecrets(vaultPath string) error {
	pass, err := secrets.Passphrase()
	if err != nil {
		return err
	}
	defer func() {
		for i := range pass {
			pass[i] = 0
		}
	}()
	vals, err := secrets.ReadVault(vaultPath, pass)
	if err != nil {
		return err
	}
	c.ESPNS2, c.SWID = vals[VaultKeyESPNS2], vals[VaultKeySWID]
	c.EmailPassword = vals[VaultKeyEmail]
	return nil
}
