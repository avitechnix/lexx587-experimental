// Command ffagent manages an ESPN fantasy football team.
//
// By default every command is a dry run that only prints what it would do.
// Pass --apply to make changes on ESPN.
package main

import (
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
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/config"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/news"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/secrets"
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
  watch          Poll injury news and act immediately: grab free agents and
                 file waiver claims the moment an opening appears, and fix
                 the lineup when a starter is ruled out
  secrets set    Store your ESPN cookies in the encrypted vault (hidden input)
  secrets check  Verify the vault decrypts (never prints the values)
  secrets passwd Change the vault passphrase

Flags:
  --config FILE  JSON config (default $FFAGENT_CONFIG; see config.example.json);
                 env vars override:
                 ESPN_LEAGUE_ID, ESPN_SEASON, ESPN_TEAM_ID
  --vault FILE   Encrypted credentials vault (default: $FFAGENT_VAULT or
                 ~/.config/ffagent/secrets.vault)
  --apply        Actually make the changes on ESPN (default: dry run)
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
		if err := runSecrets(strings.TrimPrefix(cmd, "secrets "), *vaultPath); err != nil {
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
	if cmd != "watch" && cmd != "free-agents" {
		loadInjuries(ctx, ag, newsClient)
	}

	switch cmd {
	case "watch":
		interval := *every
		if interval <= 0 {
			interval = time.Duration(cfg.WatchIntervalMinutes) * time.Minute
		}
		w := &agent.Watcher{Agent: ag, News: newsClient, Apply: *apply, Out: out,
			StatePath: filepath.Join(filepath.Dir(*vaultPath), "watch-state.json")}
		fmt.Fprintf(out, "Watching injury news every %s (apply=%v, max %d pickups/day). Ctrl-C to stop.\n",
			interval, *apply, cfg.MaxAcquisitionsPerDay)
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
