// Package approval keeps the queue of moves waiting for the owner's OK,
// parses email replies, and records every decision and outcome.
package approval

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
)

// Status is where an item is in its life.
type Status string

const (
	Pending    Status = "pending"
	Approved   Status = "approved"
	Declined   Status = "declined"
	Expired    Status = "expired"
	Superseded Status = "superseded"
	Executed   Status = "executed"
	Failed     Status = "failed"
	Skipped    Status = "skipped" // approved, but no longer valid when re-checked
)

// Item is one proposed move.
type Item struct {
	ID        int               `json:"id"`
	Batch     string            `json:"batch"`
	Number    int               `json:"number"` // 1-based position within its batch
	Kind      string            `json:"kind"`
	Summary   string            `json:"summary"`
	Reason    string            `json:"reason"`
	Txn       *espn.Transaction `json:"txn"`
	Signature string            `json:"signature"`
	Players   []int             `json:"players"`
	Depends   []int             `json:"depends,omitempty"` // item IDs
	Created   time.Time         `json:"created"`
	Expires   time.Time         `json:"expires"`
	Status    Status            `json:"status"`
	Decided   time.Time         `json:"decided,omitempty"`
	Via       string            `json:"via,omitempty"` // email, cli
	Result    string            `json:"result,omitempty"`
	Done      time.Time         `json:"done,omitempty"`
}

// Batch is a set of items sent to the owner in one email.
type Batch struct {
	Token     string    `json:"token"`
	Kind      string    `json:"kind"` // moves, digest
	Created   time.Time `json:"created"`
	Items     []int     `json:"items"`
	MessageID string    `json:"message_id"`
	Subject   string    `json:"subject"`
}

// Store is the persisted queue.
type Store struct {
	NextID     int                  `json:"next_id"`
	Items      []*Item              `json:"items"`
	Batches    []*Batch             `json:"batches"`
	Processed  map[string]time.Time `json:"processed_replies"` // reply Message-IDs
	Declined   map[string]time.Time `json:"declined"`          // signature -> when
	LastDigest string               `json:"last_digest"`       // YYYY-MM-DD

	path string
}

// Load reads the store at path, or returns an empty one.
func Load(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(b, s); err != nil {
			return nil, err
		}
	}
	if s.Processed == nil {
		s.Processed = map[string]time.Time{}
	}
	if s.Declined == nil {
		s.Declined = map[string]time.Time{}
	}
	if s.NextID == 0 {
		s.NextID = 1
	}
	return s, nil
}

// Save writes the store atomically, readable only by the owner. Items and
// batches older than 30 days are pruned.
func (s *Store) Save(now time.Time) error {
	cut := now.Add(-30 * 24 * time.Hour)
	items := s.Items[:0]
	for _, it := range s.Items {
		if it.Created.After(cut) || it.Status == Pending {
			items = append(items, it)
		}
	}
	s.Items = items
	batches := s.Batches[:0]
	for _, b := range s.Batches {
		if b.Created.After(cut) {
			batches = append(batches, b)
		}
	}
	s.Batches = batches
	for k, t := range s.Processed {
		if t.Before(cut) {
			delete(s.Processed, k)
		}
	}
	for k, t := range s.Declined {
		if t.Before(cut) {
			delete(s.Declined, k)
		}
	}
	b, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// NewToken returns a random batch code. It authenticates replies: only
// someone who received the proposal email can know it.
func NewToken() string {
	b := make([]byte, 10) // 80 bits
	_, _ = rand.Read(b)
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}

// AddBatch stores items as a new batch; Depends on input are indexes into
// items and are converted to item IDs.
func (s *Store) AddBatch(kind string, items []*Item, now time.Time) *Batch {
	b := &Batch{Token: NewToken(), Kind: kind, Created: now}
	base := s.NextID
	for i, it := range items {
		it.ID = base + i
		it.Batch = b.Token
		it.Number = i + 1
		it.Created = now
		it.Status = Pending
		for j, d := range it.Depends {
			it.Depends[j] = base + d
		}
		b.Items = append(b.Items, it.ID)
	}
	s.NextID = base + len(items)
	s.Items = append(s.Items, items...)
	s.Batches = append(s.Batches, b)
	return b
}

// Item returns an item by ID.
func (s *Store) Item(id int) *Item {
	for _, it := range s.Items {
		if it.ID == id {
			return it
		}
	}
	return nil
}

// Batch returns a batch by token.
func (s *Store) Batch(token string) *Batch {
	for _, b := range s.Batches {
		if b.Token == token {
			return b
		}
	}
	return nil
}

// BatchItems returns a batch's items in order.
func (s *Store) BatchItems(b *Batch) []*Item {
	var out []*Item
	for _, id := range b.Items {
		if it := s.Item(id); it != nil {
			out = append(out, it)
		}
	}
	return out
}

// Pending returns items awaiting a decision.
func (s *Store) Pending() []*Item {
	var out []*Item
	for _, it := range s.Items {
		if it.Status == Pending {
			out = append(out, it)
		}
	}
	return out
}

// Approved returns approved items not yet executed, in ID order.
func (s *Store) Approved() []*Item {
	var out []*Item
	for _, it := range s.Items {
		if it.Status == Approved {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Expire marks pending items past their deadline.
func (s *Store) Expire(now time.Time) []*Item {
	var out []*Item
	for _, it := range s.Items {
		if it.Status == Pending && !it.Expires.IsZero() && now.After(it.Expires) {
			it.Status, it.Done, it.Result = Expired, now, "no reply before it expired"
			out = append(out, it)
		}
	}
	return out
}

// Decide applies a decision to a batch's pending items and returns the
// items it changed.
func (s *Store) Decide(b *Batch, d Decision, via string, now time.Time) []*Item {
	var out []*Item
	for _, it := range s.BatchItems(b) {
		if it.Status != Pending {
			continue
		}
		approve, ok := d.For(it.Number)
		if !ok {
			continue
		}
		it.Decided, it.Via = now, via
		if approve {
			it.Status = Approved
		} else {
			it.Status, it.Done, it.Result = Declined, now, "declined by you"
			s.Declined[it.Signature] = now
		}
		out = append(out, it)
	}
	return out
}

// RecentlyDeclined reports whether a move was declined within cooldown.
func (s *Store) RecentlyDeclined(sig string, now time.Time, cooldown time.Duration) bool {
	t, ok := s.Declined[sig]
	return ok && now.Sub(t) < cooldown
}

// Since returns items created or finished since t.
func (s *Store) Since(t time.Time) []*Item {
	var out []*Item
	for _, it := range s.Items {
		if it.Created.After(t) || it.Done.After(t) {
			out = append(out, it)
		}
	}
	return out
}

// --- reply parsing --------------------------------------------------------

// Decision is what a reply asked for.
type Decision struct {
	AllApprove bool
	AllDecline bool
	Approve    map[int]bool
	Decline    map[int]bool
}

// For returns the decision for item number n (1-based), if any. A decline
// wins over an approval of the same item.
func (d Decision) For(n int) (approve, ok bool) {
	switch {
	case d.Decline[n]:
		return false, true
	case d.Approve[n]:
		return true, true
	case d.AllDecline:
		return false, true
	case d.AllApprove:
		return true, true
	}
	return false, false
}

// Empty reports whether the reply contained no recognizable decision.
func (d Decision) Empty() bool {
	return !d.AllApprove && !d.AllDecline && len(d.Approve) == 0 && len(d.Decline) == 0
}

var (
	approveWords = map[string]bool{"approve": true, "approved": true, "yes": true, "y": true, "ok": true, "okay": true, "accept": true, "confirm": true, "go": true, "do": true}
	declineWords = map[string]bool{"decline": true, "declined": true, "no": true, "n": true, "reject": true, "deny": true, "skip": true, "cancel": true}
	tokenRe      = regexp.MustCompile(`[a-z]+|\d+`)
	quoteStartRe = regexp.MustCompile(`(?i)^(on .+ wrote:|-+ ?original message ?-+|from: .+|sent from my .+)$`)
)

// ParseReply reads decisions from a reply's text, ignoring quoted text
// (the original proposal) and signatures. Examples:
//
//	approve all          yes                      approve 1 3, decline 2
//	yes 1,2  no 4        decline all              #2 no
func ParseReply(text string) Decision {
	d := Decision{Approve: map[int]bool{}, Decline: map[int]bool{}}
	var own []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, ">") || quoteStartRe.MatchString(t) || t == "--" {
			break
		}
		own = append(own, strings.ToLower(t))
	}
	verb, used := "", false // current verb, and whether it has taken numbers
	pending := []int{}      // numbers seen before any verb, e.g. "#2 no"
	finish := func() {
		if verb != "" && !used {
			applyAll(&d, verb) // a bare "yes" / "approve" / "no" means all
		}
	}
	for _, tok := range tokenRe.FindAllString(strings.Join(own, " "), -1) {
		switch {
		case approveWords[tok] || declineWords[tok]:
			finish()
			verb, used = "decline", false
			if approveWords[tok] {
				verb = "approve"
			}
			for _, n := range pending {
				mark(&d, verb, n)
				used = true
			}
			pending = pending[:0]
		case tok == "all" || tok == "everything":
			if verb != "" {
				applyAll(&d, verb)
				used = true
			}
		default:
			n, err := strconv.Atoi(tok)
			if err != nil || n <= 0 || n > 999 {
				continue
			}
			if verb == "" {
				pending = append(pending, n)
				continue
			}
			mark(&d, verb, n)
			used = true
		}
	}
	finish()
	return d
}

// Describe renders a decision for confirmation emails.
func (d Decision) Describe() string {
	var parts []string
	if d.AllApprove {
		parts = append(parts, "approve all")
	}
	if d.AllDecline {
		parts = append(parts, "decline all")
	}
	if len(d.Approve) > 0 {
		parts = append(parts, "approve "+nums(d.Approve))
	}
	if len(d.Decline) > 0 {
		parts = append(parts, "decline "+nums(d.Decline))
	}
	if len(parts) == 0 {
		return "no decision"
	}
	return strings.Join(parts, ", ")
}

func nums(m map[int]bool) string {
	var ns []int
	for n := range m {
		ns = append(ns, n)
	}
	sort.Ints(ns)
	var ss []string
	for _, n := range ns {
		ss = append(ss, strconv.Itoa(n))
	}
	return strings.Join(ss, " ")
}

func mark(d *Decision, verb string, n int) {
	if verb == "approve" {
		d.Approve[n] = true
	} else {
		d.Decline[n] = true
	}
}

func applyAll(d *Decision, verb string) {
	if verb == "approve" {
		d.AllApprove = true
	} else {
		d.AllDecline = true
	}
}

// TokenIn extracts a batch token from a subject like "Re: [ffagent #ABCD]".
func TokenIn(subject string) string {
	m := subjectTokenRe.FindStringSubmatch(subject)
	if m == nil {
		return ""
	}
	return m[1]
}

var subjectTokenRe = regexp.MustCompile(`\[ffagent #([A-Z2-7]{16})\]`)

// SubjectTag is the IMAP search string for replies.
const SubjectTag = "[ffagent #"
