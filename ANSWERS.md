# Answers to Architectural Questions

## Q1: Where should the refund API call happen?

**Answer: Option D - As separate usecase triggered by SubscriptionCancelledEvent**

**Reasoning:**

**Option A - Inside Cancel usecase:**
- ✅ Simple and straightforward
- ❌ Mixes orchestration with external calls
- ❌ Hard to test refund logic separately
- ❌ Synchronous coupling to external service
- ❌ If refund service is down, cancellation fails

**Option B - Inside domain Cancel() method:**
- ❌ Violates domain purity (infrastructure dependency)
- ❌ Domain would need to know about HTTP
- ❌ Cannot unit test domain without mocks
- ❌ Breaks clean architecture principles
- ❌ Domain becomes coupled to external services

**Option C - In service layer after committer.Apply() succeeds:**
- ✅ Good separation of concerns
- ✅ Domain remains pure
- ✅ Can test domain independently
- ❌ Still synchronous
- ❌ If refund fails, subscription is already cancelled (eventual consistency issue)
- ❌ No retry mechanism built-in
- ❌ Blocks cancellation if refund service is down

**Option D - As separate usecase triggered by SubscriptionCancelledEvent:**
- ✅ Best separation of concerns
- ✅ Domain emits event, doesn't know about refund
- ✅ Can implement retry logic independently
- ✅ Eventual consistency handled properly
- ✅ Can use outbox pattern for reliability
- ✅ Subscription cancellation succeeds even if refund service is down
- ✅ Can scale refund processing independently
- ✅ Better observability (separate metrics/logs)
- ❌ More complex architecture
- ❌ Requires event bus/handler infrastructure

**Recommendation:** For production systems, Option D is best. It provides resilience, proper separation, and allows for retry logic. For simpler systems or MVP, Option C is acceptable but should be documented as a technical debt.

**Implementation approach for Option D:**
1. Domain `Cancel()` method returns `SubscriptionCancelledEvent`
2. Usecase saves subscription and publishes event to event bus
3. Separate `ProcessRefundUsecase` listens to `SubscriptionCancelledEvent`
4. Refund usecase handles retries, dead letter queue, etc.
5. Use outbox pattern to ensure event delivery

**Trade-offs:**
- **Option C (current implementation):** Simpler, synchronous, but less resilient
- **Option D (recommended):** More complex, asynchronous, but production-ready

---

## Q2: If Cancel() works but the refund API is down

**What should happen to the subscription status?**
The subscription status should remain **CANCELLED**. The domain state is correct - the subscription has been cancelled. The refund is a separate concern (compensation/compensation transaction). The domain invariant (subscription is cancelled) is already satisfied.

**Should we retry? When? How many times?**
Yes, we should retry with exponential backoff:
- Initial retry: 1 second
- Second retry: 2 seconds  
- Third retry: 4 seconds
- Max 3 attempts total

**Retry strategy:**
- Use exponential backoff to avoid overwhelming the service
- Retry only on transient errors (5xx, timeouts, network errors)
- Don't retry on 4xx errors (client errors like invalid amount)
- Use jitter to prevent thundering herd

**What if refund fails after 3 retries?**
1. Log the error with full context (subscription ID, amount, customer ID, timestamp)
2. Send to dead letter queue (DLQ) for manual review
3. Alert operations team via monitoring system
4. Store refund as "pending" in a separate `pending_refunds` table for reconciliation
5. Implement manual retry mechanism for operations team
6. Consider compensating transaction (mark subscription as "refund_pending" status)

**Implementation strategy:**
- Use outbox pattern: Store refund request in `outbox` table when subscription is cancelled
- Background worker processes outbox with retry logic
- After max retries, move to DLQ table
- Operations can manually retry from DLQ via admin interface
- Monitor DLQ size and alert if it grows

**Alternative approach (if using Option D from Q1):**
- Event handler for `SubscriptionCancelledEvent` processes refund
- If refund fails, emit `RefundFailedEvent` to DLQ
- Separate process handles DLQ events
- Can implement circuit breaker pattern to stop retrying if service is consistently down

---

## Q3: Time and money calculation issues

**Problem 1: Why is `time.Since()` wrong here?**

1. **Non-deterministic for tests:** Cannot test with specific dates. Tests depend on current time, making them flaky.
2. **Timezone issues:** Uses server timezone, not customer/subscription timezone. Can cause incorrect day calculations.
3. **Clock skew:** If server clock changes (NTP sync, manual adjustment), calculations are wrong.
4. **No abstraction:** Cannot inject test doubles, violates dependency inversion.
5. **Race conditions:** In distributed systems, different servers might have slightly different times.

**Problem 2: Why is float math dangerous?**

1. **Precision loss:** `0.1 + 0.2 != 0.3` in floating point arithmetic. Example: `0.1 + 0.2 = 0.30000000000000004`.
2. **Rounding errors:** Accumulate over multiple operations. `(0.1 + 0.2) * 3 != 0.9`.
3. **Cannot represent exact amounts:** Some decimal values have no exact float representation (e.g., 0.1 in binary).
4. **Financial regulations:** Many jurisdictions require exact currency calculations. Float errors can cause compliance issues.
5. **Comparison issues:** `==` comparisons fail due to precision: `0.1 + 0.2 == 0.3` is `false`.

**Correct implementation:**

```go
// In domain/subscription.go
func (s *Subscription) Cancel(clock Clock, billingCycleDays int64) (*SubscriptionCancelledEvent, error) {
    if s.status == StatusCancelled {
        return nil, ErrAlreadyCancelled
    }

    now := clock.Now() // Use injected clock
    duration := now.Sub(s.startDate)
    daysElapsed := int64(duration.Hours() / 24)
    
    if daysElapsed >= billingCycleDays {
        daysElapsed = billingCycleDays
    }

    // All calculations in cents (int64)
    refundCents := (s.price * (billingCycleDays - daysElapsed)) / billingCycleDays
    if refundCents < 0 {
        refundCents = 0
    }

    s.status = StatusCancelled

    return &SubscriptionCancelledEvent{
        SubscriptionID: s.id,
        CustomerID:     s.customerID,
        RefundAmount:   refundCents, // int64 cents
        CancelledAt:    now,
    }, nil
}
```

**Key changes:**
- `clock.Now()` instead of `time.Now()` - allows injection of test clock
- `clock.Now().Sub(s.startDate)` instead of `time.Since()` - uses injected clock
- All calculations use `int64` cents - no floating point
- `s.price` is already in cents (int64)
- Division is integer division, which is exact for money calculations

**Example:**
- Price: $30.00 = 3000 cents
- Days elapsed: 14
- Billing cycle: 30 days
- Refund: `(3000 * (30 - 14)) / 30 = (3000 * 16) / 30 = 48000 / 30 = 1600 cents = $16.00`

---

## Q4: Test design for CancelSubscription

**Test structure:**

```go
package cancel_subscription

import (
    "context"
    "testing"
    "time"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/mock"
    "github.com/wuyiadepoju/subscription-management/internal/app/subscription/domain"
)

// Mock implementations
type MockRepository struct {
    mock.Mock
}

func (m *MockRepository) Save(ctx context.Context, sub *domain.Subscription) error {
    args := m.Called(ctx, sub)
    return args.Error(0)
}

func (m *MockRepository) FindByID(ctx context.Context, id string) (*domain.Subscription, error) {
    args := m.Called(ctx, id)
    if args.Get(0) == nil {
        return nil, args.Error(1)
    }
    return args.Get(0).(*domain.Subscription), args.Error(1)
}

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
    mockRepo.On("Save", ctx, mock.MatchedBy(func(s *domain.Subscription) bool {
        return s.ID() == "sub-123" && s.Status() == domain.StatusCancelled
    })).Return(nil)
    
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
    // Refund should NOT be called
    
    // Execute
    event, err := interactor.Execute(ctx, "sub-123")
    
    // Assert
    assert.Error(t, err)
    assert.Equal(t, domain.ErrAlreadyCancelled, err)
    assert.Nil(t, event)
    mockRepo.AssertNotCalled(t, "Save", ctx, mock.Anything)
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
        {
            name:           "edge case: zero days",
            priceCents:     3000,
            daysElapsed:    0,
            billingDays:    30,
            expectedRefund: 3000, // Full refund
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
            mockRepo.On("Save", ctx, mock.Anything).Return(nil)
            mockBilling.On("ProcessRefund", ctx, tc.expectedRefund).Return(nil)
            
            event, err := interactor.Execute(ctx, "sub-123")
            
            assert.NoError(t, err)
            assert.Equal(t, tc.expectedRefund, event.RefundAmount)
        })
    }
}

func TestCancelSubscription_OutboxEventCreated(t *testing.T) {
    // This test verifies the event is returned correctly
    // In a full implementation with outbox pattern, you would also verify
    // that the event is stored in the outbox table
    
    ctx := context.Background()
    startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
    cancelDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
    
    clock := domain.FixedClock{FixedTime: cancelDate}
    sub := domain.ReconstructFromPersistence(
        "sub-123", "cust-456", "plan-789", 3000, domain.StatusActive, startDate,
    )
    
    mockRepo := new(MockRepository)
    mockBilling := new(MockBillingClient)
    interactor := NewInteractor(mockRepo, mockBilling, clock, 30)
    
    mockRepo.On("FindByID", ctx, "sub-123").Return(sub, nil)
    mockRepo.On("Save", ctx, mock.Anything).Return(nil)
    mockBilling.On("ProcessRefund", ctx, int64(1600)).Return(nil)
    
    event, err := interactor.Execute(ctx, "sub-123")
    
    assert.NoError(t, err)
    assert.NotNil(t, event)
    assert.Equal(t, "sub-123", event.SubscriptionID)
    assert.Equal(t, "cust-456", event.CustomerID)
    assert.Equal(t, int64(1600), event.RefundAmount)
    assert.WithinDuration(t, cancelDate, event.CancelledAt, time.Second)
}
```

**What to mock:**
- **Repository:** Return test subscription, verify Save called with cancelled status
- **Billing client:** Verify ProcessRefund called with correct refund amount
- **Clock:** Use FixedClock for deterministic time

**What to assert:**
- Subscription status changed to CANCELLED
- Refund amount calculated correctly (multiple test cases)
- Event created with correct data (ID, customer ID, amount, timestamp)
- Repository Save called once with correct subscription
- Billing client ProcessRefund called with correct amount
- Error cases return appropriate domain errors
- Already cancelled subscription returns error
- Subscription not found returns error

**Additional test cases to consider:**
- Subscription not found error
- Repository save failure
- Refund API failure (should still return event but with error)
- Edge cases: zero price, negative days, etc.

---

## Q5: Business problem of ignored errors

**Beyond nil pointer crash, what BUSINESS problem does this cause?**

```go
resp, _ := s.client.Get("https://api.billing.com/validate/" + req.CustomerID)
```

**Business Problems:**

1. **Invalid customers get subscriptions:**
   - If API is down, error is ignored
   - `result.Valid` defaults to `false` (zero value)
   - But if JSON decode also fails (ignored), `result` struct might have random/zero values
   - Code checks `if !result.Valid` → should reject, BUT if decode failed, `result` might be in inconsistent state
   - Invalid/fraudulent customers can create subscriptions
   - Revenue loss from chargebacks
   - Compliance violations (KYC/AML requirements)
   - Legal liability for serving unauthorized customers

2. **Billing system inconsistency:**
   - Subscription created in our system but not validated in billing system
   - Two systems out of sync
   - Cannot bill customer properly
   - Revenue recognition issues
   - Accounting discrepancies
   - Audit trail incomplete

3. **Customer support nightmare:**
   - Customers get subscriptions they shouldn't have
   - Support tickets increase dramatically
   - Refund requests from confused customers
   - Reputation damage
   - Customer churn
   - Support team overwhelmed

4. **Financial/audit issues:**
   - Cannot prove customer was validated
   - Audit trail incomplete
   - Regulatory compliance failures (SOX, PCI-DSS, etc.)
   - Potential legal issues
   - Cannot demonstrate due diligence
   - Insurance/liability problems

5. **Data quality degradation:**
   - Invalid data enters system
   - Analytics/reporting becomes unreliable
   - Business decisions based on bad data
   - Customer segmentation wrong
   - Revenue forecasting inaccurate
   - Product decisions based on corrupted metrics

6. **Security vulnerabilities:**
   - Fraudulent accounts created
   - Account takeover attempts succeed
   - Unauthorized access to services
   - Data breaches from unvalidated accounts

**Example scenario:**
1. Billing API is down (maintenance, DDoS, network issue)
2. Error ignored, `resp` is `nil`
3. Code continues, `result.Valid` is `false` (zero value)
4. Code checks `if !result.Valid` → should reject
5. BUT if JSON decode also failed silently, `result` struct might have random values
6. Customer gets subscription anyway (race condition or logic error)
7. Later, billing system rejects the customer (they're on blacklist)
8. We have a subscription we can't bill
9. Revenue loss + support cost + compliance issue + potential legal problem

**Real-world impact:**
- **Financial:** Lost revenue, chargebacks, refunds
- **Operational:** Support costs, manual cleanup
- **Legal:** Compliance violations, potential lawsuits
- **Reputation:** Customer trust, brand damage
- **Technical:** Data corruption, system inconsistencies

**Prevention:**
- Always check errors
- Implement circuit breaker for external services
- Use outbox pattern for reliability
- Add monitoring/alerting for validation failures
- Implement fallback strategies (queue for later validation)
- Add idempotency checks
