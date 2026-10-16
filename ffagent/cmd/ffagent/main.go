// Command ffagent manages an ESPN fantasy football team.
//
// By default every command is a dry run that only prints what it would do.
// Pass --apply to make changes on ESPN.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/agent"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/analysis"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/approval"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/config"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/mail"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/news"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/secrets"
	"golang.org/x/term"
)

const usage = `ffagent — an ESPN fantasy football team manager

Usage:
  ffagent <command> [flags]

Commands:
  analyze        Roster analysis: projections vs actuals, volatility, ROS value
  matchup        This week's opponent, win probability and lineup strategy
  lineup         Optimal lineup for this week (bench injured / bye players)
  waivers        IR moves plus the best waiver claims and free-agent pickups
  trades         Trade ideas that help you and plausibly help your partner
  free-agents    Best available players (use --pos QB|RB|WR|TE|K|D/ST)
  run            Everything above; with --apply, executes IR, pickups, lineup
  watch          Poll injury news; the moment an opening appears, email you the
                 moves with their rationale, and run the ones you approve by
                 replying (approval_mode "email", the default)
  email-test     Send a test email and check the agent can read replies
  secrets set    Store your ESPN cookies in the encrypted vault (hidden input)
  secrets set email   Store the email account's app password
  secrets check  Verify the vault decrypts (never prints the values)
  secrets passwd Change the vault passphrase

Flags:
  --config FILE  JSON config (default $FFAGENT_CONFIG; see config.example.json);
                 env vars override:
                 ESPN_LEAGUE_ID, ESPN_SEASON, ESPN_TEAM_ID
  --vault FILE   Encrypted credentials vault (default: $FFAGENT_VAULT or
                 ~/.config/ffagent/secrets.vault)
  --apply        Make changes on ESPN (default: dry run). You'll be asked to
                 confirm each move, with its rationale, before it runs
  --propose      With trades/run --apply: also send trade offers
  --every DUR    With run: repeat on an interval (e.g. 6h) until interrupted;
                 with watch: polling interval (default 5m, minimum 1m)
  --pos POS      With free-agents: position filter
  --limit N      With free-agents: number of players to show (default 20)
`

func main() {
	if len(os.Args) < 2 || strings.HasPrefix(os.Args[1], "-") {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	if cmd == "secrets" {
		if len(args) == 0 {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		cmd, args = "secrets "+args[0], args[1:]
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	cfgPath := fs.String("config", os.Getenv("FFAGENT_CONFIG"), "config file")
	vaultPath := fs.String("vault", secrets.DefaultVaultPath(), "encrypted credentials vault")
	apply := fs.Bool("apply", false, "make changes on ESPN")
	propose := fs.Bool("propose", false, "send trade offers")
	every := fs.Duration("every", 0, "repeat interval for run")
	pos := fs.String("pos", "", "free-agent position filter")
	limit := fs.Int("limit", 20, "free-agent rows")
	_ = fs.Parse(args)

	if strings.HasPrefix(cmd, "secrets ") {
		if err := runSecrets(strings.TrimPrefix(cmd, "secrets "), fs.Args(), *vaultPath); err != nil {
			fatal(err)
		}
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal(err)
	}
	if _, err := os.Stat(*vaultPath); err == nil {
		if err := cfg.LoadSecrets(*vaultPath); err != nil {
			fatal(err)
		}
	} else if *apply {
		fatal(fmt.Errorf("no credentials vault at %s; run `ffagent secrets set` first", *vaultPath))
	} else {
		fmt.Fprintf(os.Stderr, "note: no credentials vault at %s; reading as a public league\n", *vaultPath)
	}
	if *propose {
		cfg.ProposeTrades = true
	}
	client := espn.NewClient(cfg.LeagueID, cfg.Season, cfg.ESPNS2, cfg.SWID)
	ag := agent.New(client, cfg)
	newsClient := news.NewClient()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	out := os.Stdout

	if *apply {
		release, err := acquireLock(filepath.Join(filepath.Dir(*vaultPath), "ffagent.lock"))
		if err != nil {
			fatal(err)
		}
		defer release()
	}
	if *apply && cmd != "watch" {
		// You're running this yourself: confirm each move in the terminal.
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			fatal(fmt.Errorf("--apply asks you to confirm each move, so it needs a terminal; for unattended use, run the watcher with email approvals"))
		}
		ag.Confirm = confirmInTerminal(os.Stdin, out)
	}
	if cmd != "watch" && cmd != "free-agents" && cmd != "email-test" {
		loadInjuries(ctx, ag, newsClient)
	}

	switch cmd {
	case "email-test":
		if err := emailTest(ctx, cfg); err != nil {
			fatal(err)
		}
	case "watch":
		interval := *every
		if interval <= 0 {
			interval = time.Duration(cfg.WatchIntervalMinutes) * time.Minute
		}
		w := &agent.Watcher{Agent: ag, News: newsClient, Apply: *apply, Out: out,
			StatePath: filepath.Join(filepath.Dir(*vaultPath), "watch-state.json")}
		if acct := mailAccount(cfg); acct != nil {
			w.Mailer = acct
		}
		mode := "dry run: logging only"
		if *apply && cfg.ApprovalMode == "email" {
			mode = "every move is emailed to " + cfg.Email.NotifyTo + " for approval"
		} else if *apply {
			mode = "approval_mode=auto: moves are made without asking"
		}
		fmt.Fprintf(out, "Watching injury news every %s (%s). Ctrl-C to stop.\n", interval, mode)
		if err := w.Run(ctx, interval); err != nil {
			fatal(err)
		}
	case "analyze":
		p := plan(ctx, ag)
		agent.PrintHeader(out, p)
		agent.PrintRoster(out, p)
		agent.PrintMatchup(out, p)
	case "matchup":
		p := plan(ctx, ag)
		agent.PrintHeader(out, p)
		agent.PrintMatchup(out, p)
		agent.PrintLineup(out, p)
	case "lineup":
		p, err := ag.PlanLineup(ctx)
		if err != nil {
			fatal(err)
		}
		agent.PrintHeader(out, p)
		agent.PrintMatchup(out, p)
		agent.PrintLineup(out, p)
		maybeApply(ctx, ag, p, *apply, agent.Scope{Lineup: true}, out)
	case "waivers":
		p := plan(ctx, ag)
		agent.PrintHeader(out, p)
		agent.PrintActions(out, p)
		maybeApply(ctx, ag, p, *apply, agent.Scope{Roster: true}, out)
	case "trades":
		p := plan(ctx, ag)
		agent.PrintHeader(out, p)
		agent.PrintTrades(out, p)
		if cfg.ProposeTrades {
			maybeApply(ctx, ag, p, *apply, agent.Scope{Trades: true}, out)
		}
	case "free-agents":
		freeAgents(ctx, client, ag, *pos, *limit, out)
	case "run":
		for {
			runOnce(ctx, ag, *apply, cfg.ProposeTrades, out)
			if *every <= 0 {
				return
			}
			fmt.Fprintf(out, "Next run at %s\n\n", time.Now().Add(*every).Format(time.Kitchen))
			select {
			case <-ctx.Done():
				return
			case <-time.After(*every):
			}
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

// mailAccount returns the configured email account, or nil if email isn't
// set up.
func mailAccount(cfg config.Config) *mail.Account {
	if !cfg.Email.Enabled() || cfg.EmailPassword.Empty() {
		return nil
	}
	e := cfg.Email
	return &mail.Account{Address: e.Address, Password: cfg.EmailPassword,
		SMTPHost: e.SMTPHost, SMTPPort: e.SMTPPort, IMAPHost: e.IMAPHost, IMAPPort: e.IMAPPort}
}

// emailTest sends a test message and checks the inbox can be read.
func emailTest(ctx context.Context, cfg config.Config) error {
	acct := mailAccount(cfg)
	if acct == nil {
		return fmt.Errorf("email isn't set up: fill in \"email\" (address, notify_to) in the config and run `ffagent secrets set email`")
	}
	if _, err := acct.Send(ctx, mail.Message{To: cfg.Email.NotifyTo, Subject: "ffagent: test email",
		Body: "This is a test from your fantasy football agent. Proposals will look like this thread:\nreply to them with \"approve all\", \"approve 1 3\" or \"decline 2\".\n"}); err != nil {
		return err
	}
	fmt.Printf("Sent a test email from %s to %s.\n", cfg.Email.Address, cfg.Email.NotifyTo)
	if _, err := acct.Replies(ctx, time.Now().Add(-time.Hour), approval.SubjectTag); err != nil {
		return fmt.Errorf("sending works, but reading the inbox failed: %w", err)
	}
	fmt.Println("Reading the inbox for replies works too.")
	return nil
}

// confirmInTerminal shows each move with its rationale and asks y/N.
func confirmInTerminal(in io.Reader, out io.Writer) func(agent.Action) bool {
	r := bufio.NewReader(in)
	return func(act agent.Action) bool {
		fmt.Fprintf(out, "\n[%s] %s\n", act.Kind, act.Summary)
		if act.Reason != "" {
			fmt.Fprintf(out, "  Why: %s\n", act.Reason)
		}
		fmt.Fprint(out, "Make this move? [y/N] ")
		line, _ := r.ReadString('\n')
		a := strings.ToLower(strings.TrimSpace(line))
		return a == "y" || a == "yes"
	}
}

// loadInjuries applies the latest injury report to planning, if available.
func loadInjuries(ctx context.Context, ag *agent.Agent, nc *news.Client) {
	reports, err := nc.Injuries(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "note: injury report unavailable:", err)
		return
	}
	ag.Injuries = map[int]analysis.Injury{}
	for id, r := range reports {
		ag.Injuries[id] = analysis.Injury{PlayerID: id, ProTeamID: r.ProTeamID, Status: r.Status, Date: r.Date, Returning: r.Returning}
	}
}

func plan(ctx context.Context, ag *agent.Agent) *agent.Plan {
	p, err := ag.Plan(ctx)
	if err != nil {
		fatal(err)
	}
	return p
}

func runOnce(ctx context.Context, ag *agent.Agent, apply, propose bool, out io.Writer) {
	fmt.Fprintf(out, "=== ffagent run %s ===\n", time.Now().Format(time.RFC1123))
	p, err := ag.Plan(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return
	}
	agent.PrintHeader(out, p)
	agent.PrintRoster(out, p)
	agent.PrintMatchup(out, p)
	agent.PrintActions(out, p)
	agent.PrintLineup(out, p)
	agent.PrintTrades(out, p)
	maybeApply(ctx, ag, p, apply, agent.Scope{Roster: true, Lineup: true, Trades: propose}, out)
}

func maybeApply(ctx context.Context, ag *agent.Agent, p *agent.Plan, apply bool, scope agent.Scope, out io.Writer) {
	if !apply {
		fmt.Fprintln(out, "Dry run: nothing was changed. Re-run with --apply to make these moves.")
		return
	}
	fmt.Fprintln(out, "Applying changes on ESPN...")
	if err := ag.Execute(ctx, p, scope, out); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return
	}
	fmt.Fprintln(out, "Done.")
}

func freeAgents(ctx context.Context, c *espn.Client, ag *agent.Agent, pos string, limit int, out io.Writer) {
	l, err := c.FetchLeague(ctx)
	if l == nil {
		fatal(err)
	}
	ps, err := c.FreeAgents(ctx, l, strings.ToUpper(pos), nil, max(limit*2, 50))
	if err != nil {
		fatal(err)
	}
	values := analysis.ValueAll(l, ps, ag.Opts)
	fmt.Fprintf(out, "Best available %s — week %d\n", strings.ToUpper(pos), l.CurrentWeek)
	agent.PrintFreeAgents(out, l, ps, values, limit)
}

func fatal(err error) {
	if err == nil {
		err = errors.New("unknown error")
	}
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
