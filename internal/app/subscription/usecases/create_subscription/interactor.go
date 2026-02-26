package create_subscription

import (
	"context"

	"github.com/google/uuid"
	"github.com/wuyiadepoju/subscription-management/internal/app/subscription/contracts"
	"github.com/wuyiadepoju/subscription-management/internal/app/subscription/domain"
)

// Request contains the input for creating a subscription
type Request struct {
	CustomerID string
	PlanID     string
	PriceCents int64
}

// Interactor handles the create subscription use case
type Interactor struct {
	repo          contracts.SubscriptionRepository
	billingClient contracts.BillingClient
	clock         domain.Clock
}

// NewInteractor creates a new create subscription interactor
func NewInteractor(repo contracts.SubscriptionRepository, billingClient contracts.BillingClient, clock domain.Clock) *Interactor {
	return &Interactor{
		repo:          repo,
		billingClient: billingClient,
		clock:         clock,
	}
}

// Execute creates a new subscription
func (i *Interactor) Execute(ctx context.Context, req Request) (*domain.Subscription, *domain.SubscriptionCreatedEvent, error) {
	// 1. Validate customer with external API
	if err := i.billingClient.ValidateCustomer(ctx, req.CustomerID); err != nil {
		return nil, nil, err
	}

	// 2. Create domain aggregate
	id := uuid.New().String()
	sub, event, err := domain.NewSubscription(id, req.CustomerID, req.PlanID, req.PriceCents, i.clock)
	if err != nil {
		return nil, nil, err
	}

	// 3. Get mutation for saving subscription
	mutation, err := i.repo.Save(ctx, sub)
	if err != nil {
		return nil, nil, err
	}

	// 4. Apply the mutation
	if err := i.repo.Apply(ctx, mutation); err != nil {
		return nil, nil, err
	}

	return sub, event, nil
}
