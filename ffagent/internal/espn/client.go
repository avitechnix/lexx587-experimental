// Package espn is a client for ESPN's (unofficial, undocumented) fantasy
// football v3 API. It reads league state and submits roster transactions.
package espn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultReadBase  = "https://lm-api-reads.fantasy.espn.com/apis/v3/games/ffl"
	DefaultWriteBase = "https://lm-api-writes.fantasy.espn.com/apis/v3/games/ffl"
)

// Client talks to the ESPN fantasy API for one league and season.
type Client struct {
	LeagueID  int
	Season    int
	ESPNS2    string // espn_s2 cookie (required for private leagues and writes)
	SWID      string // SWID cookie, including braces
	ReadBase  string
	WriteBase string
	HTTP      *http.Client
	Now       func() time.Time
}

// NewClient returns a client with default endpoints.
func NewClient(leagueID, season int, espnS2, swid string) *Client {
	return &Client{
		LeagueID:  leagueID,
		Season:    season,
		ESPNS2:    espnS2,
		SWID:      NormalizeSWID(swid),
		ReadBase:  DefaultReadBase,
		WriteBase: DefaultWriteBase,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		Now:       time.Now,
	}
}

// NormalizeSWID ensures the SWID is wrapped in braces, as ESPN expects.
func NormalizeSWID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if !strings.HasPrefix(s, "{") {
		s = "{" + s
	}
	if !strings.HasSuffix(s, "}") {
		s += "}"
	}
	return strings.ToUpper(s)
}

func (c *Client) leaguePath(base string) string {
	return fmt.Sprintf("%s/seasons/%d/segments/0/leagues/%d", base, c.Season, c.LeagueID)
}

func (c *Client) do(ctx context.Context, method, rawURL string, body any, headers map[string]string, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c.ESPNS2 != "" {
		req.AddCookie(&http.Cookie{Name: "espn_s2", Value: c.ESPNS2})
	}
	if c.SWID != "" {
		req.AddCookie(&http.Cookie{Name: "SWID", Value: c.SWID})
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("espn: %s %s: %d (check ESPN_S2 and SWID cookies)", method, req.URL.Path, resp.StatusCode)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("espn: %s %s: %d: %s", method, req.URL.Path, resp.StatusCode, truncate(string(data), 500))
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("espn: decoding %s: %w", req.URL.Path, err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func viewsQuery(scoringPeriod int, views ...string) string {
	q := url.Values{}
	for _, v := range views {
		q.Add("view", v)
	}
	if scoringPeriod > 0 {
		q.Set("scoringPeriodId", strconv.Itoa(scoringPeriod))
	}
	return q.Encode()
}

// FetchLeague loads settings, teams, rosters, schedule and pro schedule.
// Roster player stats are enriched with weekly history via the player card view.
func (c *Client) FetchLeague(ctx context.Context) (*League, error) {
	var raw rawLeague
	u := c.leaguePath(c.ReadBase) + "?" + viewsQuery(0, "mSettings", "mStatus", "mTeam", "mRoster", "mMatchup")
	if err := c.do(ctx, http.MethodGet, u, nil, nil, &raw); err != nil {
		return nil, err
	}
	// Re-fetch rosters for the current scoring period so weekly projections
	// and lineup locks reflect this week.
	if raw.ScoringPeriodID > 0 {
		var cur rawLeague
		u := c.leaguePath(c.ReadBase) + "?" + viewsQuery(raw.ScoringPeriodID, "mRoster")
		if err := c.do(ctx, http.MethodGet, u, nil, nil, &cur); err == nil && len(cur.Teams) > 0 {
			byID := map[int]rawTeam{}
			for _, t := range cur.Teams {
				byID[t.ID] = t
			}
			for i, t := range raw.Teams {
				if ct, ok := byID[t.ID]; ok {
					raw.Teams[i].Roster = ct.Roster
				}
			}
		}
	}
	league := raw.toLeague()

	if err := c.loadProSchedule(ctx, league); err != nil {
		// Bye weeks are helpful but not essential.
		league.ByeWeeks = map[int]int{}
	}
	now := c.Now()
	for _, t := range league.Teams {
		for _, p := range t.Roster {
			if league.GameStarted(p.ProTeamID, league.CurrentWeek, now) {
				p.Locked = true
			}
			p.ProTeam = league.ProTeamAbbrev(p.ProTeamID)
		}
	}

	var ids []int
	for _, t := range league.Teams {
		for _, p := range t.Roster {
			ids = append(ids, p.ID)
		}
	}
	if err := c.enrichStats(ctx, league, ids); err != nil {
		return league, fmt.Errorf("espn: loading player history: %w", err)
	}
	return league, nil
}

func (c *Client) loadProSchedule(ctx context.Context, l *League) error {
	var raw rawProSchedule
	u := fmt.Sprintf("%s/seasons/%d?view=proTeamSchedules_wl", c.ReadBase, c.Season)
	if err := c.do(ctx, http.MethodGet, u, nil, nil, &raw); err != nil {
		return err
	}
	l.ByeWeeks = map[int]int{}
	l.ProGames = map[int]map[int]ProGame{}
	l.ProTeamAbbrevs = map[int]string{}
	for _, pt := range raw.Settings.ProTeams {
		l.ProTeamAbbrevs[pt.ID] = strings.ToUpper(pt.Abbrev)
		if pt.ByeWeek > 0 {
			l.ByeWeeks[pt.ID] = pt.ByeWeek
		}
		games := map[int]ProGame{}
		for wk, gs := range pt.ProGamesByScoringPeriod {
			w, err := strconv.Atoi(wk)
			if err != nil || len(gs) == 0 {
				continue
			}
			g := gs[0]
			games[w] = ProGame{Week: w, Start: time.UnixMilli(g.Date), HomeID: g.HomeProTeamID, AwayID: g.AwayProTeamID}
		}
		if len(games) > 0 {
			l.ProGames[pt.ID] = games
		}
	}
	return nil
}

// statsFilterValue asks ESPN to include weekly stat lines (actual and
// projected) plus season totals and projections in player responses.
func (c *Client) statsFilterValue() map[string]any {
	y := strconv.Itoa(c.Season)
	return map[string]any{
		"value":           20,
		"additionalValue": []string{"00" + y, "10" + y, "002" + y, "102" + y},
	}
}

func (c *Client) enrichStats(ctx context.Context, l *League, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	filter := map[string]any{
		"players": map[string]any{
			"filterIds":                         map[string]any{"value": ids},
			"filterStatsForTopScoringPeriodIds": c.statsFilterValue(),
		},
	}
	b, _ := json.Marshal(filter)
	var resp rawPlayersResponse
	u := c.leaguePath(c.ReadBase) + "?" + viewsQuery(l.CurrentWeek, "kona_playercard")
	if err := c.do(ctx, http.MethodGet, u, nil, map[string]string{"x-fantasy-filter": string(b)}, &resp); err != nil {
		return err
	}
	byID := map[int][]StatLine{}
	for _, e := range resp.Players {
		byID[e.Player.ID] = e.Player.statLines()
	}
	for _, t := range l.Teams {
		for _, p := range t.Roster {
			if s, ok := byID[p.ID]; ok && len(s) > 0 {
				p.Stats = mergeStats(p.Stats, s)
			}
		}
	}
	return nil
}

func mergeStats(a, b []StatLine) []StatLine {
	type key struct {
		season, week int
		proj, weekly bool
	}
	seen := map[key]int{}
	out := append([]StatLine(nil), a...)
	for i, s := range out {
		seen[key{s.Season, s.Week, s.Projected, s.Weekly}] = i
	}
	for _, s := range b {
		k := key{s.Season, s.Week, s.Projected, s.Weekly}
		if i, ok := seen[k]; ok {
			out[i] = s
			continue
		}
		seen[k] = len(out)
		out = append(out, s)
	}
	return out
}

// FreeAgents returns available players (free agents and players on waivers)
// at the given position, sorted by ownership. Pass position "" for all.
func (c *Client) FreeAgents(ctx context.Context, l *League, position string, limit int) ([]*Player, error) {
	players := map[string]any{
		"filterStatus":                      map[string]any{"value": []string{StatusFreeAgent, StatusWaivers}},
		"limit":                             limit,
		"sortPercOwned":                     map[string]any{"sortPriority": 1, "sortAsc": false},
		"sortDraftRanks":                    map[string]any{"sortPriority": 100, "sortAsc": true, "value": "STANDARD"},
		"filterStatsForTopScoringPeriodIds": c.statsFilterValue(),
	}
	if slot, ok := PositionSlot[position]; ok {
		players["filterSlotIds"] = map[string]any{"value": []int{slot}}
	}
	b, _ := json.Marshal(map[string]any{"players": players})
	var resp rawPlayersResponse
	u := c.leaguePath(c.ReadBase) + "?" + viewsQuery(l.CurrentWeek, "kona_player_info")
	if err := c.do(ctx, http.MethodGet, u, nil, map[string]string{"x-fantasy-filter": string(b)}, &resp); err != nil {
		return nil, err
	}
	now := c.Now()
	out := make([]*Player, 0, len(resp.Players))
	for _, e := range resp.Players {
		p := e.toPlayer(SlotUnknown, "")
		p.ProTeam = l.ProTeamAbbrev(p.ProTeamID)
		p.Locked = l.GameStarted(p.ProTeamID, l.CurrentWeek, now)
		out = append(out, p)
	}
	return out, nil
}

// --- raw API types ---

type rawLeague struct {
	ID              int `json:"id"`
	SeasonID        int `json:"seasonId"`
	ScoringPeriodID int `json:"scoringPeriodId"`
	Status          struct {
		CurrentMatchupPeriod int `json:"currentMatchupPeriod"`
		FinalScoringPeriod   int `json:"finalScoringPeriod"`
		LatestScoringPeriod  int `json:"latestScoringPeriod"`
	} `json:"status"`
	Settings struct {
		Name           string `json:"name"`
		RosterSettings struct {
			LineupSlotCounts map[string]int `json:"lineupSlotCounts"`
		} `json:"rosterSettings"`
		AcquisitionSettings struct {
			AcquisitionBudget        int  `json:"acquisitionBudget"`
			IsUsingAcquisitionBudget bool `json:"isUsingAcquisitionBudget"`
		} `json:"acquisitionSettings"`
		ScheduleSettings struct {
			MatchupPeriods map[string][]int `json:"matchupPeriods"`
		} `json:"scheduleSettings"`
	} `json:"settings"`
	Members []struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	} `json:"members"`
	Teams    []rawTeam `json:"teams"`
	Schedule []struct {
		MatchupPeriodID int `json:"matchupPeriodId"`
		Home            struct {
			TeamID      int     `json:"teamId"`
			TotalPoints float64 `json:"totalPoints"`
		} `json:"home"`
		Away struct {
			TeamID      int     `json:"teamId"`
			TotalPoints float64 `json:"totalPoints"`
		} `json:"away"`
	} `json:"schedule"`
}

type rawTeam struct {
	ID           int      `json:"id"`
	Abbrev       string   `json:"abbrev"`
	Name         string   `json:"name"`
	Location     string   `json:"location"`
	Nickname     string   `json:"nickname"`
	Owners       []string `json:"owners"`
	PrimaryOwner string   `json:"primaryOwner"`
	WaiverRank   int      `json:"waiverRank"`
	Record       struct {
		Overall struct {
			Wins      int     `json:"wins"`
			Losses    int     `json:"losses"`
			Ties      int     `json:"ties"`
			PointsFor float64 `json:"pointsFor"`
		} `json:"overall"`
	} `json:"record"`
	TransactionCounter struct {
		AcquisitionBudgetSpent int `json:"acquisitionBudgetSpent"`
	} `json:"transactionCounter"`
	Roster struct {
		Entries []struct {
			PlayerID        int          `json:"playerId"`
			LineupSlotID    int          `json:"lineupSlotId"`
			InjuryStatus    string       `json:"injuryStatus"`
			PlayerPoolEntry rawPoolEntry `json:"playerPoolEntry"`
		} `json:"entries"`
	} `json:"roster"`
}

type rawPoolEntry struct {
	ID           int       `json:"id"`
	OnTeamID     int       `json:"onTeamId"`
	Status       string    `json:"status"`
	LineupLocked bool      `json:"lineupLocked"`
	Player       rawPlayer `json:"player"`
}

type rawPlayer struct {
	ID                int       `json:"id"`
	FullName          string    `json:"fullName"`
	DefaultPositionID int       `json:"defaultPositionId"`
	EligibleSlots     []int     `json:"eligibleSlots"`
	ProTeamID         int       `json:"proTeamId"`
	Injured           bool      `json:"injured"`
	InjuryStatus      string    `json:"injuryStatus"`
	Stats             []rawStat `json:"stats"`
	Ownership         struct {
		PercentOwned  float64 `json:"percentOwned"`
		PercentChange float64 `json:"percentChange"`
	} `json:"ownership"`
}

type rawStat struct {
	SeasonID        int     `json:"seasonId"`
	ScoringPeriodID int     `json:"scoringPeriodId"`
	StatSourceID    int     `json:"statSourceId"`
	StatSplitTypeID int     `json:"statSplitTypeId"`
	AppliedTotal    float64 `json:"appliedTotal"`
	AppliedAverage  float64 `json:"appliedAverage"`
}

type rawPlayersResponse struct {
	Players []rawPoolEntry `json:"players"`
}

type rawProSchedule struct {
	Settings struct {
		ProTeams []struct {
			ID                      int    `json:"id"`
			Abbrev                  string `json:"abbrev"`
			ByeWeek                 int    `json:"byeWeek"`
			ProGamesByScoringPeriod map[string][]struct {
				Date          int64 `json:"date"`
				HomeProTeamID int   `json:"homeProTeamId"`
				AwayProTeamID int   `json:"awayProTeamId"`
			} `json:"proGamesByScoringPeriod"`
		} `json:"proTeams"`
	} `json:"settings"`
}

func (rp rawPlayer) statLines() []StatLine {
	out := make([]StatLine, 0, len(rp.Stats))
	for _, s := range rp.Stats {
		if s.StatSplitTypeID != statSplitSeason && s.StatSplitTypeID != statSplitWeek {
			continue
		}
		if s.StatSourceID != statSourceActual && s.StatSourceID != statSourceProjected {
			continue
		}
		weekly := s.StatSplitTypeID == statSplitWeek
		wk := 0
		if weekly {
			wk = s.ScoringPeriodID
		}
		out = append(out, StatLine{
			Season:    s.SeasonID,
			Week:      wk,
			Projected: s.StatSourceID == statSourceProjected,
			Weekly:    weekly,
			Points:    s.AppliedTotal,
			Average:   s.AppliedAverage,
		})
	}
	return out
}

func (e rawPoolEntry) toPlayer(slot int, injury string) *Player {
	rp := e.Player
	status := rp.InjuryStatus
	if status == "" {
		status = injury
	}
	if status == "" || status == "NORMAL" {
		status = InjuryActive
	}
	ps := e.Status
	if ps == "" && e.OnTeamID > 0 {
		ps = StatusOnTeam
	}
	return &Player{
		ID:            rp.ID,
		Name:          rp.FullName,
		Position:      PositionName(rp.DefaultPositionID),
		ProTeamID:     rp.ProTeamID,
		ProTeam:       proTeamAbbrevs[rp.ProTeamID],
		EligibleSlots: rp.EligibleSlots,
		InjuryStatus:  status,
		Injured:       rp.Injured,
		PercentOwned:  rp.Ownership.PercentOwned,
		PercentChange: rp.Ownership.PercentChange,
		OnTeamID:      e.OnTeamID,
		Status:        ps,
		LineupSlot:    slot,
		Locked:        e.LineupLocked,
		Stats:         rp.statLines(),
	}
}

func (r rawLeague) toLeague() *League {
	l := &League{
		ID:                   r.ID,
		Name:                 r.Settings.Name,
		Season:               r.SeasonID,
		CurrentWeek:          r.ScoringPeriodID,
		CurrentMatchupPeriod: r.Status.CurrentMatchupPeriod,
		FinalWeek:            r.Status.FinalScoringPeriod,
		LineupSlotCounts:     map[int]int{},
		MatchupPeriods:       map[int][]int{},
		UsesFAAB:             r.Settings.AcquisitionSettings.IsUsingAcquisitionBudget,
		FAABBudget:           r.Settings.AcquisitionSettings.AcquisitionBudget,
		Members:              map[string]string{},
		ByeWeeks:             map[int]int{},
		ProGames:             map[int]map[int]ProGame{},
	}
	if l.FinalWeek == 0 {
		l.FinalWeek = 18
	}
	if l.CurrentMatchupPeriod == 0 {
		l.CurrentMatchupPeriod = l.CurrentWeek
	}
	for k, v := range r.Settings.RosterSettings.LineupSlotCounts {
		id, err := strconv.Atoi(k)
		if err == nil && v > 0 {
			l.LineupSlotCounts[id] = v
		}
	}
	for k, v := range r.Settings.ScheduleSettings.MatchupPeriods {
		if id, err := strconv.Atoi(k); err == nil {
			l.MatchupPeriods[id] = v
		}
	}
	for _, m := range r.Members {
		l.Members[NormalizeSWID(m.ID)] = m.DisplayName
	}
	for _, rt := range r.Teams {
		name := rt.Name
		if name == "" {
			name = strings.TrimSpace(rt.Location + " " + rt.Nickname)
		}
		t := &Team{
			ID:         rt.ID,
			Abbrev:     rt.Abbrev,
			Name:       name,
			WaiverRank: rt.WaiverRank,
			Wins:       rt.Record.Overall.Wins,
			Losses:     rt.Record.Overall.Losses,
			Ties:       rt.Record.Overall.Ties,
			PointsFor:  rt.Record.Overall.PointsFor,
		}
		for _, o := range rt.Owners {
			t.Owners = append(t.Owners, NormalizeSWID(o))
		}
		if l.UsesFAAB {
			t.FAABRemaining = l.FAABBudget - rt.TransactionCounter.AcquisitionBudgetSpent
		}
		for _, e := range rt.Roster.Entries {
			pe := e.PlayerPoolEntry
			if pe.Player.ID == 0 {
				pe.Player.ID = e.PlayerID
			}
			if pe.OnTeamID == 0 {
				pe.OnTeamID = rt.ID
			}
			p := pe.toPlayer(e.LineupSlotID, e.InjuryStatus)
			p.Status = StatusOnTeam
			t.Roster = append(t.Roster, p)
		}
		l.Teams = append(l.Teams, t)
	}
	for _, m := range r.Schedule {
		l.Schedule = append(l.Schedule, Matchup{
			Period:     m.MatchupPeriodID,
			HomeTeamID: m.Home.TeamID,
			AwayTeamID: m.Away.TeamID,
			HomePoints: m.Home.TotalPoints,
			AwayPoints: m.Away.TotalPoints,
		})
	}
	return l
}
