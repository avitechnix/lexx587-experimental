package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/approval"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/lineup"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/mail"
)

// Mailer sends email and reads replies (implemented by *mail.Account).
type Mailer interface {
	NewMessageID() string
	Send(ctx context.Context, m mail.Message) (string, error)
	Replies(ctx context.Context, since time.Time, tag string) ([]mail.Received, error)
}

func (w *Watcher) ensureLoaded() error {
	if w.loaded {
		return nil
	}
	if err := w.load(); err != nil {
		return err
	}
	w.perRun = w.Agent.Cfg.MaxAcquisitionsPerRun
	return nil
}

// propose turns a plan into a batch of moves awaiting approval and emails
// it. Moves already waiting, recently declined, or touching a player in a
// waiting move aren't proposed again.
func (w *Watcher) propose(ctx context.Context, p *Plan, now time.Time) error {
	st := w.approvals
	cfg := w.Agent.Cfg
	ttl := time.Duration(cfg.ApprovalTTLHours) * time.Hour
	cooldown := time.Duration(cfg.DeclinedCooldownHours) * time.Hour

	busy := map[int]bool{} // players in waiting moves
	waiting := map[string]bool{}
	for _, it := range st.Items {
		justDone := it.Status == approval.Executed && now.Sub(it.Done) < time.Hour // ESPN may lag
		if it.Status == approval.Pending || it.Status == approval.Approved || justDone {
			waiting[it.Signature] = true
			if it.Kind != string(ActLineup) {
				for _, id := range it.Players {
					busy[id] = true
				}
			}
		}
	}

	var items []*approval.Item
	index := map[int]int{} // plan action index -> item index
	for i, act := range p.Actions {
		sig := act.Signature()
		if waiting[sig] || st.RecentlyDeclined(sig, now, cooldown) || anyBusy(busy, act.Players()) {
			continue
		}
		var deps []int
		ok := true
		for _, d := range act.Depends {
			j, in := index[d]
			if !in {
				ok = false // what it depends on isn't being proposed
				break
			}
			deps = append(deps, j)
		}
		if !ok {
			continue
		}
		index[i] = len(items)
		items = append(items, &approval.Item{
			Kind: string(act.Kind), Summary: act.Summary, Reason: act.Reason, Txn: act.Txn,
			Signature: sig, Players: act.Players(), Depends: deps, Expires: now.Add(ttl),
		})
	}

	if act := LineupAction(espn.NormalizeSWIDSecret(cfg.SWID), p.Me.ID, p.League.CurrentWeek, p.Moves); act != nil {
		sig := act.Signature()
		if !waiting[sig] && !st.RecentlyDeclined(sig, now, cooldown) {
			// A newer lineup replaces any older lineup still waiting.
			for _, it := range st.Pending() {
				if it.Kind == string(ActLineup) {
					it.Status, it.Done, it.Result = approval.Superseded, now, "replaced by a newer lineup proposal"
				}
			}
			var deps []int
			for j := range items {
				deps = append(deps, j) // the lineup assumes the roster moves happen
			}
			items = append(items, &approval.Item{
				Kind: string(ActLineup), Summary: "Set lineup: " + act.Reason, Reason: lineupWhy(p),
				Txn: act.Txn, Signature: sig, Players: act.Players(), Depends: deps,
				Expires: earliest(now.Add(ttl), firstKickoff(p.League, p.Moves, now)),
			})
		}
	}
	if len(items) == 0 {
		fmt.Fprintf(w.Out, "%s  nothing new to propose (moves are already waiting for you)\n", stamp(now))
		return nil
	}

	b := st.AddBatch("moves", items, now)
	subject, body := proposalEmail(p, b, items, cfg.Email.Location())
	if err := w.send(ctx, b, subject, body, ""); err != nil {
		// You never saw these, so don't let them block re-proposing.
		for _, it := range items {
			it.Status, it.Done, it.Result = approval.Failed, now, "couldn't email the proposal"
		}
		return fmt.Errorf("emailing proposal: %w", err)
	}
	fmt.Fprintf(w.Out, "%s  EMAIL   sent %d move(s) for approval (#%s)\n", stamp(now), len(items), b.Token)
	return nil
}

// send emails a message for a batch and remembers its Message-ID so the
// agent never mistakes its own email for your reply.
func (w *Watcher) send(ctx context.Context, b *approval.Batch, subject, body, inReplyTo string) error {
	id := w.Mailer.NewMessageID()
	w.approvals.Processed[id] = w.Agent.Now()
	if b.MessageID == "" {
		b.MessageID, b.Subject = id, subject
	}
	if err := w.approvals.Save(w.Agent.Now()); err != nil {
		return err
	}
	_, err := w.Mailer.Send(ctx, mail.Message{To: w.Agent.Cfg.Email.NotifyTo, Subject: subject, Body: body, MessageID: id, InReplyTo: inReplyTo})
	return err
}

// TickApprovals reads replies, expires stale proposals, runs approved
// moves, emails the results, and sends the daily summary when it's due.
func (w *Watcher) TickApprovals(ctx context.Context) error {
	if err := w.ensureLoaded(); err != nil {
		return err
	}
	defer w.save()
	now := w.Agent.Now()
	st := w.approvals

	decided := map[string][]*approval.Item{} // batch token -> items decided now
	readAs := map[string]string{}
	if len(st.Pending()) > 0 {
		since := now.Add(-48 * time.Hour)
		for _, it := range st.Pending() {
			if it.Created.Before(since) {
				since = it.Created
			}
		}
		replies, err := w.Mailer.Replies(ctx, since.Add(-time.Hour), approval.SubjectTag)
		if err != nil {
			return fmt.Errorf("checking replies: %w", err)
		}
		for _, r := range replies {
			if _, done := st.Processed[r.MessageID]; done || r.MessageID == "" {
				continue
			}
			st.Processed[r.MessageID] = now
			b := st.Batch(approval.TokenIn(r.Subject))
			if b == nil {
				continue
			}
			if r.From != w.Agent.Cfg.Email.NotifyTo {
				fmt.Fprintf(w.Out, "%s  EMAIL   ignored a reply from %s (only %s can approve)\n", stamp(now), r.From, w.Agent.Cfg.Email.NotifyTo)
				continue
			}
			d := approval.ParseReply(r.Text)
			if d.Empty() {
				_ = w.send(ctx, b, "Re: "+b.Subject, "I couldn't find a decision in your reply. Reply with, for example:\n  approve all\n  approve 1 3\n  decline 2\n", b.MessageID)
				continue
			}
			changed := st.Decide(b, d, "email", now)
			decided[b.Token] = append(decided[b.Token], changed...)
			readAs[b.Token] = d.Describe()
			fmt.Fprintf(w.Out, "%s  EMAIL   reply for #%s: %s\n", stamp(now), b.Token, d.Describe())
		}
	}
	st.Expire(now)

	ran, err := w.Agent.ExecuteApproved(ctx, st, prefixWriter{w.Out, stamp(now) + "  "})
	if err != nil {
		return err
	}
	for _, it := range ran {
		if it.Status == approval.Executed && (it.Kind == string(ActAdd) || it.Kind == string(ActClaim)) {
			for _, x := range it.Txn.Items {
				if x.Type == espn.ItemAdd {
					w.state.Acquisitions = append(w.state.Acquisitions, Acquisition{PlayerID: x.PlayerID, Kind: ActionKind(it.Kind), At: now})
				}
			}
		}
		decided[it.Batch] = appendUnique(decided[it.Batch], it)
	}
	if len(ran) > 0 {
		w.lastFull = time.Time{} // the roster changed: re-plan on the next tick
	}
	for token, items := range decided {
		b := st.Batch(token)
		if b == nil || len(items) == 0 {
			continue
		}
		if err := w.send(ctx, b, "Re: "+b.Subject, resultEmail(st, b, readAs[token]), b.MessageID); err != nil {
			fmt.Fprintf(w.Out, "%s  EMAIL   couldn't send results: %v\n", stamp(now), err)
		}
	}
	return w.maybeDigest(ctx, now)
}

// maybeDigest sends the daily summary once a day after DigestHour, with
// trade ideas you can approve by replying.
func (w *Watcher) maybeDigest(ctx context.Context, now time.Time) error {
	cfg := w.Agent.Cfg
	local := now.In(cfg.Email.Location())
	today := local.Format("2006-01-02")
	if w.approvals.LastDigest == today || local.Hour() < cfg.Email.DigestHour {
		return nil
	}
	p, err := w.Agent.Plan(ctx)
	if err != nil {
		return err
	}
	var items []*approval.Item
	for _, t := range p.Trades {
		sig := t.Action.Signature()
		if w.approvals.RecentlyDeclined(sig, now, time.Duration(cfg.DeclinedCooldownHours)*time.Hour) {
			continue
		}
		items = append(items, &approval.Item{
			Kind: string(ActTrade), Summary: t.Action.Summary, Reason: t.Action.Reason, Txn: t.Action.Txn,
			Signature: sig, Players: t.Action.Players(), Expires: now.Add(24 * time.Hour),
		})
	}
	b := w.approvals.AddBatch("digest", items, now)
	subject, body := digestEmail(w.approvals, p, b, items, now, cfg.Email.Location())
	if err := w.send(ctx, b, subject, body, ""); err != nil {
		for _, it := range items {
			it.Status, it.Done, it.Result = approval.Failed, now, "couldn't email the summary"
		}
		return fmt.Errorf("emailing daily summary: %w", err) // retried next minute
	}
	w.approvals.LastDigest = today
	fmt.Fprintf(w.Out, "%s  EMAIL   daily summary sent\n", stamp(now))
	return nil
}

func anyBusy(busy map[int]bool, ids []int) bool {
	for _, id := range ids {
		if busy[id] {
			return true
		}
	}
	return false
}

func appendUnique(items []*approval.Item, it *approval.Item) []*approval.Item {
	for _, x := range items {
		if x.ID == it.ID {
			return items
		}
	}
	return append(items, it)
}

func earliest(a, b time.Time) time.Time {
	if b.IsZero() || a.Before(b) {
		return a
	}
	return b
}

// firstKickoff is when the first game among the moved players starts,
// after which those lineup slots lock.
func firstKickoff(l *espn.League, moves []lineup.Move, now time.Time) time.Time {
	var first time.Time
	for _, m := range moves {
		g, ok := l.ProGames[m.Player.ProTeamID][l.CurrentWeek]
		if !ok || g.Start.Before(now) {
			continue
		}
		if first.IsZero() || g.Start.Before(first) {
			first = g.Start
		}
	}
	return first
}

// lineupWhy explains a lineup change in projected points and win odds.
func lineupWhy(p *Plan) string {
	gain := 0.0
	var parts []string
	for _, m := range p.Moves {
		v := p.Values[m.Player.ID]
		if v == nil {
			continue
		}
		switch {
		case espn.IsStartingSlot(m.To) && !espn.IsStartingSlot(m.From):
			gain += v.WeekProjection
			parts = append(parts, fmt.Sprintf("start %s (%.1f proj)", m.Player.Name, v.WeekProjection))
		case !espn.IsStartingSlot(m.To) && espn.IsStartingSlot(m.From):
			gain -= v.WeekProjection
			note := ""
			if len(v.Notes) > 0 {
				note = ", " + strings.Join(v.Notes, ", ")
			}
			parts = append(parts, fmt.Sprintf("bench %s (%.1f proj%s)", m.Player.Name, v.WeekProjection, note))
		}
	}
	why := strings.Join(parts, "; ")
	if gain > 0 {
		why += fmt.Sprintf(". Net +%.1f projected points this week", gain)
	}
	if m := p.Matchup; m != nil && m.Opponent != nil && m.Lambda != 0 {
		why += ". Strategy: " + m.Strategy
	}
	return why
}
