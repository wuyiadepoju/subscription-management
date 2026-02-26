package repo

import (
	"context"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/wuyiadepoju/subscription-management/internal/app/subscription/contracts"
	"github.com/wuyiadepoju/subscription-management/internal/app/subscription/domain"
	"google.golang.org/api/iterator"
)

var _ contracts.SubscriptionRepository = (*SubscriptionRepo)(nil)

// SubscriptionRepo implements the subscription repository interface using Cloud Spanner
type SubscriptionRepo struct {
	client *spanner.Client
}

// NewSubscriptionRepo creates a new subscription repository
func NewSubscriptionRepo(client *spanner.Client) *SubscriptionRepo {
	return &SubscriptionRepo{client: client}
}

// Save returns a mutation for persisting a subscription to the database
// The mutation must be applied using Apply() method
func (r *SubscriptionRepo) Save(ctx context.Context, sub *domain.Subscription) (*spanner.Mutation, error) {
	mutation := spanner.InsertOrUpdate("subscriptions",
		[]string{"id", "customer_id", "plan_id", "price_cents", "status", "start_date"},
		[]interface{}{
			sub.ID(),
			sub.CustomerID(),
			sub.PlanID(),
			sub.Price(),
			string(sub.Status()),
			sub.StartDate(),
		})

	return mutation, nil
}

// Apply applies the given mutations to the database
func (r *SubscriptionRepo) Apply(ctx context.Context, mutations ...*spanner.Mutation) error {
	_, err := r.client.Apply(ctx, mutations)
	return err
}

// FindByID retrieves a subscription by ID
func (r *SubscriptionRepo) FindByID(ctx context.Context, id string) (*domain.Subscription, error) {
	stmt := spanner.Statement{
		SQL: `
			SELECT id, customer_id, plan_id, price_cents, status, start_date
			FROM subscriptions
			WHERE id = @id
		`,
		Params: map[string]interface{}{
			"id": id,
		},
	}

	iter := r.client.Single().Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err != nil {
		if err == iterator.Done {
			return nil, domain.ErrSubscriptionNotFound
		}
		return nil, err
	}

	var (
		dbID       string
		customerID string
		planID     string
		priceCents int64
		status     string
		startDate  time.Time
	)

	if err := row.Columns(&dbID, &customerID, &planID, &priceCents, &status, &startDate); err != nil {
		return nil, err
	}

	sub := domain.ReconstructFromPersistence(
		dbID,
		customerID,
		planID,
		priceCents,
		domain.SubscriptionStatus(status),
		startDate,
	)

	return sub, nil
}
