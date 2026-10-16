# ffagent — ESPN fantasy football team manager

A Go agent that manages your ESPN fantasy football team. Each run it:

1. **Analyzes every player.** It compares each player's weekly actual points to ESPN's
   weekly projections. From that it works out a projection-accuracy factor, shrunk
   toward 100% until there are enough games to trust it. It also tracks season and
   last-3 averages, week-to-week volatility, and injury and bye status. The result is
   an adjusted projection for this week and a rest-of-season (ROS) value.
2. **Keeps IR legal and useful.**
   - Healthy players sitting in IR are activated. ESPN blocks every other move until
     that's fixed. If the roster is full, the least valuable player is dropped first.
   - Players who are OUT or on INJURY_RESERVE are moved into open IR slots, most
     valuable first.
3. **Finds a replacement and works the waiver wire.** It pulls the top free agents and
   waiver players at each position. It tries every possible add/drop pair, including
   adds into a spot freed by an IR move, and keeps the pickups that most improve the
   team. Team value means this week's best lineup (weighted up), plus the best ROS
   lineup over future weeks, plus a little credit for bench depth. Free agents are
   added right away. Waiver players get a claim with a FAAB bid sized to the upgrade
   (kickers and defenses get small bids because they're streamable).
4. **Sets the lineup to maximize your chance of beating this week's opponent.** It
   solves the lineup exactly (an assignment solver, so FLEX and superflex slots are
   handled correctly). Injured and bye-week players are benched. Players whose games
   have started stay where they are. It also projects your opponent's best possible
   lineup and computes your win probability. Then it tries lineups that lean toward
   upside or toward safety, and keeps the one with the highest win probability. When
   you're the underdog it starts boom/bust players; when you're the favorite it
   starts high-floor players.
5. **Suggests trades.** It searches 1-for-1, 2-for-1 and 1-for-2 deals with every team.
   A deal qualifies only if it raises your ROS lineup value and leaves the other team
   no worse than a small tolerance by the same measure, so they have a reason to say
   yes. It can send the offers for you.

6. **Watches injury news and moves first.** `ffagent watch` polls ESPN's league-wide
   injury report and NFL headlines every few minutes. When a starter is ruled out, it
   immediately proposes adding his backup (or a waiver claim) and benching the injured
   starter (see [Injury news and watch mode](#injury-news-and-watch-mode)).
7. **Asks you first.** No move runs without your OK. The watcher emails you every
   proposed move with its rationale, and runs only the moves you approve by replying.
   When you run `--apply` yourself, it asks you in the terminal. You also get a daily
   summary of everything proposed, approved, declined and done (see
   [Approvals and email](#approvals-and-email)).

Everything is a **dry run by default**. Nothing changes on ESPN unless you pass
`--apply`, and even then each move needs your confirmation.

## Server install (recommended)

On any Linux server with systemd 250+ (Ubuntu 22.04+, Debian 12+, Fedora, etc.), for
example a small VPS or a Raspberry Pi:

```sh
git clone -b claude/espn-fantasy-football-agent-3nhs6i https://github.com/avitechnix/lexx587-experimental
cd lexx587-experimental/ffagent
sudo ./deploy/install.sh
```

The installer:

1. **Installs Go if needed.** It downloads Go 1.27.2 and checks it against a pinned
   SHA-256.
2. **Builds** the agent.
3. **Creates a locked-down `ffagent` system user.**
4. **Asks for your league ID** (and optionally your team ID).
5. **Generates a random vault passphrase.** It's sealed with `systemd-creds` (bound to
   the TPM2 chip if the machine has one). You never see or type it; it exists in
   plaintext only inside a pipe at setup time and in systemd's private, in-memory
   credential directory while the service runs.
6. **Asks you to paste the two ESPN cookies** into hidden prompts, then sets up email:
   - the agent's Gmail address, your address and your time zone,
   - the Gmail app password (also a hidden prompt),
   - a test email, to confirm that sending and reading replies both work.
7. **Checks it can read your league**, then starts `ffagent watch` as a
   sandboxed service that restarts on failure and on boot. It runs with no
   privileges, a read-only filesystem except its state directory, no core dumps,
   and network access limited to IP sockets. You choose approve mode (moves are
   emailed to you, and only the ones you approve run) or dry-run mode.

Day to day:

```sh
sudo ffagentctl logs -f        # what it sees and does
sudo ffagentctl analyze        # also: matchup, lineup, waivers, trades, free-agents
sudo ffagentctl mode apply     # or: mode dry-run
sudo ffagentctl secrets set    # when ESPN cookies expire (you'll see 401 errors in the logs)
sudo ./deploy/install.sh       # update after pulling new code; keeps config and vault
sudo ./deploy/uninstall.sh
```

Settings are in `/etc/ffagent/config.json`. Restart after editing:
`sudo systemctl restart ffagent-watch`.

## Setup (manual / development)

```sh
cd ffagent
go build -o ffagent ./cmd/ffagent
```

This needs Go 1.26.9 or newer. With an older Go installed, the `go` command fetches the
pinned, patched toolchain (`go1.27.2`) automatically. Don't override that with
`GOTOOLCHAIN=local`: older Go releases have known vulnerabilities in `net/http`,
`crypto/tls` and `crypto/x509`, which this agent relies on.

### Security updates

- `.github/workflows/ffagent-security.yml` runs the tests and
  [govulncheck](https://go.dev/doc/security/vuln/) on every change and weekly. It fails
  if any known vulnerability is reachable from this code.
- Dependabot opens PRs for Go module and GitHub Actions updates.
- To scan locally:
  `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`

You need your league ID and two cookies from a logged-in browser session on espn.com.
The cookies are needed for private leagues and for making changes:

| Setting | Where to find it |
|---|---|
| `ESPN_LEAGUE_ID` | The `leagueId=` value in your league's URL. Not a secret; set it in the env or config. |
| `espn_s2` | Browser dev tools → Application/Storage → Cookies → `espn_s2` |
| `SWID` | Same place, cookie `SWID` (looks like `{XXXXXXXX-...}`) |
| `ESPN_TEAM_ID` | Optional. Detected from your SWID. Otherwise it's the `teamId=` in your team URL |

### Storing credentials (encrypted only)

The cookies are kept **only** in an encrypted vault:

```sh
./ffagent secrets set      # choose a passphrase, then paste the cookies (input is hidden)
./ffagent secrets check    # confirms the vault decrypts; never prints values
./ffagent secrets passwd   # change the passphrase
```

- **Encryption.** The vault (`~/.config/ffagent/secrets.vault`, or `$FFAGENT_VAULT`)
  is encrypted with AES-256-GCM. The key is derived from your passphrase with Argon2id
  (64 MiB, 3 passes). The file header is authenticated, so tampering or a parameter
  downgrade is detected. The file is written with mode `600`.
- **Plaintext refused.** The agent won't start if credentials appear in plaintext:
  `espn_s2`/`swid` in the config file, or `ESPN_S2`/`ESPN_SWID` in the environment
  (it also scrubs them from the environment).
- **Redacted in output.** In memory, credentials are a `Secret` type that prints as
  `[REDACTED]` under every format verb, in JSON and in structured logs. They're only
  revealed when the HTTPS request to ESPN is built. If ESPN's error responses echo
  them back, they're scrubbed.
- **Typed, never passed.** Secrets are never command-line arguments, which are visible
  in `ps`. `secrets set` reads only from a real terminal with echo off and refuses
  piped input.

**Unattended runs** need the passphrase without a prompt. In order of preference:

1. **systemd encrypted credential** (recommended on a server). The passphrase is
   encrypted with the machine's key (TPM when available), and systemd decrypts it only
   for the service, into a private in-memory directory:
   ```sh
   systemd-ask-password -n "Vault passphrase:" | sudo systemd-creds encrypt --name=ffagent-passphrase - /etc/credstore.encrypted/ffagent-passphrase
   # in the service unit:
   #   LoadCredentialEncrypted=ffagent-passphrase
   ```
2. **CI secret store** (e.g. a GitHub Actions secret exposed as `FFAGENT_PASSPHRASE`).
   GitHub stores it encrypted and masks it in logs. The agent removes it from its
   environment as soon as it reads it.
3. **`FFAGENT_PASSPHRASE_FILE`** pointing at a file only you can read. The agent
   refuses the file unless its mode is `600` or stricter. This is the weakest option,
   because the passphrase sits on disk unencrypted. Use it only on an encrypted disk.

Never paste the cookies or the passphrase into a chat, a ticket, or a shell command.

## Usage

```sh
./ffagent analyze                  # roster table: ESPN vs adjusted projection, accuracy, ROS
./ffagent matchup                  # opponent, win probability, chosen lineup strategy
./ffagent lineup                   # recommended lineup and moves
./ffagent waivers                  # IR moves + pickups/claims (with FAAB bids)
./ffagent trades                   # trade ideas
./ffagent free-agents --pos RB     # best available at a position

./ffagent run                      # everything, dry run
./ffagent run --apply              # do it: IR, pickups, claims, then set the lineup
./ffagent run --apply --propose    # ...and send the trade offers too
./ffagent run --apply --every 6h   # keep managing the team on a schedule
```

Only one applying instance runs at a time. `--apply` takes an exclusive lock, so a
watcher and a scheduled run can't both make moves at once.

With `--apply`, roster moves go first, in order, and the run stops at the first
failure. If any move changed who's on the roster, the agent re-reads the league before
setting the lineup.

A good routine is to keep `watch --apply` running all week, and run `waivers --apply`
once before your league's waiver deadline.

## Injury news and watch mode

```sh
./ffagent watch                 # dry run: prints injury changes and what it would do
./ffagent watch --apply         # act on them as they happen
./ffagent watch --apply --every 2m
```

Every interval, the watcher:

1. **Reads ESPN's injury report** for all 32 teams and the latest NFL headlines. These
   are public feeds, and their player IDs are the same as fantasy player IDs.
2. **Detects changes** since the last check, such as Questionable → Out, a move to IR,
   or a player cleared from the report. It also flags new headlines about your
   players or your opponent's.
3. **Re-plans right away** when anything changed, and otherwise at least hourly.
   - **Breaking news first.** The fresh status updates projections and the lineup
     before ESPN Fantasy's projections catch up.
   - **Next man up.** When a starter goes OUT or to IR, his same-team backup gets a
     share of the lost production: QB 90%, RB 55%, TE 45%, WR 30% per game. The boost
     covers the expected games missed (IR means at least 4). The agent also searches
     free agents on that NFL team specifically, so a low-owned backup still gets
     found.
4. **Asks you.** The proposed moves are emailed to you right away. With
   `approval_mode: "auto"`, the watcher acts without asking instead.
   - **Free agents** are first come, first served, so the faster you approve, the
     better your chance of beating other managers to him.
   - **Waiver players** get a claim filed once you approve. Waivers aren't won by
     speed: ESPN awards them by priority or FAAB bid when they process.
   - **Lineup.** Benching the injured starter is part of the same proposal.

Safeguards:

- **Boosts only for fresh news.** Opportunity boosts apply only to reports newer than
  `news_fresh_hours` (48). After that, ESPN's projections have caught up, and boosting
  again would double-count.
- **Returning players are left alone.** Players designated to return (`IR-R`, `PUP-R`,
  `NFI-R`) never boost their backups.
- **IR legality follows ESPN Fantasy.** Projections and lineups use the freshest
  status. IR moves use ESPN Fantasy's own status, because that's what ESPN enforces,
  so a move ESPN would reject is never attempted.
- **Pickup limits.** At most `max_acquisitions_per_day` pickups (3). Pickups are never
  dropped again within `keep_new_pickups_days` (7), so the agent doesn't churn its own
  adds.
- **State survives restarts.** Seen statuses, seen headlines and the pickup log are
  stored in `~/.config/ffagent/watch-state.json`, so a restart doesn't react to old
  news again.

The watcher needs the vault passphrase without a prompt. Run it as a service using one
of the unattended options under [Storing credentials](#storing-credentials-encrypted-only).

### Tuning (`config.example.json`)

- `protected_players`: names or ESPN IDs that are never dropped or traded.
- `ir_eligible_statuses`: match your league's IR rules.
- `max_acquisitions_per_run` and `min_pickup_gain`: how aggressive to be on the wire.
- `this_week_weight`: how much this week's matchup counts compared with a future week.
  Raise it to chase this week's win harder.
- `max_faab_fraction`: the most of your remaining budget one claim can use.
- `risk_aware`: turn the win-probability lineup logic on or off.
- `min_trade_gain`, `trade_tolerance`: how good a trade must be for you, and how much
  the partner can lose by the model's measure before the offer is dropped.

## Approvals and email

In `approval_mode: "email"` (the default), nothing happens on ESPN until you approve it.

1. **Proposal.** When the agent finds moves, it emails you one message with them
   numbered. Each move shows:
   - what it is,
   - **why**: the injury or opportunity behind it, projections for the player added
     and the player dropped, and the change in team value,
   - which other move it depends on, if any,
   - when it expires (12 hours, or kickoff for lineup moves).

   The email opens with your win probability as your lineup is set now, and with the
   moves.
2. **Your reply.** Reply from any mail app: `approve all`, `yes`, `approve 1 3`,
   `decline 2`, `approve 1, decline 2`, or `no`. Quoted text and signatures are
   ignored. If a reply is ambiguous, it counts as a decline.
3. **Re-check, then run.** The agent checks for replies every minute. Before running
   an approved move, it re-checks it against fresh league data. A move doesn't run if:
   - the player was taken or got hurt,
   - his game has started,
   - your roster changed, or
   - a move it depends on was declined.

   You get a reply in the same thread saying what ran and why anything didn't.
4. **Daily summary.** Each morning (`digest_hour`, your `time_zone`) you get a summary
   of the last 24 hours: done, declined, expired, not run, and still waiting. It also
   lists the day's **trade ideas**, which you can approve by replying.

How replies are protected:

- **Sender check.** Only replies from `notify_to` count.
- **One-time code.** Each email carries a random 80-bit code in its subject, which a
  normal reply keeps automatically. Someone who never saw the email can't approve
  anything.
- **No self-approval.** The agent remembers the IDs of the messages it sent, so it
  never reads its own email as a reply.

Declined moves aren't proposed again for 24 hours (`declined_cooldown_hours`). Moves
already waiting aren't sent twice.

**Gmail setup.** Create a separate Gmail account for the agent: its app password
grants full access to its mailbox, so don't use your personal one. In that account:

1. Turn on 2-Step Verification.
2. Create an app password at <https://myaccount.google.com/apppasswords>.
3. Store it with `ffagent secrets set email`. Like the cookies, it's kept only in the
   encrypted vault.

Then put the agent's address in `email.address` and yours in `email.notify_to`, and
check everything with `ffagent email-test`. The server installer walks you through all
of this.

## Caveats

- **Limits of encryption.** The credentials have to exist in plaintext in memory for
  the moment they're sent to ESPN over HTTPS. Go can't reliably wipe strings, so
  someone with root on the running machine (or reading its swap or core dumps) could
  still recover them. Encrypt the disk, and disable core dumps on servers.
- ESPN's fantasy API is **unofficial and undocumented**. Reading league data is
  well understood. The write endpoint (lineup, add/drop, waiver claims, trade offers)
  follows the payloads ESPN's own web app sends, but ESPN can change it without notice.
  Try one command with `--apply` and check the result on espn.com before you schedule
  it.
- **News coverage.** News comes from ESPN's injury report and headline tags. Status
  changes are acted on, but the agent doesn't read article text. A depth-chart change
  with no injury, such as "X will start", only counts once ESPN's projections reflect
  it.
- **Feed format.** The news feeds are also unofficial. If ESPN changes them, the
  watcher logs an error and retries on the next interval.
- Multi-week playoff matchups are treated one scoring week at a time.

## Layout

```
cmd/ffagent         CLI
internal/espn       API client: league/roster/player reads, transactions
internal/analysis   player valuation: accuracy vs projections, volatility, ROS
internal/lineup     exact lineup optimizer (Hungarian assignment)
internal/agent      IR, pickups, matchup strategy, trades, execution, reports
internal/config     JSON + env configuration (rejects plaintext credentials)
internal/news       ESPN injury report and headline feeds
internal/approval  approval queue, reply parsing, decision history
internal/mail      SMTP sending and minimal IMAP reply reading (TLS only)
internal/secrets    encrypted vault, passphrase sources, redacting Secret type
```

Run the tests with `go test ./...`.
