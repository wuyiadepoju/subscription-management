package contracts

import "context"

// BillingClient defines the interface for external billing service interactions
type BillingClient interface {
	ValidateCustomer(ctx context.Context, customerID string) error
	ProcessRefund(ctx context.Context, amount int64) error
}
