package domain

import (
	"time"
)

// SubscriptionStatus represents the status of a subscription
type SubscriptionStatus string

const (
	StatusActive    SubscriptionStatus = "ACTIVE"
	StatusCancelled SubscriptionStatus = "CANCELLED"
)

// Subscription is the aggregate root for subscription management
type Subscription struct {
	id         string
	customerID string
	planID     string
	price      int64 // cents
	status     SubscriptionStatus
	startDate  time.Time
}

// NewSubscription creates a new subscription aggregate
func NewSubscription(id, customerID, planID string, priceCents int64, clock Clock) (*Subscription, *SubscriptionCreatedEvent, error) {
	if customerID == "" {
		return nil, nil, ErrInvalidCustomerID
	}
	if planID == "" {
		return nil, nil, ErrInvalidPlanID
	}
	if priceCents <= 0 {
		return nil, nil, ErrInvalidPrice
	}

	now := clock.Now()
	sub := &Subscription{
		id:         id,
		customerID: customerID,
		planID:     planID,
		price:      priceCents,
		status:     StatusActive,
		startDate:  now,
	}

	event := &SubscriptionCreatedEvent{
		SubscriptionID: id,
		CustomerID:     customerID,
		PlanID:         planID,
		Price:          priceCents,
		CreatedAt:      now,
	}

	return sub, event, nil
}

// Cancel cancels the subscription and calculates refund
func (s *Subscription) Cancel(clock Clock, billingCycleDays int64) (*SubscriptionCancelledEvent, error) {
	if s.status == StatusCancelled {
		return nil, ErrAlreadyCancelled
	}

	now := clock.Now()
	daysElapsed := int64(now.Sub(s.startDate).Hours() / 24)

	if daysElapsed >= billingCycleDays {
		// No refund if full cycle used
		daysElapsed = billingCycleDays
	}

	refundCents := (s.price * (billingCycleDays - daysElapsed)) / billingCycleDays
	if refundCents < 0 {
		refundCents = 0
	}

	s.status = StatusCancelled

	event := &SubscriptionCancelledEvent{
		SubscriptionID: s.id,
		CustomerID:     s.customerID,
		RefundAmount:   refundCents,
		CancelledAt:    now,
	}

	return event, nil
}

// ReconstructFromPersistence recreates a subscription from database
func ReconstructFromPersistence(id, customerID, planID string, priceCents int64, status SubscriptionStatus, startDate time.Time) *Subscription {
	return &Subscription{
		id:         id,
		customerID: customerID,
		planID:     planID,
		price:      priceCents,
		status:     status,
		startDate:  startDate,
	}
}

// Getters (no setters!)
func (s *Subscription) ID() string {
	return s.id
}

func (s *Subscription) CustomerID() string {
	return s.customerID
}

func (s *Subscription) PlanID() string {
	return s.planID
}

func (s *Subscription) Price() int64 {
	return s.price
}

func (s *Subscription) Status() SubscriptionStatus {
	return s.status
}

func (s *Subscription) StartDate() time.Time {
	return s.startDate
}
