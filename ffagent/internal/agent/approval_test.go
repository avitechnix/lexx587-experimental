package agent

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/approval"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/mail"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/news"
)

type fakeMail struct {
	n     int
	sent  []mail.Message
	inbox []mail.Received
	fail  bool
}

func (m *fakeMail) NewMessageID() string { m.n++; return fmt.Sprintf("<m%d@bot.test>", m.n) }
func (m *fakeMail) Send(_ context.Context, msg mail.Message) (string, error) {
	if m.fail {
		return "", fmt.Errorf("smtp down")
	}
	m.sent = append(m.sent, msg)
	return msg.MessageID, nil
}
func (m *fakeMail) Replies(context.Context, time.Time, string) ([]mail.Received, error) {
	return m.inbox, nil
}

var replyN int

func (m *fakeMail) reply(from, subject, text string) {
	replyN++
	m.inbox = append(m.inbox, mail.Received{MessageID: fmt.Sprintf("<r%d@phone.test>", replyN), From: from, Subject: "Re: " + subject, Text: text})
}

const owner = "me@example.com"

func approvalWatcher(t *testing.T) (*Watcher, *fakeESPN, *fakeMail, *fakeNews, *espn.Player, *espn.Player) {
	f, star, backup := handcuffLeague()
	a := testAgent(f) // approval_mode defaults to "email"
	a.Cfg.Email.Address, a.Cfg.Email.NotifyTo, a.Cfg.Email.DigestHour = "bot@example.com", owner, 25
	nf := &fakeNews{reports: map[int]news.Report{
		star.ID: {PlayerID: star.ID, Name: star.Name, ProTeamID: starTeam, Position: "RB", Status: espn.InjuryOut, Date: a.Now().Add(-10 * time.Minute)},
	}}
	fm := &fakeMail{}
	w := &Watcher{Agent: a, News: nf, Apply: true, Out: io.Discard, Mailer: fm, StatePath: filepath.Join(t.TempDir(), "state.json")}
	return w, f, fm, nf, star, backup
}

func TestNothingRunsWithoutApproval(t *testing.T) {
	w, f, fm, _, _, backup := approvalWatcher(t)
	ctx := context.Background()
	if err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.submitted) != 0 {
		t.Fatalf("submitted %d transactions before any approval", len(f.submitted))
	}
	if len(fm.sent) != 1 {
		t.Fatalf("expected one proposal email, got %d", len(fm.sent))
	}
	msg := fm.sent[0]
	for _, want := range []string{"Backup RB", "Why:", "Star RB is OUT, so he's next man up", "win probability", "approve all", "Expires:", "Needs: move 1", "first come, first served"} {
		if !strings.Contains(msg.Body, want) {
			t.Errorf("proposal email missing %q:\n%s", want, msg.Body)
		}
	}
	if msg.To != owner || !strings.Contains(msg.Subject, "[ffagent #") {
		t.Errorf("bad envelope: %+v", msg)
	}

	// The same news again doesn't send a duplicate proposal.
	w.lastFull = time.Time{}
	if err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fm.sent) != 1 {
		t.Errorf("duplicate proposal sent")
	}

	// Replies that must be ignored: a stranger, and the agent's own email.
	fm.reply("attacker@example.com", msg.Subject, "approve all")
	fm.inbox = append(fm.inbox, mail.Received{MessageID: msg.MessageID, From: owner, Subject: msg.Subject, Text: msg.Body})
	if err := w.TickApprovals(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.submitted) != 0 || countAdds(f, backup.ID) != 0 {
		t.Fatalf("acted on an unauthorized reply")
	}
	for _, it := range w.approvals.Items {
		if it.Status != approval.Pending {
			t.Errorf("item %d changed to %s without a valid reply", it.Number, it.Status)
		}
	}
}

func TestApproveSomeDeclineOthers(t *testing.T) {
	w, f, fm, _, _, backup := approvalWatcher(t)
	ctx := context.Background()
	if err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	proposal := fm.sent[0]

	// Approve only the pickup. Quoted text below the reply must be ignored.
	fm.reply(owner, proposal.Subject, "approve 1\n\nOn Fri, Oct 16, ffagent wrote:\n> approve all")
	if err := w.TickApprovals(ctx); err != nil {
		t.Fatal(err)
	}
	if countAdds(f, backup.ID) != 1 {
		t.Fatalf("approved pickup didn't run")
	}
	items := w.approvals.Items
	if items[0].Status != approval.Executed || items[1].Status != approval.Pending {
		t.Fatalf("statuses after approving 1: %s, %s", items[0].Status, items[1].Status)
	}
	res := fm.sent[len(fm.sent)-1]
	if res.InReplyTo != proposal.MessageID || !strings.Contains(res.Body, "I read your reply as: approve 1") || !strings.Contains(res.Body, "DONE") {
		t.Errorf("result email:\n%+v", res)
	}

	// Decline the lineup.
	before := len(f.submitted)
	fm.reply(owner, proposal.Subject, "decline 2")
	if err := w.TickApprovals(ctx); err != nil {
		t.Fatal(err)
	}
	if items[1].Status != approval.Declined || len(f.submitted) != before {
		t.Errorf("decline: status %s, submitted %d->%d", items[1].Status, before, len(f.submitted))
	}
	if !w.approvals.RecentlyDeclined(items[1].Signature, w.Agent.Now(), time.Hour) {
		t.Error("declined move not remembered")
	}
}

func TestApprovedMoveIsRecheckedBeforeRunning(t *testing.T) {
	w, f, fm, _, _, backup := approvalWatcher(t)
	ctx := context.Background()
	if err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	proposal := fm.sent[0]

	// Before you reply, a rival grabs the backup.
	orig := f.league
	f.league = func() *espn.League {
		l := orig()
		b := *backup
		b.OnTeamID, b.LineupSlot, b.Status = 2, espn.SlotBench, espn.StatusOnTeam
		l.Team(2).Roster = append(l.Team(2).Roster, &b)
		return l
	}
	fm.reply(owner, proposal.Subject, "approve all")
	if err := w.TickApprovals(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.submitted) != 0 {
		t.Fatalf("ran a move that was no longer valid")
	}
	items := w.approvals.Items
	if items[0].Status != approval.Skipped || !strings.Contains(items[0].Result, "no longer available") {
		t.Errorf("pickup: %s %q", items[0].Status, items[0].Result)
	}
	if items[1].Status != approval.Skipped || !strings.Contains(items[1].Result, "depends on move 1") {
		t.Errorf("dependent lineup: %s %q", items[1].Status, items[1].Result)
	}
	if res := fm.sent[len(fm.sent)-1].Body; !strings.Contains(res, "NOT RUN") {
		t.Errorf("result email should explain what didn't run:\n%s", res)
	}
}

func TestDailySummary(t *testing.T) {
	w, _, fm, _, _, _ := approvalWatcher(t)
	ctx := context.Background()
	if err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	fm.reply(owner, fm.sent[0].Subject, "approve all")
	w.Agent.Cfg.Email.DigestHour = 0
	if err := w.TickApprovals(ctx); err != nil {
		t.Fatal(err)
	}
	var digest *mail.Message
	for i := range fm.sent {
		if strings.Contains(fm.sent[i].Subject, "Daily summary") {
			digest = &fm.sent[i]
		}
	}
	if digest == nil {
		t.Fatal("no daily summary sent")
	}
	if !strings.Contains(digest.Body, "DONE (2)") || !strings.Contains(digest.Body, "Backup RB") || !strings.Contains(digest.Subject, "2 done") {
		t.Errorf("summary:\n%s\n%s", digest.Subject, digest.Body)
	}
	// Only once a day.
	n := len(fm.sent)
	if err := w.TickApprovals(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fm.sent) != n {
		t.Error("summary sent twice in one day")
	}
}

func TestConfirmInTerminal(t *testing.T) {
	f, me := scenario()
	a := testAgent(f)
	p, err := a.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Decline the IR move; the add that uses its roster spot must not run.
	var asked []string
	a.Confirm = func(act Action) bool {
		asked = append(asked, string(act.Kind))
		return act.Kind != ActIRStash
	}
	if err := a.Execute(context.Background(), p, Scope{Roster: true, Lineup: true}, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, txn := range f.submitted {
		for _, it := range txn.Items {
			if it.Type == espn.ItemLineup && *it.ToLineupSlotID == espn.SlotIR {
				t.Error("declined IR move ran")
			}
			if it.Type == espn.ItemAdd && txn.Type == espn.TxnFreeAgent && len(txn.Items) == 1 {
				t.Error("add depending on the declined IR move ran")
			}
		}
	}
	if len(asked) == 0 || asked[0] != string(ActIRStash) {
		t.Errorf("asked: %v", asked)
	}
	_ = me
}

func TestFailedProposalEmailIsRetried(t *testing.T) {
	w, f, fm, _, _, _ := approvalWatcher(t)
	ctx := context.Background()
	fm.fail = true
	if err := w.Tick(ctx); err == nil {
		t.Fatal("expected the email failure to be reported")
	}
	if len(w.approvals.Pending()) != 0 {
		t.Fatal("moves you never saw are left waiting")
	}
	fm.fail = false
	w.lastFull = time.Time{}
	if err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fm.sent) != 1 || len(f.submitted) != 0 {
		t.Errorf("retry: sent %d emails, %d transactions", len(fm.sent), len(f.submitted))
	}
}
