package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/analysis"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/approval"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/news"
)

// NewsSource is the subset of the news client the watcher uses.
type NewsSource interface {
	Injuries(ctx context.Context) (map[int]news.Report, error)
	Headlines(ctx context.Context, limit int) ([]news.Article, error)
}

// WatchState persists what the watcher has already seen and done, so a
// restart doesn't re-react to old news or exceed the daily pickup cap.
type WatchState struct {
	Statuses     map[int]SeenStatus   `json:"statuses"`
	SeenArticles map[string]time.Time `json:"seen_articles"`
	Acquisitions []Acquisition        `json:"acquisitions"`
}

// SeenStatus is the last injury designation seen for a player.
type SeenStatus struct {
	Status   string    `json:"status"`
	Date     time.Time `json:"date"`
	Name     string    `json:"name"`
	Position string    `json:"position"`
}

// Acquisition is a pickup the agent made.
type Acquisition struct {
	PlayerID int        `json:"player_id"`
	Kind     ActionKind `json:"kind"`
	At       time.Time  `json:"at"`
}

// Watcher polls injury news and acts on it right away. Free agents are
// first come, first served, so the add goes in the moment the agent sees
// the opening; waiver players get a claim queued immediately.
type Watcher struct {
	Agent     *Agent
	News      NewsSource
	StatePath string
	Apply     bool
	Out       io.Writer

	// Mailer and ApprovalsPath are used in "email" approval mode: moves are
	// proposed by email and only run once you approve them.
	Mailer        Mailer
	ApprovalsPath string

	state     WatchState
	approvals *approval.Store
	loaded    bool
	lastFull  time.Time
	lastNews  time.Time
	perRun    int
}

// emailApprovals reports whether moves wait for an emailed OK.
func (w *Watcher) emailApprovals() bool { return w.Agent.Cfg.ApprovalMode != "auto" }

// DefaultStatePath is <user config dir>/ffagent/watch-state.json.
func DefaultStatePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "ffagent", "watch-state.json")
}

// Run checks the news every interval and, in email approval mode, checks
// for your replies every minute, until the context is cancelled.
func (w *Watcher) Run(ctx context.Context, every time.Duration) error {
	if every < time.Minute {
		every = time.Minute // be polite to ESPN
	}
	if w.Apply && w.emailApprovals() && w.Mailer == nil {
		return errors.New("email approvals are on but email isn't set up; configure \"email\" in the config and run `ffagent secrets set email` (or set approval_mode to \"auto\")")
	}
	report := func(err error) {
		if err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(w.Out, "%s  error: %v\n", stamp(w.Agent.Now()), err)
		}
	}
	for {
		if w.Apply && w.emailApprovals() {
			report(w.TickApprovals(ctx))
		}
		if w.lastNews.IsZero() || w.Agent.Now().Sub(w.lastNews) >= every || w.lastFull.IsZero() {
			w.lastNews = w.Agent.Now()
			report(w.Tick(ctx))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Minute):
		}
	}
}

// Tick checks the news once and, if anything relevant changed (or a full
// re-plan is due), plans and executes roster moves and the lineup.
func (w *Watcher) Tick(ctx context.Context) error {
	if err := w.ensureLoaded(); err != nil {
		return err
	}
	now := w.Agent.Now()
	reports, err := w.News.Injuries(ctx)
	if err != nil {
		return err
	}
	articles, err := w.News.Headlines(ctx, 50)
	if err != nil {
		fmt.Fprintf(w.Out, "%s  headlines unavailable: %v\n", stamp(now), err)
	}

	baseline := len(w.state.Statuses) == 0
	changes := w.diffStatuses(reports)
	fresh := w.newArticles(articles, now)

	// Feed the news into the valuation model.
	inj := make(map[int]analysis.Injury, len(reports))
	for id, r := range reports {
		inj[id] = analysis.Injury{PlayerID: id, ProTeamID: r.ProTeamID, Status: r.Status, Date: r.Date, Returning: r.Returning}
	}
	w.Agent.Injuries = inj

	full := w.Agent.Cfg.FullReplanMinutes
	due := w.lastFull.IsZero() || now.Sub(w.lastFull) >= time.Duration(full)*time.Minute
	if !baseline {
		for _, c := range changes {
			fmt.Fprintf(w.Out, "%s  INJURY  %s\n", stamp(now), c)
		}
	}
	if len(changes) == 0 && len(fresh) == 0 && !due {
		return w.save()
	}

	w.lastFull = now
	if err := w.act(ctx, now, fresh); err != nil {
		w.save()
		return err
	}
	return w.save()
}

func (w *Watcher) act(ctx context.Context, now time.Time, fresh []news.Article) error {
	a := w.Agent
	// Respect the daily cap, counting pickups from previous runs.
	used := 0
	for _, acq := range w.state.Acquisitions {
		if now.Sub(acq.At) < 24*time.Hour {
			used++
		}
	}
	a.Cfg.MaxAcquisitionsPerRun = max(0, min(w.perRun, a.Cfg.MaxAcquisitionsPerDay-used))
	a.RecentlyAcquired = map[int]time.Time{}
	for _, acq := range w.state.Acquisitions {
		a.RecentlyAcquired[acq.PlayerID] = acq.At
	}

	p, err := a.Plan(ctx)
	if err != nil {
		return err
	}
	w.printNews(p, fresh, now)

	if a.Cfg.MaxAcquisitionsPerRun == 0 && used > 0 {
		fmt.Fprintf(w.Out, "%s  daily pickup cap reached (%d in 24h)\n", stamp(now), used)
	}
	if len(p.Actions) == 0 && len(p.Moves) == 0 {
		fmt.Fprintf(w.Out, "%s  checked: no moves needed\n", stamp(now))
		return nil
	}
	for _, act := range p.Actions {
		fmt.Fprintf(w.Out, "%s  PLAN    [%s] %s — %s\n", stamp(now), act.Kind, act.Summary, act.Reason)
	}
	for _, m := range p.Moves {
		fmt.Fprintf(w.Out, "%s  PLAN    lineup: %s %s → %s\n", stamp(now), m.Player.Name, espn.SlotName(m.From), espn.SlotName(m.To))
	}
	if !w.Apply {
		fmt.Fprintf(w.Out, "%s  dry run (use --apply to act)\n", stamp(now))
		return nil
	}
	if w.emailApprovals() {
		return w.propose(ctx, p, now)
	}

	a.OnSubmitted = func(act Action) {
		if act.Kind != ActAdd && act.Kind != ActClaim {
			return
		}
		for _, it := range act.Txn.Items {
			if it.Type == espn.ItemAdd {
				w.state.Acquisitions = append(w.state.Acquisitions, Acquisition{PlayerID: it.PlayerID, Kind: act.Kind, At: now})
			}
		}
		_ = w.save() // record immediately, in case a later step fails
	}
	defer func() { a.OnSubmitted = nil }()
	return a.Execute(ctx, p, Scope{Roster: true, Lineup: true}, prefixWriter{w.Out, stamp(now) + "  "})
}

// diffStatuses records the new report and describes what changed.
func (w *Watcher) diffStatuses(reports map[int]news.Report) []string {
	var out []string
	next := make(map[int]SeenStatus, len(reports))
	for id, r := range reports {
		next[id] = SeenStatus{Status: r.Status, Date: r.Date, Name: r.Name, Position: r.Position}
		prev, ok := w.state.Statuses[id]
		if !fantasyPosition(r.Position) {
			continue
		}
		switch {
		case !ok && r.Status != espn.InjuryActive:
			out = append(out, fmt.Sprintf("%s (%s) %s — %s", r.Name, r.Position, r.Status, r.Comment))
		case ok && prev.Status != r.Status:
			out = append(out, fmt.Sprintf("%s (%s) %s → %s — %s", r.Name, r.Position, prev.Status, r.Status, r.Comment))
		}
	}
	// Players who dropped off the report are healthy again.
	for id, prev := range w.state.Statuses {
		if _, ok := reports[id]; !ok && prev.Status != espn.InjuryActive && fantasyPosition(prev.Position) {
			out = append(out, fmt.Sprintf("%s (%s) cleared from the injury report (was %s)", prev.Name, prev.Position, prev.Status))
		}
	}
	sort.Strings(out)
	w.state.Statuses = next
	return out
}

func fantasyPosition(pos string) bool {
	switch pos {
	case "QB", "RB", "WR", "TE", "K", "PK":
		return true
	}
	return false
}

// newArticles returns unseen recent headlines tagged with a player.
func (w *Watcher) newArticles(arts []news.Article, now time.Time) []news.Article {
	var out []news.Article
	for _, art := range arts {
		if _, seen := w.state.SeenArticles[art.ID]; seen {
			continue
		}
		w.state.SeenArticles[art.ID] = now
		if len(art.PlayerIDs) > 0 && now.Sub(art.Published) < 24*time.Hour {
			out = append(out, art)
		}
	}
	// Forget old article IDs so the state file stays small.
	for id, at := range w.state.SeenArticles {
		if now.Sub(at) > 7*24*time.Hour {
			delete(w.state.SeenArticles, id)
		}
	}
	return out
}

// printNews shows headlines about players on my roster or my opponent's.
func (w *Watcher) printNews(p *Plan, arts []news.Article, now time.Time) {
	mine := map[int]string{}
	for _, pl := range p.Me.Roster {
		mine[pl.ID] = "your"
	}
	if p.Matchup != nil && p.Matchup.Opponent != nil {
		for _, pl := range p.Matchup.Opponent.Roster {
			mine[pl.ID] = "opponent's"
		}
	}
	for _, art := range arts {
		for _, id := range art.PlayerIDs {
			if whose, ok := mine[id]; ok {
				fmt.Fprintf(w.Out, "%s  NEWS    (%s player) %s\n", stamp(now), whose, art.Headline)
				break
			}
		}
	}
}

func (w *Watcher) load() error {
	if w.StatePath == "" {
		w.StatePath = DefaultStatePath()
	}
	w.state = WatchState{Statuses: map[int]SeenStatus{}, SeenArticles: map[string]time.Time{}}
	b, err := os.ReadFile(w.StatePath)
	if err == nil {
		if err := json.Unmarshal(b, &w.state); err != nil {
			return fmt.Errorf("watch state %s: %w", w.StatePath, err)
		}
		if w.state.Statuses == nil {
			w.state.Statuses = map[int]SeenStatus{}
		}
		if w.state.SeenArticles == nil {
			w.state.SeenArticles = map[string]time.Time{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if w.ApprovalsPath == "" {
		w.ApprovalsPath = filepath.Join(filepath.Dir(w.StatePath), "approvals.json")
	}
	if w.approvals, err = approval.Load(w.ApprovalsPath); err != nil {
		return fmt.Errorf("approvals %s: %w", w.ApprovalsPath, err)
	}
	w.loaded = true
	return nil
}

func (w *Watcher) save() error {
	if w.approvals != nil {
		if err := w.approvals.Save(w.Agent.Now()); err != nil {
			return err
		}
	}
	// Keep 30 days of acquisitions for the anti-churn window.
	now := w.Agent.Now()
	kept := w.state.Acquisitions[:0]
	for _, a := range w.state.Acquisitions {
		if now.Sub(a.At) < 30*24*time.Hour {
			kept = append(kept, a)
		}
	}
	w.state.Acquisitions = kept
	b, err := json.MarshalIndent(w.state, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(w.StatePath), 0o700); err != nil {
		return err
	}
	tmp := w.StatePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, w.StatePath)
}

func stamp(t time.Time) string { return t.Format("Mon 15:04:05") }

// prefixWriter prefixes each line written to it.
type prefixWriter struct {
	w      io.Writer
	prefix string
}

func (p prefixWriter) Write(b []byte) (int, error) {
	lines := strings.SplitAfter(string(b), "\n")
	for _, l := range lines {
		if l == "" {
			continue
		}
		if _, err := io.WriteString(p.w, p.prefix+l); err != nil {
			return 0, err
		}
	}
	return len(b), nil
}
