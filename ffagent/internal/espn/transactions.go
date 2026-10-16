package espn

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Transaction types accepted by the ESPN write API.
const (
	TxnRoster    = "ROSTER"         // lineup changes, including IR moves
	TxnFreeAgent = "FREEAGENT"      // immediate add (and optional drop)
	TxnWaiver    = "WAIVER"         // waiver claim (and optional drop)
	TxnTrade     = "TRADE_PROPOSAL" // trade offer to another team
)

// Transaction item types.
const (
	ItemLineup = "LINEUP"
	ItemAdd    = "ADD"
	ItemDrop   = "DROP"
	ItemTrade  = "TRADE"
)

// TxnItem is one player movement inside a transaction.
type TxnItem struct {
	PlayerID         int    `json:"playerId"`
	Type             string `json:"type"`
	FromLineupSlotID *int   `json:"fromLineupSlotId,omitempty"`
	ToLineupSlotID   *int   `json:"toLineupSlotId,omitempty"`
	FromTeamID       *int   `json:"fromTeamId,omitempty"`
	ToTeamID         *int   `json:"toTeamId,omitempty"`
}

// Transaction is the request body for ESPN's transactions endpoint.
type Transaction struct {
	IsLeagueManager bool      `json:"isLeagueManager"`
	TeamID          int       `json:"teamId"`
	Type            string    `json:"type"`
	MemberID        string    `json:"memberId"`
	ScoringPeriodID int       `json:"scoringPeriodId"`
	ExecutionType   string    `json:"executionType"`
	BidAmount       *int      `json:"bidAmount,omitempty"`
	ExpirationDate  *int64    `json:"expirationDate,omitempty"`
	Comment         string    `json:"comment,omitempty"`
	Items           []TxnItem `json:"items"`
}

func intp(v int) *int { return &v }

// LineupMove builds a lineup item moving a player between slots.
func LineupMove(playerID, from, to int) TxnItem {
	return TxnItem{PlayerID: playerID, Type: ItemLineup, FromLineupSlotID: intp(from), ToLineupSlotID: intp(to)}
}

// AddItem builds an add item for a player joining teamID.
func AddItem(playerID, teamID int) TxnItem {
	return TxnItem{PlayerID: playerID, Type: ItemAdd, ToTeamID: intp(teamID)}
}

// DropItem builds a drop item for a player leaving teamID.
func DropItem(playerID, teamID int) TxnItem {
	return TxnItem{PlayerID: playerID, Type: ItemDrop, FromTeamID: intp(teamID)}
}

// TradeItem builds a trade item moving a player between two teams.
func TradeItem(playerID, fromTeam, toTeam int) TxnItem {
	return TxnItem{PlayerID: playerID, Type: ItemTrade, FromTeamID: intp(fromTeam), ToTeamID: intp(toTeam)}
}

// NewTransaction returns a transaction submitted by memberID (a SWID).
func NewTransaction(memberID, kind string, teamID, week int, items ...TxnItem) *Transaction {
	return &Transaction{
		TeamID:          teamID,
		Type:            kind,
		MemberID:        memberID,
		ScoringPeriodID: week,
		ExecutionType:   "EXECUTE",
		Items:           items,
	}
}

// NewWaiverClaim returns a waiver claim, with a FAAB bid when bid >= 0.
func NewWaiverClaim(memberID string, teamID, week, bid int, items ...TxnItem) *Transaction {
	t := NewTransaction(memberID, TxnWaiver, teamID, week, items...)
	if bid >= 0 {
		t.BidAmount = intp(bid)
	}
	return t
}

// NewTradeProposal returns a trade offer that expires at the given time.
func NewTradeProposal(memberID string, teamID, week int, expires time.Time, items ...TxnItem) *Transaction {
	t := NewTransaction(memberID, TxnTrade, teamID, week, items...)
	exp := expires.UnixMilli()
	t.ExpirationDate = &exp
	return t
}

// TransactionResult is the subset of ESPN's response we care about.
type TransactionResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Type   string `json:"type"`
}

// Submit sends a transaction to ESPN. It requires espn_s2 and SWID.
func (c *Client) Submit(ctx context.Context, t *Transaction) (*TransactionResult, error) {
	if c.ESPNS2 == "" || c.SWID == "" {
		return nil, fmt.Errorf("espn: ESPN_S2 and SWID are required to make changes")
	}
	var res TransactionResult
	u := c.leaguePath(c.WriteBase) + "/transactions/"
	if err := c.do(ctx, http.MethodPost, u, t, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
