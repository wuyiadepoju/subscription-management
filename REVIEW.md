# Code Review: Subscription Service Issues

This document identifies all issues found in the broken implementation.

## Layer Violations

### Issue 1: Direct HTTP Client in Service Struct
- **Category:** Layer Violation / Testability
- **Severity:** CRITICAL
- **Location:** `type SubscriptionService struct { client *http.Client }`
- **Problem:** Service depends on concrete `*http.Client`, making it impossible to mock for tests. This violates dependency inversion principle.
- **Why it matters:** Cannot unit test without making real HTTP calls. Service is tightly coupled to HTTP implementation.
- **Fix:** Use interface (BillingClient) instead of concrete type.

### Issue 2: Direct SQL.DB in Service Struct
- **Category:** Layer Violation / Testability
- **Severity:** CRITICAL
- **Location:** `type SubscriptionService struct { db *sql.DB }`
- **Problem:** Service depends on concrete `*sql.DB`, violating repository pattern and dependency inversion.
- **Why it matters:** Cannot test without database. Service directly handles persistence concerns.
- **Fix:** Use SubscriptionRepository interface instead of direct database access.

### Issue 3: HTTP Call in Service Method
- **Category:** Layer Violation
- **Severity:** CRITICAL
- **Location:** `CreateSubscription` method - `s.client.Get("https://api.billing.com/validate/" + req.CustomerID)`
- **Problem:** Service layer directly calls external HTTP API, mixing infrastructure concerns with application logic.
- **Why it matters:** Violates clean architecture. Service should depend on abstractions, not concrete HTTP implementations.
- **Fix:** Move HTTP calls to adapter layer (BillingClient adapter).

### Issue 4: SQL Query in Service Method
- **Category:** Layer Violation
- **Severity:** CRITICAL
- **Location:** `CreateSubscription` method - `s.db.ExecContext(ctx, "INSERT INTO subscriptions VALUES (?, ?, ?, ?, ?, ?)", ...)`
- **Problem:** Service directly executes SQL queries instead of using repository pattern.
- **Why it matters:** Violates separation of concerns. Persistence logic should be in repository layer.
- **Fix:** Use SubscriptionRepository interface to abstract database operations.

### Issue 5: Business Logic in Service (Refund Calculation)
- **Category:** Layer Violation / Domain Purity
- **Severity:** CRITICAL
- **Location:** `CancelSubscription` method - refund calculation logic
- **Problem:** Business rule (refund calculation) is implemented in service layer instead of domain aggregate.
- **Why it matters:** Violates DDD principles. Business logic should be in domain layer, not application layer.
- **Fix:** Move refund calculation to domain `Cancel()` method.

## Domain Purity Issues

### Issue 6: Public Fields Exposing Internal State
- **Category:** Domain Purity
- **Severity:** CRITICAL
- **Location:** `type Subscription struct` - all fields are public
- **Problem:** All fields are public (ID, CustomerID, PlanID, Price, Status, StartDate), providing no encapsulation.
- **Why it matters:** Can be mutated from anywhere, violates encapsulation, no invariants enforced. Any code can directly modify subscription state.
- **Fix:** Make all fields private and provide getters only (no setters).

### Issue 7: Anemic Domain Model
- **Category:** Domain Purity
- **Severity:** CRITICAL
- **Location:** Entire `Subscription` struct
- **Problem:** No domain methods, just a data structure. All behavior is in service layer.
- **Why it matters:** Business logic scattered in service, violates DDD aggregate pattern. Domain model is just a data container.
- **Fix:** Add domain methods like `Cancel()`, `Activate()` that contain business logic and return events.

### Issue 8: No Domain Events
- **Category:** Domain Purity
- **Severity:** WARNING
- **Location:** Missing throughout codebase
- **Problem:** State changes don't emit events. No way to track what happened or trigger side effects.
- **Why it matters:** Cannot implement event-driven patterns, no audit trail, cannot trigger downstream processes.
- **Fix:** Domain methods should return events (SubscriptionCreatedEvent, SubscriptionCancelledEvent).

### Issue 9: Status as String Type
- **Category:** Domain Purity
- **Severity:** WARNING
- **Location:** `type Subscription struct { Status string }`
- **Problem:** Status can be any string value, no type safety.
- **Why it matters:** Typos possible ("ACTIVE" vs "Active" vs "active"), no compile-time checking.
- **Fix:** Use typed constant: `type SubscriptionStatus string` with constants.

### Issue 10: Magic Number 30
- **Category:** Domain Purity
- **Severity:** WARNING
- **Location:** `CancelSubscription` method - `refundAmount := sub.Price * (30 - daysUsed) / 30`
- **Problem:** Assumes 30-day billing cycle, hard-coded in business logic.
- **Why it matters:** Different plans have different cycles, not configurable, violates single responsibility.
- **Fix:** Pass billing cycle days as parameter or get from plan configuration.

## Error Handling Issues

### Issue 11: Ignored HTTP Error
- **Category:** Error Handling
- **Severity:** CRITICAL
- **Location:** `CreateSubscription` method - `resp, _ := s.client.Get(...)`
- **Problem:** HTTP request error completely ignored using blank identifier.
- **Why it matters:** Network failures, timeouts, invalid responses all silently fail. Can cause nil pointer panic when accessing `resp.Body`.
- **Fix:** Check and handle error: `resp, err := s.client.Get(...); if err != nil { return nil, err }`

### Issue 12: Ignored JSON Decode Error
- **Category:** Error Handling
- **Severity:** CRITICAL
- **Location:** `CreateSubscription` method - `json.NewDecoder(resp.Body).Decode(&result)`
- **Problem:** JSON decode error ignored, no validation of response format.
- **Why it matters:** Invalid JSON responses cause silent failures, wrong validation results, `result.Valid` might have zero value.
- **Fix:** Check decode error: `if err := json.NewDecoder(resp.Body).Decode(&result); err != nil { return nil, err }`

### Issue 13: No HTTP Status Code Check
- **Category:** Error Handling
- **Severity:** WARNING
- **Location:** `CreateSubscription` method - after HTTP call
- **Problem:** Doesn't check if `resp.StatusCode == 200` before decoding response.
- **Why it matters:** 404, 500, etc. are treated as success, wrong behavior for error responses.
- **Fix:** Check status code: `if resp.StatusCode != http.StatusOK { return nil, errors.New("validation failed") }`

### Issue 14: Ignored Refund API Error
- **Category:** Error Handling
- **Severity:** CRITICAL
- **Location:** `CancelSubscription` method - `s.client.Post("https://api.billing.com/refund", ...)`
- **Problem:** Refund API call error ignored, no handling of failure.
- **Why it matters:** Refund can fail silently, money lost, customer charged incorrectly, no retry mechanism.
- **Fix:** Check and handle error, implement retry logic or outbox pattern.

### Issue 15: Row.Scan Without Error Check
- **Category:** Error Handling
- **Severity:** CRITICAL
- **Location:** `CancelSubscription` method - `row.Scan(&sub.ID, ...)`
- **Problem:** Scan error ignored, no validation of database query result.
- **Why it matters:** Wrong number of columns, type mismatches silently fail, subscription might have zero values.
- **Fix:** Check scan error: `if err := row.Scan(...); err != nil { return err }`

### Issue 16: No Response Body Close Check
- **Category:** Error Handling
- **Severity:** WARNING
- **Location:** `CreateSubscription` method - `defer resp.Body.Close()`
- **Problem:** If `resp` is nil (from ignored error), defer on nil causes runtime panic.
- **Why it matters:** When HTTP call fails and error is ignored, `resp` is nil, causing panic.
- **Fix:** Check error before deferring, or check if resp is nil before closing.

### Issue 17: No Context Propagation for HTTP
- **Category:** Error Handling / Best Practice
- **Severity:** WARNING
- **Location:** `CreateSubscription` and `CancelSubscription` methods
- **Problem:** HTTP calls don't use context for cancellation/timeout (`s.client.Get(...)` instead of `s.client.GetWithContext(ctx, ...)`).
- **Why it matters:** Cannot cancel long-running requests, no timeout control, cannot propagate cancellation.
- **Fix:** Use `http.NewRequestWithContext(ctx, ...)` for all HTTP calls.

### Issue 18: No Validation of Input
- **Category:** Domain Purity / Error Handling
- **Severity:** WARNING
- **Location:** `CreateSubscription` method - no validation of request
- **Problem:** No validation of `req.CustomerID`, `req.PlanID`, `req.Price` before processing.
- **Why it matters:** Invalid data can enter system, null/empty values cause issues downstream.
- **Fix:** Add input validation in use case or domain constructor.

## Money Handling Issues

### Issue 19: Float64 for Money
- **Category:** Money Handling
- **Severity:** CRITICAL
- **Location:** `type Subscription struct { Price float64 }`
- **Problem:** Using float64 for currency representation.
- **Why it matters:** Precision errors, rounding issues, cannot represent exact amounts (e.g., 0.1 + 0.2 != 0.3 in floating point).
- **Fix:** Use `int64` for price in cents: `Price int64 // cents`

### Issue 20: Float Arithmetic in Refund
- **Category:** Money Handling
- **Severity:** CRITICAL
- **Location:** `CancelSubscription` method - `refundAmount := sub.Price * (30 - daysUsed) / 30`
- **Problem:** Floating point arithmetic for money calculation.
- **Why it matters:** Precision loss, rounding errors, incorrect refund amounts, financial regulations require exact calculations.
- **Fix:** Use int64 cents: `refundCents := (priceCents * (billingCycleDays - daysElapsed)) / billingCycleDays`

## Testability Issues

### Issue 21: Hard-coded API URLs
- **Category:** Testability
- **Severity:** WARNING
- **Location:** Multiple places - `"https://api.billing.com/validate/"`, `"https://api.billing.com/refund"`
- **Problem:** URLs hard-coded in business logic, not configurable.
- **Why it matters:** Cannot test with mock servers, environment-specific config impossible, hard to test different environments.
- **Fix:** Inject base URL as configuration parameter.

### Issue 22: time.Now() Without Abstraction
- **Category:** Testability
- **Severity:** WARNING
- **Location:** `CreateSubscription` method - `StartDate: time.Now()`
- **Problem:** Direct use of `time.Now()` makes time-dependent logic non-deterministic.
- **Why it matters:** Cannot test with specific dates, tests are flaky, cannot verify time-based logic.
- **Fix:** Inject Clock interface: `clock.Now()` instead of `time.Now()`.

### Issue 23: time.Since() Without Abstraction
- **Category:** Testability
- **Severity:** WARNING
- **Location:** `CancelSubscription` method - `daysUsed := time.Since(sub.StartDate).Hours() / 24`
- **Problem:** Direct use of `time.Since()` makes refund calculation non-deterministic.
- **Why it matters:** Cannot test refund calculation with specific dates, tests depend on current time.
- **Fix:** Use injected clock: `clock.Now().Sub(sub.StartDate)` instead of `time.Since()`.

### Issue 24: Missing Imports
- **Category:** Code Quality
- **Severity:** WARNING
- **Location:** Multiple places
- **Problem:** Code references `uuid`, `errors`, `bytes` but imports are missing. Code won't compile.
- **Why it matters:** Code is broken, won't compile.
- **Fix:** Add missing imports: `import "github.com/google/uuid"`, `import "errors"`, `import "bytes"`.

## Summary

**Total Issues Found: 24**

- **Layer Violations:** 5 (Issues 1-5)
- **Domain Purity Issues:** 5 (Issues 6-10)
- **Error Handling Issues:** 8 (Issues 11-18)
- **Money Handling Issues:** 2 (Issues 19-20)
- **Testability Issues:** 4 (Issues 21-24)

**Critical Issues:** 15
**Warning Issues:** 9
