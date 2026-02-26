package cancel_subscription

import (
	"context"

	"github.com/wuyiadepoju/subscription-management/internal/app/subscription/contracts"
	"github.com/wuyiadepoju/subscription-management/internal/app/subscription/domain"
)

// Interactor handles the cancel subscription use case
type Interactor struct {
	repo             contracts.SubscriptionRepository
	billingClient    contracts.BillingClient
	clock            domain.Clock
	billingCycleDays int64 // Could be from plan, but keeping simple
}

// NewInteractor creates a new cancel subscription interactor
func NewInteractor(repo contracts.SubscriptionRepository, billingClient contracts.BillingClient, clock domain.Clock, billingCycleDays int64) *Interactor {
	return &Interactor{
		repo:             repo,
		billingClient:    billingClient,
		clock:            clock,
		billingCycleDays: billingCycleDays,
	}
}

// Execute cancels a subscription
func (i *Interactor) Execute(ctx context.Context, subscriptionID string) (*domain.SubscriptionCancelledEvent, error) {
	// 1. Load subscription
	sub, err := i.repo.FindByID(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}

	// 2. Cancel via domain method (returns event)
	event, err := sub.Cancel(i.clock, i.billingCycleDays)
	if err != nil {
		return nil, err
	}

	// 3. Get mutation for saving updated subscription
	mutation, err := i.repo.Save(ctx, sub)
	if err != nil {
		return nil, err
	}

	// 4. Apply the mutation
	if err := i.repo.Apply(ctx, mutation); err != nil {
		return nil, err
	}

	// 5. Process refund (after successful save)
	// Note: See ANSWERS.md Q1 for discussion on where this should be
	if event.RefundAmount > 0 {
		if err := i.billingClient.ProcessRefund(ctx, event.RefundAmount); err != nil {
			// Log error but don't fail - subscription is already cancelled
			// See ANSWERS.md Q2 for handling strategy
			return event, err // Return event but also error for caller to handle
		}
	}

	return event, nil
}
