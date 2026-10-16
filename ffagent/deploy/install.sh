#!/usr/bin/env bash
# Installs ffagent as a hardened systemd service on a Linux server.
#
#   git clone -b claude/espn-fantasy-football-agent-3nhs6i https://github.com/avitechnix/lexx587-experimental
#   cd lexx587-experimental/ffagent && sudo ./deploy/install.sh
#
# Re-running it updates the binary and unit and keeps your config and vault.
# Optional environment: LEAGUE_ID, TEAM_ID (skip the prompts),
# MODE=apply|dry-run (default: ask).
set -euo pipefail

GO_VERSION=go1.27.2
declare -A GO_SHA256=(
  [amd64]=ecbadb99091a3f46e31f5f934b068b1864eafa7995211b39eaddf76996045fe5
  [arm64]=94f3e30b8e374bc285e7dadc11e0865726b9bc6e85b841ccceaabc0214c6b7c8
)
BIN=/usr/local/bin/ffagent
CTL=/usr/local/bin/ffagentctl
ETC=/etc/ffagent
STATE=/var/lib/ffagent
CREDDIR=/etc/credstore.encrypted
CRED=$CREDDIR/ffagent-passphrase
UNIT=/etc/systemd/system/ffagent-watch.service

say()  { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
die()  { printf 'install: %s\n' "$*" >&2; exit 1; }
ask()  { local r; read -r -p "$1" r </dev/tty; printf '%s' "$r"; }
have_tty() { [[ -r /dev/tty && -w /dev/tty ]] && { : </dev/tty; } 2>/dev/null; }

SRC=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
[[ -f $SRC/go.mod && -d $SRC/cmd/ffagent ]] || die "run this from the ffagent source tree"

# --- preflight --------------------------------------------------------------
[[ $EUID -eq 0 ]] || die "run with sudo"
[[ $(uname -s) == Linux ]] || die "Linux with systemd is required"
command -v systemctl >/dev/null && [[ -d /run/systemd/system ]] || die "systemd isn't running on this machine"
sdver=$(systemctl --version | awk 'NR==1{print $2}')
(( sdver >= 250 )) || die "systemd $sdver is too old; 250+ is needed for encrypted credentials"
command -v systemd-creds >/dev/null || die "systemd-creds not found"
case $(uname -m) in
  x86_64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported CPU $(uname -m)" ;;
esac
for t in curl sha256sum tar useradd; do command -v $t >/dev/null || die "$t is required"; done

# --- Go toolchain -------------------------------------------------------------
# Any Go >= 1.21 works: go.mod pins a patched toolchain, which the go command
# downloads and verifies against the Go checksum database.
GO=$(command -v go || true)
if [[ -n $GO ]] && ! "$GO" version | grep -Eq 'go1\.(2[1-9]|[3-9][0-9])'; then GO=""; fi
if [[ -z $GO ]]; then
  say "Installing $GO_VERSION (checksum-verified)"
  tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
  tgz=$GO_VERSION.linux-$ARCH.tar.gz
  curl -fsSL --proto '=https' --tlsv1.2 -o "$tmp/$tgz" "https://go.dev/dl/$tgz"
  echo "${GO_SHA256[$ARCH]}  $tmp/$tgz" | sha256sum -c --quiet - || die "Go download failed checksum verification"
  rm -rf /usr/local/go && tar -C /usr/local -xzf "$tmp/$tgz"
  GO=/usr/local/go/bin/go
fi

# --- build ------------------------------------------------------------------
say "Building ffagent"
build=$(mktemp -d)
(cd "$SRC" && GOTOOLCHAIN=auto GOPATH=$build/gopath GOCACHE=$build/cache GOFLAGS=-mod=readonly \
  "$GO" build -trimpath -o "$build/ffagent" ./cmd/ffagent)
install -m 0755 "$build/ffagent" "$BIN"
install -m 0755 "$SRC/deploy/ffagentctl" "$CTL"
rm -rf "$build"
"$BIN" 2>&1 | head -1 || true # usage screen; exits non-zero by design

# --- service account ----------------------------------------------------------
if ! id ffagent >/dev/null 2>&1; then
  say "Creating the ffagent system user"
  useradd --system --home-dir "$STATE" --no-create-home --shell /usr/sbin/nologin ffagent
fi
install -d -m 0700 -o ffagent -g ffagent "$STATE"
install -d -m 0755 "$ETC"

# --- config -------------------------------------------------------------------
if [[ ! -f $ETC/config.json ]]; then
  say "League settings"
  league=${LEAGUE_ID:-}
  [[ -n $league ]] || { have_tty && league=$(ask "ESPN league ID (the leagueId= number in your league URL): "); }
  [[ $league =~ ^[0-9]+$ ]] || die "league ID must be a number"
  team=${TEAM_ID:-}
  [[ -n $team ]] || { have_tty && team=$(ask "Your team ID (Enter to detect it from your login): "); }
  team=${team:-0}
  [[ $team =~ ^[0-9]+$ ]] || die "team ID must be a number"
  season=$(date +%Y); (( 10#$(date +%m) < 3 )) && season=$((season - 1))
  sed -e "s/\"league_id\": [0-9]*/\"league_id\": $league/" \
      -e "s/\"season\": [0-9]*/\"season\": $season/" \
      -e "s/\"team_id\": [0-9]*/\"team_id\": $team/" \
      -e 's/"protected_players": \[[^]]*\]/"protected_players": []/' \
      "$SRC/config.example.json" > "$ETC/config.json"
  chown root:ffagent "$ETC/config.json"; chmod 0640 "$ETC/config.json"
  echo "Wrote $ETC/config.json (edit it to protect players or tune behavior)"
fi

# --- vault passphrase: random, never displayed, sealed by systemd -----------------
if [[ ! -f $CRED ]]; then
  say "Generating the vault passphrase (encrypted with this machine's key; TPM2 if present)"
  install -d -m 0700 "$CREDDIR"
  # The passphrase only ever exists in this pipe.
  head -c 48 /dev/urandom | base64 -w0 | systemd-creds encrypt --name=ffagent-passphrase - "$CRED"
  chmod 0600 "$CRED"
fi

# --- unit -------------------------------------------------------------------
install -m 0644 "$SRC/deploy/ffagent-watch.service" "$UNIT"
[[ -f $ETC/watch.env ]] || { echo 'FFAGENT_WATCH_FLAGS=' > "$ETC/watch.env"; chmod 0644 "$ETC/watch.env"; }
systemctl daemon-reload

# --- ESPN cookies -------------------------------------------------------------
if [[ ! -f $STATE/secrets.vault ]]; then
  say "ESPN login cookies"
  cat <<'TXT'
In a browser where you're logged in to espn.com, open developer tools:
  Chrome/Edge: Application > Cookies > https://www.espn.com
  Firefox:     Storage > Cookies > https://www.espn.com
Copy the values of "espn_s2" and "SWID". Paste them below; input is hidden
and they're stored only in the encrypted vault.
TXT
  have_tty || die "a terminal is needed to enter the cookies; re-run interactively"
  "$CTL" secrets set </dev/tty || die "storing the cookies failed; re-run: sudo ffagentctl secrets set"
fi

# --- verify ------------------------------------------------------------------
say "Checking the connection to your league (read-only)"
if ! "$CTL" matchup </dev/null; then
  die "couldn't read your league. Fix the issue above (often the cookies: sudo ffagentctl secrets set), then re-run this installer."
fi

# --- start --------------------------------------------------------------------
mode=${MODE:-}
if [[ -z $mode ]]; then
  cur=$(grep -q -- --apply "$ETC/watch.env" && echo apply || echo dry-run)
  if have_tty; then
    echo
    echo "The watcher can start in dry-run mode (logs what it would do) or apply mode (makes moves)."
    echo "ESPN's write API is unofficial, so dry-run for a day and review 'sudo ffagentctl logs' is a good first step."
    r=$(ask "Start in apply mode now? [y/N] (current: $cur): ")
    [[ $r =~ ^[Yy] ]] && mode=apply || mode=dry-run
  else
    mode=$cur
  fi
fi
case $mode in
  apply)   echo 'FFAGENT_WATCH_FLAGS=--apply' > "$ETC/watch.env" ;;
  dry-run) echo 'FFAGENT_WATCH_FLAGS=' > "$ETC/watch.env" ;;
  *) die "MODE must be apply or dry-run" ;;
esac
systemctl enable --quiet ffagent-watch.service
systemctl restart ffagent-watch.service
sleep 2
systemctl is-active --quiet ffagent-watch.service || { journalctl -u ffagent-watch -n 30 --no-pager; die "the watcher didn't start"; }

say "Done. The watcher is running in $mode mode and starts on boot."
cat <<TXT
  sudo ffagentctl logs -f        follow what it sees and does
  sudo ffagentctl status         service status
  sudo ffagentctl analyze        roster report (also: matchup, lineup, waivers, trades)
  sudo ffagentctl mode apply     let it make moves (or: mode dry-run)
  sudo ffagentctl secrets set    refresh the ESPN cookies when they expire
  sudo $SRC/deploy/uninstall.sh  remove everything
TXT
