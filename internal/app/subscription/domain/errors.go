package domain

import "errors"

var (
	ErrInvalidCustomer      = errors.New("invalid customer")
	ErrAlreadyCancelled     = errors.New("subscription already cancelled")
	ErrSubscriptionNotFound = errors.New("subscription not found")
	ErrInvalidPrice         = errors.New("price must be positive")
	ErrInvalidPlanID        = errors.New("plan ID cannot be empty")
	ErrInvalidCustomerID    = errors.New("customer ID cannot be empty")
)
