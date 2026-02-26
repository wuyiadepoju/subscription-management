# Answers to Architectural Questions

## Q1: Where should the refund API call happen?

**Answer: Separate usecase triggered by SubscriptionCancelledEvent**

**How it works:**
1. Domain `Cancel()` returns `SubscriptionCancelledEvent`
2. Usecase saves subscription and publishes event
3. Separate `ProcessRefundUsecase` handles refund with retry logic
4. Use outbox pattern for reliability

**Why:** Better separation of concerns, more resilient, allows retry logic, eventual consistency. Subscription cancellation succeeds even if refund service is down.

---

## Q2: If Cancel() works but the refund API is down

**Subscription status:** Stays **CANCELLED**. Domain invariant is satisfied; refund is separate compensation.

**Retry strategy:**
- Exponential backoff: 1s, 2s, 4s
- Max 3 attempts
- Retry only transient errors (5xx, timeouts)
- Don't retry 4xx errors

**After 3 retries fail:**
1. Log error with full context
2. Send to dead letter queue (DLQ)
3. Alert operations team
4. Store in `pending_refunds` table
5. Enable manual retry via admin interface

**Implementation:** Use outbox pattern - store refund request in outbox table, background worker processes with retry, move to DLQ after max retries.

---

## Q3: Time and money calculation issues

**Problem 1: `time.Since()` issues:**
- Non-deterministic for tests
- Timezone problems
- Clock skew issues
- No abstraction (violates dependency inversion)

**Problem 2: Float math issues:**
- Precision loss (`0.1 + 0.2 != 0.3`)
- Rounding errors accumulate
- Cannot represent exact amounts
- Financial compliance violations
- Comparison failures

**Correct implementation:**

```go
func (s *Subscription) Cancel(clock Clock, billingCycleDays int64) (*SubscriptionCancelledEvent, error) {
    if s.status == StatusCancelled {
        return nil, ErrAlreadyCancelled
    }

    now := clock.Now()
    daysElapsed := int64(now.Sub(s.startDate).Hours() / 24)
    if daysElapsed >= billingCycleDays {
        daysElapsed = billingCycleDays
    }

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
- Use `clock.Now()` instead of `time.Now()`
- Use `clock.Now().Sub()` instead of `time.Since()`
- All calculations in `int64` cents (no floats)

**Example:** $30.00 (3000 cents), 14 days elapsed, 30-day cycle → `(3000 * (30 - 14)) / 30 = 1600 cents = $16.00`

---

## Q4: Test design for CancelSubscription

**Test cases:**
1. **Success path:** Verify cancellation, refund calculation, event creation
2. **Already cancelled:** Return error, no save/refund calls
3. **Refund calculation:** Test multiple scenarios (half month, full month, one day, zero days)
4. **Outbox event:** Verify event returned with correct data

**What to mock:**
- Repository: Return subscription, verify Save called with cancelled status
- Billing client: Verify ProcessRefund called with correct amount
- Clock: Use FixedClock for deterministic time

**What to assert:**
- Status changed to CANCELLED
- Refund amount correct
- Event created with correct data
- Repository Save called once
- Billing client ProcessRefund called with correct amount
- Error cases return domain errors

**Example test structure:**

```go
func TestCancelSubscription_Success(t *testing.T) {
    clock := domain.FixedClock{FixedTime: cancelDate}
    sub := domain.ReconstructFromPersistence("sub-123", "cust-456", "plan-789", 3000, domain.StatusActive, startDate)
    
    mockRepo.On("FindByID", ctx, "sub-123").Return(sub, nil)
    mockRepo.On("Save", ctx, mock.MatchedBy(func(s *domain.Subscription) bool {
        return s.Status() == domain.StatusCancelled
    })).Return(mockMutation, nil)
    mockBilling.On("ProcessRefund", ctx, int64(1600)).Return(nil)
    
    event, err := interactor.Execute(ctx, "sub-123")
    
    assert.NoError(t, err)
    assert.Equal(t, int64(1600), event.RefundAmount)
}
```

---

## Q5: Business problem of ignored errors

```go
resp, _ := s.client.Get("https://api.billing.com/validate/" + req.CustomerID)
```

**What goes wrong:**

1. **Invalid customers get subscriptions:** API down → error ignored → invalid/fraudulent customers create subscriptions → revenue loss, chargebacks, compliance violations

2. **Billing system inconsistency:** Subscription created but not validated → systems out of sync → cannot bill properly → revenue recognition issues

3. **Customer support issues:** Customers get subscriptions they shouldn't → support tickets increase → reputation damage → customer churn

4. **Financial/audit issues:** Cannot prove validation → incomplete audit trail → regulatory compliance failures → legal issues

5. **Data quality degradation:** Invalid data enters system → analytics unreliable → bad business decisions

6. **Security vulnerabilities:** Fraudulent accounts created → unauthorized access → data breaches

**How to prevent:**
- Always check errors
- Circuit breaker for external services
- Outbox pattern for reliability
- Monitoring/alerting for validation failures
- Fallback strategies (queue for later validation)
- Idempotency checks
