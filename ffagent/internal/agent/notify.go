package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/approval"
)

const replyHelp = `HOW TO REPLY (just reply to this email)
  approve all         approve 1 3         decline 2         approve 1, decline 2
Each approved move is re-checked against the latest league data right before
it runs, and you'll get a reply with what happened. Moves you don't answer
expire at the time shown.`

func when(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return "-"
	}
	return t.In(loc).Format("Mon Jan 2, 3:04 PM MST")
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// kindLabel is a friendly name for an action kind.
func kindLabel(k string) string {
	switch ActionKind(k) {
	case ActAdd:
		return "FREE AGENT"
	case ActClaim:
		return "WAIVER CLAIM"
	case ActIRStash:
		return "MOVE TO IR"
	case ActIRActivate:
		return "ACTIVATE FROM IR"
	case ActDrop:
		return "DROP"
	case ActLineup:
		return "LINEUP"
	case ActTrade:
		return "TRADE OFFER"
	}
	return k
}

func itemBlock(b *strings.Builder, st *approval.Store, it *approval.Item, loc *time.Location) {
	fmt.Fprintf(b, "%d. [%s] %s\n", it.Number, kindLabel(it.Kind), it.Summary)
	if it.Reason != "" {
		fmt.Fprintf(b, "   Why: %s\n", it.Reason)
	}
	if it.Kind == string(ActAdd) {
		b.WriteString("   Timing: free agents are first come, first served, so reply soon.\n")
	}
	if len(it.Depends) > 0 {
		var ns []string
		for _, id := range it.Depends {
			if d := st.Item(id); d != nil {
				ns = append(ns, fmt.Sprint(d.Number))
			}
		}
		fmt.Fprintf(b, "   Needs: move %s (it only runs if that one does)\n", strings.Join(ns, " and "))
	}
	fmt.Fprintf(b, "   Expires: %s\n\n", when(it.Expires, loc))
}

func proposalEmail(p *Plan, b *approval.Batch, items []*approval.Item, loc *time.Location) (string, string) {
	subject := fmt.Sprintf("[ffagent #%s] %s to review - reply to approve", b.Token, plural(len(items), "move"))
	st := &approval.Store{Items: items}
	var body strings.Builder
	fmt.Fprintf(&body, "%sWeek %d: I'd like to make %s for %s.\n", leaguePrefix(p), p.League.CurrentWeek, plural(len(items), "move"), p.Me.Name)
	if m := p.Matchup; m != nil && m.Opponent != nil {
		fmt.Fprintf(&body, "This week vs %s: win probability %.0f%% with your lineup as it's set now, %.0f%% with these moves.\n",
			m.Opponent.Name, p.CurrentWinProb*100, m.WinProb*100)
	}
	body.WriteString("\n")
	for _, it := range items {
		itemBlock(&body, st, it, loc)
	}
	body.WriteString(replyHelp)
	body.WriteString("\n\n" + glossary)
	fmt.Fprintf(&body, "\n\nRef: %s\n", b.Token)
	return subject, body.String()
}

const glossary = `"Team value" = projected points from your best lineup this week (weighted
double) plus your best lineup for the rest of the season, with a little credit
for bench depth. Projections are ESPN's, adjusted for how each player has done
against them, injuries and byes.`

func leaguePrefix(p *Plan) string {
	if p.League.Name == "" {
		return ""
	}
	return p.League.Name + ". "
}

func statusMark(s approval.Status) string {
	switch s {
	case approval.Executed:
		return "DONE"
	case approval.Declined:
		return "DECLINED"
	case approval.Skipped:
		return "NOT RUN"
	case approval.Failed:
		return "FAILED"
	case approval.Expired:
		return "EXPIRED"
	case approval.Superseded:
		return "REPLACED"
	case approval.Approved:
		return "APPROVED, WAITING"
	}
	return "WAITING FOR YOU"
}

func resultEmail(st *approval.Store, b *approval.Batch, readAs string) string {
	var body strings.Builder
	if readAs != "" {
		fmt.Fprintf(&body, "I read your reply as: %s.\n\n", readAs)
	}
	for _, it := range st.BatchItems(b) {
		fmt.Fprintf(&body, "%d. [%s] %s\n", it.Number, statusMark(it.Status), it.Summary)
		switch {
		case it.Status == approval.Approved:
			body.WriteString("   Waiting on the move it depends on.\n")
		case it.Result != "" && it.Result != "done":
			fmt.Fprintf(&body, "   %s\n", upperFirst(it.Result))
		}
		body.WriteString("\n")
	}
	body.WriteString("If a move didn't run, I'll propose a fresh one if it still makes sense.\n")
	fmt.Fprintf(&body, "\nRef: %s\n", b.Token)
	return body.String()
}

func digestEmail(st *approval.Store, p *Plan, b *approval.Batch, trades []*approval.Item, now time.Time, loc *time.Location) (string, string) {
	recent := st.Since(now.Add(-24 * time.Hour))
	groups := map[approval.Status][]*approval.Item{}
	for _, it := range recent {
		if it.Batch == b.Token {
			continue
		}
		groups[it.Status] = append(groups[it.Status], it)
	}
	for _, it := range st.Pending() {
		if it.Batch != b.Token && !it.Created.After(now.Add(-24*time.Hour)) {
			groups[approval.Pending] = append(groups[approval.Pending], it)
		}
	}
	subject := fmt.Sprintf("[ffagent #%s] Daily summary: %d done, %d declined, %d waiting for you",
		b.Token, len(groups[approval.Executed]), len(groups[approval.Declined]), len(groups[approval.Pending]))

	var body strings.Builder
	fmt.Fprintf(&body, "%s%s, week %d. %s (%d-%d-%d)\n", leaguePrefix(p), now.In(loc).Format("Mon Jan 2"), p.League.CurrentWeek,
		p.Me.Name, p.Me.Wins, p.Me.Losses, p.Me.Ties)
	if m := p.Matchup; m != nil && m.Opponent != nil {
		fmt.Fprintf(&body, "This week vs %s: you %.1f, them %.1f projected; %.0f%% win probability.\n",
			m.Opponent.Name, m.MyProjected, m.OppProjected, m.WinProb*100)
	}
	body.WriteString("\nMOVES IN THE LAST 24 HOURS\n")
	order := []approval.Status{approval.Executed, approval.Failed, approval.Skipped, approval.Declined, approval.Expired, approval.Superseded, approval.Approved, approval.Pending}
	found := false
	for _, s := range order {
		items := groups[s]
		if len(items) == 0 {
			continue
		}
		found = true
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		fmt.Fprintf(&body, "\n%s (%d)\n", statusMark(s), len(items))
		for _, it := range items {
			fmt.Fprintf(&body, "  - %s  [%s] %s", when(it.Created, loc), kindLabel(it.Kind), it.Summary)
			if it.Result != "" {
				fmt.Fprintf(&body, ": %s", it.Result)
			}
			if s == approval.Pending {
				fmt.Fprintf(&body, " (move %d in the email \"#%s\"; reply there)", it.Number, it.Batch)
			}
			body.WriteString("\n")
		}
	}
	if !found {
		body.WriteString("  None.\n")
	}

	if len(trades) > 0 {
		body.WriteString("\nTRADE IDEAS (reply to this email to send an offer)\n\n")
		for _, it := range trades {
			itemBlock(&body, st, it, loc)
		}
		body.WriteString(replyHelp)
		body.WriteString("\n")
	} else {
		body.WriteString("\nNo trade ideas today that help you without hurting the other team.\n")
	}
	fmt.Fprintf(&body, "\nRef: %s\n", b.Token)
	return subject, body.String()
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
