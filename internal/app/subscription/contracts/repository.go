package contracts

import (
	"context"

	"cloud.google.com/go/spanner"
	"github.com/wuyiadepoju/subscription-management/internal/app/subscription/domain"
)

// SubscriptionRepository defines the interface for subscription persistence
type SubscriptionRepository interface {
	Save(ctx context.Context, sub *domain.Subscription) (*spanner.Mutation, error)
	FindByID(ctx context.Context, id string) (*domain.Subscription, error)
	Apply(ctx context.Context, mutations ...*spanner.Mutation) error
}
