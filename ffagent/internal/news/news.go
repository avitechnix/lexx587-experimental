// Package news reads ESPN's NFL injury report and headline feed. Player and
// team IDs in these feeds are the same IDs the fantasy API uses.
package news

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
)

const DefaultBase = "https://site.api.espn.com/apis/site/v2/sports/football/nfl"

// Report is one player's entry in the league-wide injury report.
type Report struct {
	PlayerID   int
	Name       string
	ProTeamID  int
	Position   string
	Status     string // normalized to espn.Injury* constants
	RawStatus  string // ESPN's fantasy designation, e.g. "IR-R", "INACTIVE"
	Returning  bool   // designated to return: IR-R, PUP-R, NFI-R
	Injury     string // body part, e.g. "Ankle"
	Comment    string
	Date       time.Time
	ReturnDate time.Time
}

// Article is a headline tagged with players and teams.
type Article struct {
	ID          string
	Headline    string
	Description string
	Published   time.Time
	PlayerIDs   []int
	TeamIDs     []int
}

// Client fetches the feeds. The feeds are public and need no credentials.
type Client struct {
	Base string
	HTTP *http.Client
}

// NewClient returns a client for ESPN's public NFL feeds.
func NewClient() *Client {
	return &Client{Base: DefaultBase, HTTP: &http.Client{Timeout: 45 * time.Second}}
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("news: GET %s: %d: %s", path, resp.StatusCode, b)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
}

var athleteIDRe = regexp.MustCompile(`/id/(\d+)`)

// Injuries returns the current injury report keyed by player ID.
func (c *Client) Injuries(ctx context.Context) (map[int]Report, error) {
	var raw struct {
		Injuries []struct {
			ID       string `json:"id"`
			Injuries []struct {
				Status       string `json:"status"`
				Date         string `json:"date"`
				ShortComment string `json:"shortComment"`
				Athlete      struct {
					DisplayName string `json:"displayName"`
					Links       []struct {
						Href string `json:"href"`
					} `json:"links"`
					Position struct {
						Abbreviation string `json:"abbreviation"`
					} `json:"position"`
					Team struct {
						ID string `json:"id"`
					} `json:"team"`
				} `json:"athlete"`
				Details struct {
					Type          string `json:"type"`
					ReturnDate    string `json:"returnDate"`
					FantasyStatus struct {
						Description string `json:"description"`
					} `json:"fantasyStatus"`
				} `json:"details"`
			} `json:"injuries"`
		} `json:"injuries"`
	}
	if err := c.get(ctx, "/injuries", &raw); err != nil {
		return nil, err
	}
	out := map[int]Report{}
	for _, team := range raw.Injuries {
		for _, e := range team.Injuries {
			id := 0
			for _, l := range e.Athlete.Links {
				if m := athleteIDRe.FindStringSubmatch(l.Href); m != nil {
					id, _ = strconv.Atoi(m[1])
					break
				}
			}
			if id == 0 {
				continue
			}
			teamID, _ := strconv.Atoi(e.Athlete.Team.ID)
			if teamID == 0 {
				teamID, _ = strconv.Atoi(team.ID)
			}
			r := Report{
				PlayerID:  id,
				Name:      e.Athlete.DisplayName,
				ProTeamID: teamID,
				Position:  e.Athlete.Position.Abbreviation,
				RawStatus: e.Details.FantasyStatus.Description,
				Status:    NormalizeStatus(e.Details.FantasyStatus.Description, e.Status),
				Returning: strings.HasSuffix(strings.ToUpper(e.Details.FantasyStatus.Description), "-R"),
				Injury:    e.Details.Type,
				Comment:   e.ShortComment,
				Date:      parseTime(e.Date),
			}
			r.ReturnDate, _ = time.Parse("2006-01-02", e.Details.ReturnDate)
			// A player can appear more than once; keep the newest report.
			if prev, ok := out[id]; !ok || r.Date.After(prev.Date) {
				out[id] = r
			}
		}
	}
	return out, nil
}

// NormalizeStatus maps the injury report's designations to the fantasy
// API's injury statuses.
func NormalizeStatus(fantasy, status string) string {
	f := strings.ToUpper(strings.TrimSpace(fantasy))
	switch {
	case f == "QUESTIONABLE":
		return espn.InjuryQuestionable
	case f == "DOUBTFUL":
		return espn.InjuryDoubtful
	case f == "OUT" || f == "INACTIVE":
		return espn.InjuryOut
	case f == "RESERVE-SUS" || strings.Contains(f, "SUSP"):
		return espn.InjurySuspension
	case strings.HasPrefix(f, "IR") || strings.HasPrefix(f, "PUP") || strings.HasPrefix(f, "NFI"):
		return espn.InjuryReserve
	case strings.HasPrefix(f, "RESERVE"):
		return espn.InjuryOut
	case f == "":
		switch strings.ToLower(status) {
		case "out":
			return espn.InjuryOut
		case "injured reserve":
			return espn.InjuryReserve
		case "questionable":
			return espn.InjuryQuestionable
		case "doubtful":
			return espn.InjuryDoubtful
		}
	}
	return espn.InjuryActive
}

// Headlines returns the latest NFL headlines.
func (c *Client) Headlines(ctx context.Context, limit int) ([]Article, error) {
	var raw struct {
		Articles []struct {
			ID          json.Number `json:"id"`
			Headline    string      `json:"headline"`
			Description string      `json:"description"`
			Published   string      `json:"published"`
			Categories  []struct {
				Type      string `json:"type"`
				AthleteID int    `json:"athleteId"`
				TeamID    int    `json:"teamId"`
			} `json:"categories"`
		} `json:"articles"`
	}
	if err := c.get(ctx, fmt.Sprintf("/news?limit=%d", limit), &raw); err != nil {
		return nil, err
	}
	out := make([]Article, 0, len(raw.Articles))
	for _, a := range raw.Articles {
		art := Article{ID: a.ID.String(), Headline: a.Headline, Description: a.Description, Published: parseTime(a.Published)}
		for _, cat := range a.Categories {
			switch {
			case cat.Type == "athlete" && cat.AthleteID != 0:
				art.PlayerIDs = append(art.PlayerIDs, cat.AthleteID)
			case cat.Type == "team" && cat.TeamID != 0:
				art.TeamIDs = append(art.TeamIDs, cat.TeamID)
			}
		}
		out = append(out, art)
	}
	return out, nil
}

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04Z", "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
