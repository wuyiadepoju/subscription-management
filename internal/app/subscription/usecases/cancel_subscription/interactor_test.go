package cancel_subscription

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/wuyiadepoju/subscription-management/internal/app/subscription/domain"
)

// MockRepository is a mock implementation of SubscriptionRepository
type MockRepository struct {
	mock.Mock
}

func (m *MockRepository) Save(ctx context.Context, sub *domain.Subscription) (*spanner.Mutation, error) {
	args := m.Called(ctx, sub)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*spanner.Mutation), args.Error(1)
}

func (m *MockRepository) Apply(ctx context.Context, mutations ...*spanner.Mutation) error {
	// Convert variadic to slice for mock
	args := m.Called(ctx, mutations)
	return args.Error(0)
}

func (m *MockRepository) FindByID(ctx context.Context, id string) (*domain.Subscription, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Subscription), args.Error(1)
}

// MockBillingClient is a mock implementation of BillingClient
type MockBillingClient struct {
	mock.Mock
}

func (m *MockBillingClient) ValidateCustomer(ctx context.Context, customerID string) error {
	args := m.Called(ctx, customerID)
	return args.Error(0)
}

func (m *MockBillingClient) ProcessRefund(ctx context.Context, amount int64) error {
	args := m.Called(ctx, amount)
	return args.Error(0)
}

func TestCancelSubscription_Success(t *testing.T) {
	// Setup
	ctx := context.Background()
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cancelDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC) // 14 days later

	clock := domain.FixedClock{FixedTime: cancelDate}

	sub := domain.ReconstructFromPersistence(
		"sub-123",
		"cust-456",
		"plan-789",
		3000, // $30.00 in cents
		domain.StatusActive,
		startDate,
	)

	mockRepo := new(MockRepository)
	mockBilling := new(MockBillingClient)

	interactor := NewInteractor(mockRepo, mockBilling, clock, 30)

	// Expectations
	mockRepo.On("FindByID", ctx, "sub-123").Return(sub, nil)
	mockMutation := &spanner.Mutation{}
	mockRepo.On("Save", ctx, mock.MatchedBy(func(s *domain.Subscription) bool {
		return s.ID() == "sub-123" && s.Status() == domain.StatusCancelled
	})).Return(mockMutation, nil)
	// Apply accepts variadic mutations (becomes []*spanner.Mutation when called)
	mockRepo.On("Apply", ctx, mock.Anything).Return(nil)

	// Expected refund: 3000 * (30 - 14) / 30 = 3000 * 16 / 30 = 1600 cents
	mockBilling.On("ProcessRefund", ctx, int64(1600)).Return(nil)

	// Execute
	event, err := interactor.Execute(ctx, "sub-123")

	// Assert
	assert.NoError(t, err)
	assert.NotNil(t, event)
	assert.Equal(t, "sub-123", event.SubscriptionID)
	assert.Equal(t, int64(1600), event.RefundAmount)
	mockRepo.AssertExpectations(t)
	mockBilling.AssertExpectations(t)
}

func TestCancelSubscription_AlreadyCancelled(t *testing.T) {
	// Setup
	ctx := context.Background()
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	sub := domain.ReconstructFromPersistence(
		"sub-123",
		"cust-456",
		"plan-789",
		3000,
		domain.StatusCancelled, // Already cancelled
		startDate,
	)

	mockRepo := new(MockRepository)
	mockBilling := new(MockBillingClient)
	clock := domain.FixedClock{FixedTime: time.Now()}

	interactor := NewInteractor(mockRepo, mockBilling, clock, 30)

	// Expectations
	mockRepo.On("FindByID", ctx, "sub-123").Return(sub, nil)
	// Save should NOT be called
	// Apply should NOT be called
	// Refund should NOT be called

	// Execute
	event, err := interactor.Execute(ctx, "sub-123")

	// Assert
	assert.Error(t, err)
	assert.Equal(t, domain.ErrAlreadyCancelled, err)
	assert.Nil(t, event)
	mockRepo.AssertNotCalled(t, "Save", ctx, mock.Anything)
	mockRepo.AssertNotCalled(t, "Apply", ctx, mock.Anything)
	mockBilling.AssertNotCalled(t, "ProcessRefund", ctx, mock.Anything)
}

func TestCancelSubscription_RefundCalculationCorrectness(t *testing.T) {
	testCases := []struct {
		name           string
		priceCents     int64
		daysElapsed    int
		billingDays    int64
		expectedRefund int64
	}{
		{
			name:           "half month used",
			priceCents:     3000,
			daysElapsed:    15,
			billingDays:    30,
			expectedRefund: 1500, // 3000 * (30-15) / 30
		},
		{
			name:           "full month used",
			priceCents:     3000,
			daysElapsed:    30,
			billingDays:    30,
			expectedRefund: 0, // No refund
		},
		{
			name:           "one day used",
			priceCents:     3000,
			daysElapsed:    1,
			billingDays:    30,
			expectedRefund: 2900, // 3000 * (30-1) / 30 = 2900
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			cancelDate := startDate.AddDate(0, 0, tc.daysElapsed)

			clock := domain.FixedClock{FixedTime: cancelDate}

			sub := domain.ReconstructFromPersistence(
				"sub-123",
				"cust-456",
				"plan-789",
				tc.priceCents,
				domain.StatusActive,
				startDate,
			)

			mockRepo := new(MockRepository)
			mockBilling := new(MockBillingClient)

			interactor := NewInteractor(mockRepo, mockBilling, clock, tc.billingDays)

			mockRepo.On("FindByID", ctx, "sub-123").Return(sub, nil)
			mockMutation := &spanner.Mutation{}
			mockRepo.On("Save", ctx, mock.Anything).Return(mockMutation, nil)
			// Apply accepts variadic mutations (becomes []*spanner.Mutation when called)
			mockRepo.On("Apply", ctx, mock.Anything).Return(nil)
			mockBilling.On("ProcessRefund", ctx, tc.expectedRefund).Return(nil)

			event, err := interactor.Execute(ctx, "sub-123")

			assert.NoError(t, err)
			assert.Equal(t, tc.expectedRefund, event.RefundAmount)
		})
	}
}
