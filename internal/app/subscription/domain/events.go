package domain

import "time"

// SubscriptionCreatedEvent is emitted when a subscription is created
type SubscriptionCreatedEvent struct {
	SubscriptionID string
	CustomerID     string
	PlanID         string
	Price          int64 // cents
	CreatedAt      time.Time
}

// SubscriptionCancelledEvent is emitted when a subscription is cancelled
type SubscriptionCancelledEvent struct {
	SubscriptionID string
	CustomerID     string
	RefundAmount   int64 // cents
	CancelledAt    time.Time
}
