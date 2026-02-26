.PHONY: help spanner-up spanner-down spanner-logs migrate migrate-create test test-e2e test-unit

# Default values for migrations
PROJECT_ID ?= test-project
INSTANCE_ID ?= test-instance
DATABASE_ID ?= subscription-db

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

spanner-up: ## Start Spanner emulator
	docker compose up -d
	@echo "Spanner emulator started on localhost:9010"
	@echo "Set SPANNER_EMULATOR_HOST=localhost:9010"

spanner-down: ## Stop Spanner emulator
	docker compose down

spanner-logs: ## View Spanner emulator logs
	docker compose logs -f spanner-emulator

migrate: ## Run database migrations (use PROJECT_ID, INSTANCE_ID, DATABASE_ID env vars)
	@if [ -z "$$SPANNER_EMULATOR_HOST" ]; then \
		echo "Warning: SPANNER_EMULATOR_HOST not set. Using emulator default."; \
		SPANNER_EMULATOR_HOST=localhost:9010 go run cmd/migrate/main.go \
			-project $(PROJECT_ID) \
			-instance $(INSTANCE_ID) \
			-database $(DATABASE_ID); \
	else \
		go run cmd/migrate/main.go \
			-project $(PROJECT_ID) \
			-instance $(INSTANCE_ID) \
			-database $(DATABASE_ID); \
	fi

migrate-create: ## Create database and run migrations (emulator only)
	@echo "Creating database $(DATABASE_ID) and applying migrations..."
	SPANNER_EMULATOR_HOST=localhost:9010 make migrate


test-e2e: ## Run e2e tests
	SPANNER_EMULATOR_HOST=localhost:9010 go test ./internal/app/subscription/e2e/... -v

