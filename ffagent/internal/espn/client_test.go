package espn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/secrets"
)

const leagueJSON = `{
  "id": 123, "seasonId": 2026, "scoringPeriodId": 6,
  "status": {"currentMatchupPeriod": 6, "finalScoringPeriod": 17},
  "settings": {
    "name": "Test League",
    "rosterSettings": {"lineupSlotCounts": {"0":1,"2":2,"4":2,"6":1,"23":1,"16":1,"17":1,"20":7,"21":1,"7":0}},
    "acquisitionSettings": {"acquisitionBudget": 100, "isUsingAcquisitionBudget": true}
  },
  "members": [{"id": "{ABC-123}", "displayName": "me"}],
  "teams": [{
    "id": 1, "abbrev": "ME", "location": "My", "nickname": "Team", "owners": ["{abc-123}"],
    "waiverRank": 3, "record": {"overall": {"wins": 3, "losses": 2, "ties": 0, "pointsFor": 600.5}},
    "transactionCounter": {"acquisitionBudgetSpent": 40},
    "roster": {"entries": [{
      "playerId": 99, "lineupSlotId": 2, "injuryStatus": "QUESTIONABLE",
      "playerPoolEntry": {"id": 99, "onTeamId": 1, "lineupLocked": false, "player": {
        "id": 99, "fullName": "Run Ningback", "defaultPositionId": 2, "proTeamId": 12,
        "eligibleSlots": [2,3,23,7,20,21], "injuryStatus": "QUESTIONABLE",
        "ownership": {"percentOwned": 99.1, "percentChange": 0.2},
        "stats": [
          {"seasonId": 2026, "scoringPeriodId": 6, "statSourceId": 1, "statSplitTypeId": 1, "appliedTotal": 14.2},
          {"seasonId": 2026, "scoringPeriodId": 0, "statSourceId": 1, "statSplitTypeId": 0, "appliedTotal": 240, "appliedAverage": 14.1}
        ]
      }}
    }]}
  }, {"id": 2, "name": "Rival", "owners": ["{XYZ}"], "roster": {"entries": []}}],
  "schedule": [{"matchupPeriodId": 6, "home": {"teamId": 1, "totalPoints": 0}, "away": {"teamId": 2, "totalPoints": 0}}]
}`

const cardJSON = `{"players": [{"id": 99, "player": {"id": 99, "stats": [
  {"seasonId": 2026, "scoringPeriodId": 5, "statSourceId": 0, "statSplitTypeId": 1, "appliedTotal": 21.5},
  {"seasonId": 2026, "scoringPeriodId": 5, "statSourceId": 1, "statSplitTypeId": 1, "appliedTotal": 13.0}
]}}]}`

const proJSON = `{"settings": {"proTeams": [{"id": 12, "abbrev": "kc", "byeWeek": 10, "proGamesByScoringPeriod": {
  "6": [{"date": 1792000000000, "homeProTeamId": 12, "awayProTeamId": 13}]}}]}}`

func TestFetchLeagueAndSubmit(t *testing.T) {
	var posted Transaction
	var postedRaw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("espn_s2"); err != nil || c.Value != "s2" {
			t.Errorf("missing espn_s2 cookie on %s", r.URL)
		}
		views := r.URL.Query()["view"]
		switch {
		case r.Method == http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(b, &posted); err != nil {
				t.Errorf("bad body: %v", err)
			}
			_ = json.Unmarshal(b, &postedRaw)
			io.WriteString(w, `{"id":"tx1","status":"EXECUTED"}`)
		case strings.Contains(r.URL.RawQuery, "proTeamSchedules_wl"):
			io.WriteString(w, proJSON)
		case len(views) == 1 && views[0] == "kona_playercard":
			if !strings.Contains(r.Header.Get("x-fantasy-filter"), `"filterIds":{"value":[99]}`) {
				t.Errorf("bad filter %q", r.Header.Get("x-fantasy-filter"))
			}
			io.WriteString(w, cardJSON)
		default:
			io.WriteString(w, leagueJSON)
		}
	}))
	defer srv.Close()

	c := NewClient(123, 2026, secrets.New("s2"), secrets.New("abc-123"))
	c.ReadBase, c.WriteBase = srv.URL, srv.URL
	c.Now = func() time.Time { return time.UnixMilli(1791000000000) } // before kickoff

	l, err := c.FetchLeague(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if l.CurrentWeek != 6 || l.Name != "Test League" || l.RosterSize() != 16 {
		t.Fatalf("league basics wrong: week %d name %q size %d", l.CurrentWeek, l.Name, l.RosterSize())
	}
	me := l.TeamByOwner(c.SWID.Reveal())
	if me == nil || me.ID != 1 || me.Name != "My Team" || me.FAABRemaining != 60 {
		t.Fatalf("team detection wrong: %+v", me)
	}
	if opp, ok := l.Opponent(1, 6); !ok || opp != 2 {
		t.Errorf("opponent = %d, %v", opp, ok)
	}
	p := me.Player(99)
	if p.ProTeam != "KC" || p.InjuryStatus != InjuryQuestionable || p.LineupSlot != SlotRB || p.Locked {
		t.Errorf("player wrong: %+v", p)
	}
	if proj, ok := p.WeekProjection(2026, 6); !ok || proj != 14.2 {
		t.Errorf("week 6 projection = %v %v", proj, ok)
	}
	if act, ok := p.WeekActual(2026, 5); !ok || act != 21.5 {
		t.Errorf("week 5 actual (from player card) = %v %v", act, ok)
	}
	if !l.OnBye(12, 10) || l.OnBye(12, 6) {
		t.Errorf("bye detection wrong")
	}

	txn := NewTransaction(c.SWID, TxnRoster, 1, 6, LineupMove(99, SlotRB, SlotBench))
	res, err := c.Submit(context.Background(), txn)
	if err != nil || res.Status != "EXECUTED" {
		t.Fatalf("submit: %v %+v", err, res)
	}
	if posted.Type != TxnRoster || postedRaw["memberId"] != "{ABC-123}" || len(posted.Items) != 1 ||
		*posted.Items[0].FromLineupSlotID != SlotRB || *posted.Items[0].ToLineupSlotID != SlotBench {
		t.Errorf("posted %+v", posted)
	}
}

func TestClientNeverExposesCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate an error response that echoes the request back.
		b, _ := io.ReadAll(r.Body)
		c, _ := r.Cookie("espn_s2")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"bad request","echo":`+string(b)+`,"cookie":"`+c.Value+`"}`)
	}))
	defer srv.Close()
	c := NewClient(1, 2026, secrets.New("AEBtopsecretcookie"), secrets.New("{1111AAAA-2222-3333-4444-555566667777}"))
	c.WriteBase = srv.URL
	txn := NewTransaction(c.SWID, TxnRoster, 1, 6, LineupMove(1, SlotRB, SlotBench))
	_, err := c.Submit(context.Background(), txn)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, out := range []string{err.Error(), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", txn), fmt.Sprintf("%+v", *txn)} {
		if strings.Contains(out, "topsecret") || strings.Contains(out, "1111AAAA") {
			t.Errorf("credential leaked: %s", out)
		}
	}
	if b, _ := json.Marshal(txn); strings.Contains(string(b), "1111AAAA") {
		t.Errorf("transaction JSON leaks SWID: %s", b)
	}
}
