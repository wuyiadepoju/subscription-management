package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	admin "cloud.google.com/go/spanner/admin/database/apiv1"
	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	instanceadmin "cloud.google.com/go/spanner/admin/instance/apiv1"
	"cloud.google.com/go/spanner/admin/instance/apiv1/instancepb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/wuyiadepoju/subscription-management/internal/app/subscription/domain"
	"github.com/wuyiadepoju/subscription-management/internal/app/subscription/repo"
	"github.com/wuyiadepoju/subscription-management/internal/app/subscription/usecases/cancel_subscription"
	"github.com/wuyiadepoju/subscription-management/internal/app/subscription/usecases/create_subscription"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	testProject  = "test-project"
	testInstance = "test-instance"
	testDatabase = "test-db"
	emulatorHost = "localhost:9010"
)

// MockBillingClient is a mock implementation of BillingClient for e2e tests
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

// testSetup holds test dependencies
type testSetup struct {
	ctx               context.Context
	cancel            context.CancelFunc
	database          string
	spannerClient     *spanner.Client
	adminClient       *admin.DatabaseAdminClient
	subscriptionRepo  *repo.SubscriptionRepo
	mockBillingClient *MockBillingClient
	createInteractor  *create_subscription.Interactor
	cancelInteractor  *cancel_subscription.Interactor
	clock             domain.Clock
}

// setupTest creates a test database and initializes all dependencies
func setupTest(t *testing.T) *testSetup {
	// Create context with timeout for setup operations to prevent hanging
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer setupCancel()

	// Set emulator host
	os.Setenv("SPANNER_EMULATOR_HOST", emulatorHost)
	defer os.Unsetenv("SPANNER_EMULATOR_HOST")

	// Create unique database name for this test
	dbName := fmt.Sprintf("%s-%s", testDatabase, uuid.New().String()[:8])
	database := fmt.Sprintf("projects/%s/instances/%s/databases/%s", testProject, testInstance, dbName)

	// Handle endpoint format for emulator
	endpoint := emulatorHost
	if strings.Contains(emulatorHost, "://") {
		endpoint = strings.TrimPrefix(strings.TrimPrefix(emulatorHost, "http://"), "https://")
	}

	// Create admin client with timeout context
	adminClient, err := admin.NewDatabaseAdminClient(setupCtx, option.WithEndpoint(endpoint))
	if err != nil {
		t.Fatalf("Failed to create admin client: %v. Make sure Spanner emulator is running (docker compose up -d)", err)
	}

	// Create instance if it doesn't exist (for emulator)
	instanceName := fmt.Sprintf("projects/%s/instances/%s", testProject, testInstance)
	projectName := fmt.Sprintf("projects/%s", testProject)

	instanceAdminClient, err := instanceadmin.NewInstanceAdminClient(setupCtx, option.WithEndpoint(endpoint))
	if err != nil {
		t.Fatalf("Failed to create instance admin client: %v", err)
	}
	defer instanceAdminClient.Close()

	_, err = instanceAdminClient.GetInstance(setupCtx, &instancepb.GetInstanceRequest{
		Name: instanceName,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			// Instance doesn't exist, create it
			op, err := instanceAdminClient.CreateInstance(setupCtx, &instancepb.CreateInstanceRequest{
				Parent:     projectName,
				InstanceId: testInstance,
				Instance: &instancepb.Instance{
					DisplayName: testInstance,
				},
			})
			if err != nil {
				t.Fatalf("Failed to create instance: %v", err)
			}
			_, err = op.Wait(setupCtx)
			if err != nil {
				if setupCtx.Err() == context.DeadlineExceeded {
					t.Fatalf("Timeout waiting for instance creation. Is Spanner emulator running? (docker compose up -d)")
				}
				t.Fatalf("Failed to wait for instance creation: %v", err)
			}
		} else {
			t.Fatalf("Failed to check instance existence: %v", err)
		}
	}

	// Create database
	op, err := adminClient.CreateDatabase(setupCtx, &databasepb.CreateDatabaseRequest{
		Parent:          instanceName,
		CreateStatement: fmt.Sprintf("CREATE DATABASE `%s`", dbName),
	})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}

	// Wait for database creation with timeout
	db, err := op.Wait(setupCtx)
	if err != nil {
		if setupCtx.Err() == context.DeadlineExceeded {
			t.Fatalf("Timeout waiting for database creation. Is Spanner emulator running? (docker compose up -d)")
		}
		t.Fatalf("Failed to wait for database creation: %v", err)
	}
	database = db.Name

	// Run migrations (apply schema)
	if err := runMigrations(setupCtx, adminClient, database); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Create a background context for test execution (not canceled when setup returns)
	ctx, cancel := context.WithCancel(context.Background())

	// Create Spanner client
	spannerEndpoint := emulatorHost
	if strings.Contains(emulatorHost, "://") {
		spannerEndpoint = strings.TrimPrefix(strings.TrimPrefix(emulatorHost, "http://"), "https://")
	}
	spannerClient, err := spanner.NewClient(ctx, database, option.WithEndpoint(spannerEndpoint))
	if err != nil {
		cancel()
		t.Fatalf("Failed to create Spanner client: %v", err)
	}

	// Initialize dependencies
	subscriptionRepo := repo.NewSubscriptionRepo(spannerClient)
	mockBillingClient := new(MockBillingClient)
	clock := domain.RealClock{}

	createInteractor := create_subscription.NewInteractor(
		subscriptionRepo,
		mockBillingClient,
		clock,
	)

	cancelInteractor := cancel_subscription.NewInteractor(
		subscriptionRepo,
		mockBillingClient,
		clock,
		30, // billing cycle days
	)

	return &testSetup{
		ctx:               ctx,
		cancel:            cancel,
		database:          database,
		spannerClient:     spannerClient,
		adminClient:       adminClient,
		subscriptionRepo:  subscriptionRepo,
		mockBillingClient: mockBillingClient,
		createInteractor:  createInteractor,
		cancelInteractor:  cancelInteractor,
		clock:             clock,
	}
}

// teardownTest cleans up test resources
func (ts *testSetup) teardownTest(t *testing.T) {
	// Cancel context first to stop any ongoing operations
	if ts.cancel != nil {
		ts.cancel()
	}

	if ts.spannerClient != nil {
		ts.spannerClient.Close()
	}

	if ts.adminClient != nil {
		// Use a fresh context for cleanup operations
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()

		// Drop database
		if err := ts.adminClient.DropDatabase(cleanupCtx, &databasepb.DropDatabaseRequest{
			Database: ts.database,
		}); err != nil {
			t.Logf("Failed to drop database: %v", err)
		}
		ts.adminClient.Close()
	}
}

// runMigrations runs database migrations by reading the migration file
func runMigrations(ctx context.Context, adminClient *admin.DatabaseAdminClient, database string) error {
	// Find migrations directory relative to project root
	migrationsDir, err := findMigrationsDir()
	if err != nil {
		return fmt.Errorf("failed to find migrations directory: %w", err)
	}

	migrationFile := filepath.Join(migrationsDir, "001_initial_schema.sql")
	migrationSQL, err := os.ReadFile(migrationFile)
	if err != nil {
		return fmt.Errorf("failed to read migration file: %w", err)
	}

	statements := parseDDLStatements(string(migrationSQL))

	op, err := adminClient.UpdateDatabaseDdl(ctx, &databasepb.UpdateDatabaseDdlRequest{
		Database:   database,
		Statements: statements,
	})
	if err != nil {
		return fmt.Errorf("failed to start DDL operation: %w", err)
	}

	err = op.Wait(ctx)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("timeout waiting for migrations. Is Spanner emulator running? (docker compose up -d)")
		}
		return fmt.Errorf("failed to wait for migrations: %w", err)
	}
	return nil
}

// findMigrationsDir finds the migrations directory relative to the project root
func findMigrationsDir() (string, error) {
	// Start from current working directory
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	// Walk up the directory tree to find go.mod (project root)
	dir := wd
	for {
		goModPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			// Found project root, migrations should be at migrations/
			migrationsPath := filepath.Join(dir, "migrations")
			if _, err := os.Stat(migrationsPath); err == nil {
				return migrationsPath, nil
			}
			return "", fmt.Errorf("migrations directory not found at %s", migrationsPath)
		}

		// Move up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root
			break
		}
		dir = parent
	}

	// Fallback: try relative path from current directory
	migrationsPath := filepath.Join(wd, "migrations")
	if _, err := os.Stat(migrationsPath); err == nil {
		return migrationsPath, nil
	}

	return "", fmt.Errorf("could not find migrations directory (searched from %s)", wd)
}

// parseDDLStatements parses SQL into DDL statements
func parseDDLStatements(sql string) []string {
	var statements []string
	current := ""

	lines := strings.Split(sql, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		current += " " + trimmed
		if strings.HasSuffix(trimmed, ";") {
			stmt := strings.TrimSpace(strings.TrimSuffix(current, ";"))
			if stmt != "" {
				statements = append(statements, stmt)
			}
			current = ""
		}
	}

	return statements
}

// cleanupDatabase deletes all test data
func (ts *testSetup) cleanupDatabase(t *testing.T) {
	// Delete all subscriptions
	_, err := ts.spannerClient.Apply(ts.ctx, []*spanner.Mutation{
		spanner.Delete("subscriptions", spanner.AllKeys()),
	})
	if err != nil {
		t.Logf("Failed to cleanup subscriptions: %v", err)
	}
}

// Test scenarios

func TestE2E_CreateAndCancelSubscription(t *testing.T) {
	ts := setupTest(t)
	defer ts.teardownTest(t)
	defer ts.cleanupDatabase(t)

	// Use fixed clock for deterministic tests
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	fixedClock := domain.FixedClock{FixedTime: startDate}

	// Create use cases with fixed clock
	createInteractor := create_subscription.NewInteractor(
		ts.subscriptionRepo,
		ts.mockBillingClient,
		fixedClock,
	)

	// Test data
	customerID := "cust-e2e-123"
	planID := "plan-premium"
	priceCents := int64(3000) // $30.00

	// Step 1: Create subscription
	t.Run("Create subscription", func(t *testing.T) {
		// Mock billing client validation
		ts.mockBillingClient.On("ValidateCustomer", ts.ctx, customerID).Return(nil)

		req := create_subscription.Request{
			CustomerID: customerID,
			PlanID:     planID,
			PriceCents: priceCents,
		}

		sub, event, err := createInteractor.Execute(ts.ctx, req)

		// Assertions
		require.NoError(t, err)
		require.NotNil(t, sub)
		require.NotNil(t, event)

		assert.Equal(t, customerID, sub.CustomerID())
		assert.Equal(t, planID, sub.PlanID())
		assert.Equal(t, priceCents, sub.Price())
		assert.Equal(t, domain.StatusActive, sub.Status())
		assert.Equal(t, startDate, sub.StartDate())

		assert.Equal(t, sub.ID(), event.SubscriptionID)
		assert.Equal(t, customerID, event.CustomerID)
		assert.Equal(t, planID, event.PlanID)
		assert.Equal(t, priceCents, event.Price)
		assert.Equal(t, startDate, event.CreatedAt)

		// Verify subscription was persisted
		persistedSub, err := ts.subscriptionRepo.FindByID(ts.ctx, sub.ID())
		require.NoError(t, err)
		assert.Equal(t, sub.ID(), persistedSub.ID())
		assert.Equal(t, customerID, persistedSub.CustomerID())
		assert.Equal(t, domain.StatusActive, persistedSub.Status())

		ts.mockBillingClient.AssertExpectations(t)
	})

	// Step 2: Retrieve subscription to get ID
	var subscriptionID string
	t.Run("Retrieve created subscription", func(t *testing.T) {
		// Query database to get subscription ID
		stmt := spanner.Statement{
			SQL:    `SELECT id FROM subscriptions WHERE customer_id = @customer_id LIMIT 1`,
			Params: map[string]any{"customer_id": customerID},
		}
		iter := ts.spannerClient.Single().Query(ts.ctx, stmt)
		defer iter.Stop()
		row, err := iter.Next()
		require.NoError(t, err)
		err = row.Columns(&subscriptionID)
		require.NoError(t, err)
		assert.NotEmpty(t, subscriptionID)
	})

	// Step 3: Cancel subscription (14 days later)
	t.Run("Cancel subscription with refund", func(t *testing.T) {
		// Set clock to 14 days after start
		cancelDate := startDate.AddDate(0, 0, 14)
		cancelClock := domain.FixedClock{FixedTime: cancelDate}

		// Create new cancel interactor with updated clock
		cancelInteractorWithClock := cancel_subscription.NewInteractor(
			ts.subscriptionRepo,
			ts.mockBillingClient,
			cancelClock,
			30,
		)

		// Expected refund: 3000 * (30 - 14) / 30 = 1600 cents
		expectedRefund := int64(1600)
		ts.mockBillingClient.On("ProcessRefund", ts.ctx, expectedRefund).Return(nil)

		event, err := cancelInteractorWithClock.Execute(ts.ctx, subscriptionID)

		// Assertions
		require.NoError(t, err)
		require.NotNil(t, event)

		assert.Equal(t, subscriptionID, event.SubscriptionID)
		assert.Equal(t, customerID, event.CustomerID)
		assert.Equal(t, expectedRefund, event.RefundAmount)
		assert.Equal(t, cancelDate, event.CancelledAt)

		// Verify subscription status was updated
		persistedSub, err := ts.subscriptionRepo.FindByID(ts.ctx, subscriptionID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCancelled, persistedSub.Status())

		ts.mockBillingClient.AssertExpectations(t)
	})

	// Step 4: Verify cannot cancel again
	t.Run("Cannot cancel already cancelled subscription", func(t *testing.T) {
		cancelDate := startDate.AddDate(0, 0, 15)
		cancelClock := domain.FixedClock{FixedTime: cancelDate}

		cancelInteractorWithClock := cancel_subscription.NewInteractor(
			ts.subscriptionRepo,
			ts.mockBillingClient,
			cancelClock,
			30,
		)

		event, err := cancelInteractorWithClock.Execute(ts.ctx, subscriptionID)

		// Should return error
		assert.Error(t, err)
		assert.Equal(t, domain.ErrAlreadyCancelled, err)
		assert.Nil(t, event)

		// Verify status is still cancelled
		persistedSub, err := ts.subscriptionRepo.FindByID(ts.ctx, subscriptionID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCancelled, persistedSub.Status())
	})
}

func TestE2E_CancelSubscription_NoRefund(t *testing.T) {
	ts := setupTest(t)
	defer ts.teardownTest(t)
	defer ts.cleanupDatabase(t)

	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := domain.FixedClock{FixedTime: startDate}

	createInteractor := create_subscription.NewInteractor(
		ts.subscriptionRepo,
		ts.mockBillingClient,
		clock,
	)

	// Create subscription
	ts.mockBillingClient.On("ValidateCustomer", ts.ctx, "cust-no-refund").Return(nil)
	req := create_subscription.Request{
		CustomerID: "cust-no-refund",
		PlanID:     "plan-basic",
		PriceCents: 2000,
	}

	sub, _, err := createInteractor.Execute(ts.ctx, req)
	require.NoError(t, err)

	// Cancel after 30 days (full cycle)
	cancelDate := startDate.AddDate(0, 0, 30)
	cancelClock := domain.FixedClock{FixedTime: cancelDate}

	cancelInteractor := cancel_subscription.NewInteractor(
		ts.subscriptionRepo,
		ts.mockBillingClient,
		cancelClock,
		30,
	)

	// No refund should be processed (amount is 0)
	event, err := cancelInteractor.Execute(ts.ctx, sub.ID())

	require.NoError(t, err)
	assert.Equal(t, int64(0), event.RefundAmount)

	// Verify ProcessRefund was NOT called (since refund amount is 0)
	ts.mockBillingClient.AssertNotCalled(t, "ProcessRefund", ts.ctx, mock.Anything)
}

func TestE2E_CancelSubscription_RefundCalculation(t *testing.T) {
	testCases := []struct {
		name           string
		priceCents     int64
		daysElapsed    int
		expectedRefund int64
	}{
		{
			name:           "half month used",
			priceCents:     3000,
			daysElapsed:    15,
			expectedRefund: 1500,
		},
		{
			name:           "one day used",
			priceCents:     3000,
			daysElapsed:    1,
			expectedRefund: 2900,
		},
		{
			name:           "full month used",
			priceCents:     3000,
			daysElapsed:    30,
			expectedRefund: 0,
		},
		{
			name:           "more than full month",
			priceCents:     3000,
			daysElapsed:    45,
			expectedRefund: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.teardownTest(t)
			defer ts.cleanupDatabase(t)

			startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			createClock := domain.FixedClock{FixedTime: startDate}

			createInteractor := create_subscription.NewInteractor(
				ts.subscriptionRepo,
				ts.mockBillingClient,
				createClock,
			)

			// Create subscription
			customerID := "cust-" + tc.name
			ts.mockBillingClient.On("ValidateCustomer", ts.ctx, customerID).Return(nil)

			req := create_subscription.Request{
				CustomerID: customerID,
				PlanID:     "plan-test",
				PriceCents: tc.priceCents,
			}

			sub, _, err := createInteractor.Execute(ts.ctx, req)
			require.NoError(t, err)

			// Cancel after specified days
			cancelDate := startDate.AddDate(0, 0, tc.daysElapsed)
			cancelClock := domain.FixedClock{FixedTime: cancelDate}

			cancelInteractor := cancel_subscription.NewInteractor(
				ts.subscriptionRepo,
				ts.mockBillingClient,
				cancelClock,
				30,
			)

			if tc.expectedRefund > 0 {
				ts.mockBillingClient.On("ProcessRefund", ts.ctx, tc.expectedRefund).Return(nil)
			}

			event, err := cancelInteractor.Execute(ts.ctx, sub.ID())

			require.NoError(t, err)
			assert.Equal(t, tc.expectedRefund, event.RefundAmount)

			// Verify subscription is cancelled
			persistedSub, err := ts.subscriptionRepo.FindByID(ts.ctx, sub.ID())
			require.NoError(t, err)
			assert.Equal(t, domain.StatusCancelled, persistedSub.Status())

			ts.mockBillingClient.AssertExpectations(t)
		})
	}
}

func TestE2E_CreateSubscription_InvalidCustomer(t *testing.T) {
	ts := setupTest(t)
	defer ts.teardownTest(t)
	defer ts.cleanupDatabase(t)

	// Mock billing client to return error
	ts.mockBillingClient.On("ValidateCustomer", ts.ctx, "invalid-customer").Return(domain.ErrInvalidCustomer)

	req := create_subscription.Request{
		CustomerID: "invalid-customer",
		PlanID:     "plan-test",
		PriceCents: 1000,
	}

	sub, event, err := ts.createInteractor.Execute(ts.ctx, req)

	// Should return error
	assert.Error(t, err)
	assert.Equal(t, domain.ErrInvalidCustomer, err)
	assert.Nil(t, sub)
	assert.Nil(t, event)

	// Verify no subscription was created
	stmt := spanner.Statement{
		SQL:    `SELECT COUNT(*) as count FROM subscriptions WHERE customer_id = @customer_id`,
		Params: map[string]any{"customer_id": "invalid-customer"},
	}
	iter := ts.spannerClient.Single().Query(ts.ctx, stmt)
	defer iter.Stop()
	row, err := iter.Next()
	require.NoError(t, err)
	var count int64
	err = row.Columns(&count)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	ts.mockBillingClient.AssertExpectations(t)
}

func TestE2E_CancelSubscription_NotFound(t *testing.T) {
	ts := setupTest(t)
	defer ts.teardownTest(t)
	defer ts.cleanupDatabase(t)

	// Try to cancel non-existent subscription
	event, err := ts.cancelInteractor.Execute(ts.ctx, "non-existent-id")

	// Should return error
	assert.Error(t, err)
	assert.Equal(t, domain.ErrSubscriptionNotFound, err)
	assert.Nil(t, event)

	// Verify ProcessRefund was NOT called
	ts.mockBillingClient.AssertNotCalled(t, "ProcessRefund", ts.ctx, mock.Anything)
}
