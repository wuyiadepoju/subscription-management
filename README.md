# Subscription Management Service

A subscription management service built with clean architecture and DDD principles.

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

Clean Architecture with DDD:

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

For production migrations:
```bash
PROJECT_ID=my-project INSTANCE_ID=my-instance DATABASE_ID=my-db make migrate
```

## Testing

```bash
# E2E tests
make test-e2e
```

E2E tests use the Spanner emulator and cover create/cancel flows, refund calculations, error cases, and database persistence. See `e2e/e2e_test.go`.

## Documentation

- `REVIEW.md` - Issues found in the original implementation
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
