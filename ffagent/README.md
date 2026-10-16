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

Everything is a **dry run by default**. Nothing changes on ESPN unless you pass `--apply`.

## Setup

```sh
cd ffagent
go build -o ffagent ./cmd/ffagent
```

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

With `--apply`, roster moves go first, in order, and the run stops at the first
failure. If any move changed who's on the roster, the agent re-reads the league before
setting the lineup.

A good routine: run `waivers --apply` before your league's waiver deadline, and
`run --apply --every 3h` on game days to catch late inactives.

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
- Projections come from ESPN and are adjusted by each player's track record. The agent
  doesn't read news, so a late scratch only shows up once ESPN updates the player's
  status. Running it on a schedule close to kickoff covers most of that.
- Multi-week playoff matchups are treated one scoring week at a time.

## Layout

```
cmd/ffagent         CLI
internal/espn       API client: league/roster/player reads, transactions
internal/analysis   player valuation: accuracy vs projections, volatility, ROS
internal/lineup     exact lineup optimizer (Hungarian assignment)
internal/agent      IR, pickups, matchup strategy, trades, execution, reports
internal/config     JSON + env configuration (rejects plaintext credentials)
internal/secrets    encrypted vault, passphrase sources, redacting Secret type
```

Run the tests with `go test ./...`.
