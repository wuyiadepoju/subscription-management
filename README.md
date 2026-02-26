# Subscription Management Service

A clean architecture implementation of a subscription management service following Domain-Driven Design (DDD) principles.

## Project Structure

```
internal/app/subscription/
├── domain/                    # Business logic (aggregate root, events, errors, clock)
├── contracts/                 # Interfaces (repository, billing client)
├── usecases/                  # Application layer (create, cancel)
├── repo/                      # Repository implementation (Spanner adapter)
└── adapters/                  # External service adapters (HTTP billing client)
```

## Architecture

Clean Architecture with DDD principles:

- **Domain Layer**: Pure business logic, no infrastructure dependencies
- **Contracts**: Interfaces defining layer boundaries
- **Use Cases**: Application logic orchestrating domain and adapters
- **Adapters**: Infrastructure implementations (database, HTTP clients)

### Key Design Decisions

- Domain aggregate with private fields, behavior through methods
- Domain events for state changes (`SubscriptionCreatedEvent`, `SubscriptionCancelledEvent`)
- Money handling: `int64` cents (never `float64`)
- Time abstraction: `Clock` interface for testability
- Dependency inversion: all dependencies are interfaces

## Setup

### Prerequisites

- Go 1.21+
- Docker & Docker Compose (for Spanner emulator)
- Google Cloud Spanner (or emulator for local development)

### Quick Start

```bash
# Install dependencies
go mod download

# Start Spanner emulator
make spanner-up
export SPANNER_EMULATOR_HOST=localhost:9010

# Run migrations
make migrate
```

**Production migrations:**
```bash
PROJECT_ID=my-project INSTANCE_ID=my-instance DATABASE_ID=my-db make migrate
```

**Note:** Spanner uses `STRING` (not `VARCHAR`), `INT64` (not `BIGINT`), and `TIMESTAMP` for date/time. See `migrations/README.md` for details.

## Usage

```go
import (
    "context"
    "cloud.google.com/go/spanner"
    "github.com/wuyiadepoju/subscription-management/internal/app/subscription/domain"
    "github.com/wuyiadepoju/subscription-management/internal/app/subscription/usecases/create_subscription"
    "github.com/wuyiadepoju/subscription-management/internal/app/subscription/usecases/cancel_subscription"
    "github.com/wuyiadepoju/subscription-management/internal/app/subscription/repo"
    "github.com/wuyiadepoju/subscription-management/internal/app/subscription/adapters"
)

// Initialize (emulator example)
os.Setenv("SPANNER_EMULATOR_HOST", "localhost:9010")
client, _ := spanner.NewClient(ctx, "projects/test-project/instances/test-instance/databases/test-db",
    option.WithEndpoint("http://localhost:9010"))

repo := repo.NewSubscriptionRepo(client)
billingClient := adapters.NewHTTPBillingClient(http.DefaultClient, "https://api.billing.com")
clock := domain.RealClock{}

// Create subscription
createInteractor := create_subscription.NewInteractor(repo, billingClient, clock)
sub, event, err := createInteractor.Execute(ctx, create_subscription.Request{
    CustomerID: "cust-123",
    PlanID:     "plan-premium",
    PriceCents: 3000, // $30.00
})

// Cancel subscription
cancelInteractor := cancel_subscription.NewInteractor(repo, billingClient, clock, 30)
event, err := cancelInteractor.Execute(ctx, "sub-123")
```

## Testing

```bash
# E2E tests
make test-e2e
```

**E2E Tests:** Full flow using Spanner emulator. Covers create/cancel flow, refund calculations, error cases, and database persistence. Located in `e2e/e2e_test.go`.

## Documentation

- `REVIEW.md` - Analysis of issues in original implementation
- `ANSWERS.md` - Architectural Q&A (refund API placement, error handling, time/money calculations, testing)

## Key Features

- ✅ Proper layer separation (domain, use cases, adapters)
- ✅ Encapsulated domain aggregate with private fields
- ✅ Safe money handling (`int64` cents)
- ✅ Testable time-dependent logic (`Clock` interface)
- ✅ Comprehensive error handling
- ✅ Domain events for state changes
- ✅ Full dependency inversion (interfaces only)
- ✅ Complete testability with mocks

## License

MIT
