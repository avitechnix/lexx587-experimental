package news

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
)

// Trimmed from real responses of the ESPN injuries and news endpoints.
const injuriesJSON = `{"injuries":[{"id":"22","displayName":"Arizona Cardinals","injuries":[
 {"id":"1","status":"Questionable","date":"2026-10-15T23:41Z","shortComment":"Love (ankle) remained sidelined.",
  "athlete":{"displayName":"Jeremiyah Love","links":[{"href":"https://www.espn.com/nfl/player/_/id/4870808/jeremiyah-love"}],
   "position":{"abbreviation":"RB"},"team":{"id":"22"}},
  "details":{"type":"Ankle","returnDate":"2026-10-18","fantasyStatus":{"description":"QUESTIONABLE"}}},
 {"id":"2","status":"Injured Reserve","date":"2026-10-16T10:00Z","shortComment":"Placed on IR.",
  "athlete":{"displayName":"Some Receiver","links":[{"href":"https://www.espn.com/nfl/player/_/id/123/some-receiver"}],
   "position":{"abbreviation":"WR"},"team":{"id":"22"}},
  "details":{"type":"Knee","fantasyStatus":{"description":"IR-R"}}}
]}]}`

const newsJSON = `{"articles":[{"id":50196775,"type":"HeadlineNews","headline":"RB Hall out","description":"Breece Hall ruled out.",
 "published":"2026-10-16T15:50:41Z","categories":[{"type":"team","teamId":20},{"type":"athlete","athleteId":4427366}]}]}`

func TestFeeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/injuries" {
			io.WriteString(w, injuriesJSON)
		} else {
			io.WriteString(w, newsJSON)
		}
	}))
	defer srv.Close()
	c := NewClient()
	c.Base = srv.URL

	inj, err := c.Injuries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	love := inj[4870808]
	if love.Status != espn.InjuryQuestionable || love.ProTeamID != 22 || love.Position != "RB" || love.Date.IsZero() || love.ReturnDate.IsZero() {
		t.Errorf("bad report: %+v", love)
	}
	if r := inj[123]; r.Status != espn.InjuryReserve || !r.Returning {
		t.Errorf("IR-R should be INJURY_RESERVE and designated to return: %+v", r)
	}
	if love.Returning {
		t.Errorf("questionable player isn't returning from IR")
	}

	arts, err := c.Headlines(context.Background(), 10)
	if err != nil || len(arts) != 1 {
		t.Fatalf("headlines: %v %v", arts, err)
	}
	if a := arts[0]; a.ID != "50196775" || len(a.PlayerIDs) != 1 || a.PlayerIDs[0] != 4427366 || a.TeamIDs[0] != 20 {
		t.Errorf("bad article: %+v", a)
	}
}

func TestNormalizeStatus(t *testing.T) {
	cases := map[string]string{
		"QUESTIONABLE": espn.InjuryQuestionable, "DOUBTFUL": espn.InjuryDoubtful,
		"OUT": espn.InjuryOut, "INACTIVE": espn.InjuryOut,
		"IR": espn.InjuryReserve, "IR-R": espn.InjuryReserve, "PUP-R": espn.InjuryReserve, "NFI-R": espn.InjuryReserve,
		"RESERVE-SUS": espn.InjurySuspension, "RESERVE-CEL": espn.InjuryOut, "": espn.InjuryActive,
	}
	for in, want := range cases {
		if got := NormalizeStatus(in, "Active"); got != want {
			t.Errorf("%q -> %s, want %s", in, got, want)
		}
	}
}
