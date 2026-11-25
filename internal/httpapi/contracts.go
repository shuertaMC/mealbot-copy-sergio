package httpapi

import (
	"context"
	"io"

	"github.com/johnamadeo/mealbot/internal/store"
)

// OrganizationStore is the consumer-owned persistence surface needed by the
// organization routes.
type OrganizationStore interface {
	OrganizationsByAdmin(context.Context, string) ([]string, error)
	CreateOrganization(context.Context, string, string) error
	CrossMatchTrait(context.Context, string) (*string, error)
	SetCrossMatchTrait(context.Context, string, *string) error
}

// MemberStore is the consumer-owned persistence surface needed by member
// retrieval and CSV synchronization routes.
type MemberStore interface {
	Members(context.Context, string, bool) ([]store.Member, error)
	SynchronizeMembersCSV(context.Context, string, io.Reader) (store.MemberSyncPlan, error)
}

// RoundStore is the consumer-owned persistence surface needed by round routes.
type RoundStore interface {
	ListRounds(context.Context, string) ([]string, error)
	AddRound(context.Context, string, string) (int, error)
	RescheduleRound(context.Context, string, string, int) error
	DeleteRound(context.Context, string, int) error
}

// PairStore is the consumer-owned persistence surface needed by pair history.
type PairStore interface {
	PairHistory(context.Context, string) ([][]store.HistoricalPair, error)
}

// Dependencies groups the narrow route-owned contracts without forcing later
// handlers to depend on the concrete PostgreSQL adapter.
type Dependencies struct {
	Organizations OrganizationStore
	Members       MemberStore
	Rounds        RoundStore
	Pairs         PairStore
}

var (
	_ OrganizationStore = (*store.Store)(nil)
	_ MemberStore       = (*store.Store)(nil)
	_ RoundStore        = (*store.Store)(nil)
	_ PairStore         = (*store.Store)(nil)
)
