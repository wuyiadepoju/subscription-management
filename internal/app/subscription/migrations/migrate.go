package migrations

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	admin "cloud.google.com/go/spanner/admin/database/apiv1"
	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	instanceadmin "cloud.google.com/go/spanner/admin/instance/apiv1"
	"cloud.google.com/go/spanner/admin/instance/apiv1/instancepb"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RunMigrations executes all SQL migration files in the migrations directory
func RunMigrations(ctx context.Context, projectID, instanceID, databaseID string) error {
	emulatorHost := os.Getenv("SPANNER_EMULATOR_HOST")

	projectName := fmt.Sprintf("projects/%s", projectID)
	instanceName := fmt.Sprintf("projects/%s/instances/%s", projectID, instanceID)
	databasePath := fmt.Sprintf("projects/%s/instances/%s/databases/%s", projectID, instanceID, databaseID)

	// Create instance admin client to check/create instance
	var instanceAdminClient *instanceadmin.InstanceAdminClient
	var err error

	fmt.Printf("Connecting to Spanner...\n")
	if emulatorHost != "" {
		fmt.Printf("Using emulator at %s\n", emulatorHost)
		// For emulator, endpoint should be without http:// for gRPC
		endpoint := emulatorHost
		if strings.Contains(emulatorHost, "://") {
			// Remove http:// or https:// prefix
			endpoint = strings.TrimPrefix(strings.TrimPrefix(emulatorHost, "http://"), "https://")
		}
		instanceAdminClient, err = instanceadmin.NewInstanceAdminClient(ctx, option.WithEndpoint(endpoint))
	} else {
		fmt.Printf("Using production Spanner\n")
		instanceAdminClient, err = instanceadmin.NewInstanceAdminClient(ctx)
	}
	if err != nil {
		return fmt.Errorf("failed to create instance admin client: %w", err)
	}
	defer instanceAdminClient.Close()

	// Check if instance exists, create if it doesn't
	fmt.Printf("Checking if instance exists: %s\n", instanceName)
	_, err = instanceAdminClient.GetInstance(ctx, &instancepb.GetInstanceRequest{
		Name: instanceName,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			fmt.Printf("Instance does not exist, creating: %s\n", instanceID)
			// For emulator, create instance with minimal config
			op, err := instanceAdminClient.CreateInstance(ctx, &instancepb.CreateInstanceRequest{
				Parent:     projectName,
				InstanceId: instanceID,
				Instance: &instancepb.Instance{
					DisplayName: instanceID,
				},
			})
			if err != nil {
				return fmt.Errorf("failed to create instance: %w", err)
			}

			// Wait for instance creation
			fmt.Printf("Waiting for instance creation...\n")
			_, err = op.Wait(ctx)
			if err != nil {
				return fmt.Errorf("instance creation failed: %w", err)
			}
			fmt.Printf("✓ Instance created: %s\n", instanceName)
		} else {
			return fmt.Errorf("failed to check instance existence: %w", err)
		}
	} else {
		fmt.Printf("✓ Instance exists: %s\n", instanceName)
	}

	// Create database admin client for DDL operations
	var adminClient *admin.DatabaseAdminClient
	if emulatorHost != "" {
		endpoint := emulatorHost
		if strings.Contains(emulatorHost, "://") {
			endpoint = strings.TrimPrefix(strings.TrimPrefix(emulatorHost, "http://"), "https://")
		}
		adminClient, err = admin.NewDatabaseAdminClient(ctx, option.WithEndpoint(endpoint))
	} else {
		adminClient, err = admin.NewDatabaseAdminClient(ctx)
	}
	if err != nil {
		return fmt.Errorf("failed to create database admin client: %w", err)
	}
	defer adminClient.Close()

	// Get migration files - find migrations directory relative to project root
	migrationsDir, err := findMigrationsDir()
	if err != nil {
		return fmt.Errorf("failed to find migrations directory: %w", err)
	}
	files, err := getMigrationFiles(migrationsDir)
	if err != nil {
		return fmt.Errorf("failed to get migration files: %w", err)
	}

	if len(files) == 0 {
		fmt.Printf("No migration files found in migrations/ directory\n")
		return nil
	}

	// Read all migration files and combine statements
	var allStatements []string
	for _, file := range files {
		fmt.Printf("Reading migration: %s\n", filepath.Base(file))
		sql, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", file, err)
		}

		// Extract DDL statements
		statements := parseDDLStatements(string(sql))
		if len(statements) == 0 {
			fmt.Printf("  Skipping (no DDL statements found)\n")
			continue
		}
		allStatements = append(allStatements, statements...)
		fmt.Printf("  Extracted %d DDL statement(s)\n", len(statements))
	}

	if len(allStatements) == 0 {
		fmt.Printf("No DDL statements found in migration files\n")
		return nil
	}

	// Check if database exists
	fmt.Printf("Checking if database exists: %s\n", databasePath)
	_, err = adminClient.GetDatabase(ctx, &databasepb.GetDatabaseRequest{
		Name: databasePath,
	})

	if err != nil {
		// Database doesn't exist, create it with DDL statements
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			fmt.Printf("Database does not exist, creating with migrations: %s\n", databaseID)
			op, err := adminClient.CreateDatabase(ctx, &databasepb.CreateDatabaseRequest{
				Parent:          instanceName,
				CreateStatement: fmt.Sprintf("CREATE DATABASE `%s`", databaseID),
				ExtraStatements: allStatements,
			})
			if err != nil {
				return fmt.Errorf("failed to create database: %w", err)
			}

			// Wait for database creation
			fmt.Printf("Waiting for database creation and migrations...\n")
			db, err := op.Wait(ctx)
			if err != nil {
				return fmt.Errorf("database creation failed: %w", err)
			}
			fmt.Printf("✓ Database created: %s\n", db.Name)
			fmt.Printf("✓ Successfully applied %d migration statement(s)\n", len(allStatements))
			return nil
		}
		return fmt.Errorf("failed to check database existence: %w", err)
	}

	// Database exists - apply migrations using UpdateDatabaseDdl
	fmt.Printf("✓ Database exists: %s\n", databaseID)
	fmt.Printf("Applying %d DDL statement(s)...\n", len(allStatements))

	op, err := adminClient.UpdateDatabaseDdl(ctx, &databasepb.UpdateDatabaseDdlRequest{
		Database:   databasePath,
		Statements: allStatements,
	})
	if err != nil {
		return fmt.Errorf("failed to start migrations: %w", err)
	}

	fmt.Printf("Waiting for DDL operations to complete...\n")
	if err := op.Wait(ctx); err != nil {
		return fmt.Errorf("failed to complete migrations: %w", err)
	}

	fmt.Printf("✓ Successfully applied %d migration statement(s)\n", len(allStatements))
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

// getMigrationFiles returns sorted list of migration SQL files
func getMigrationFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}

	sort.Strings(files)
	return files, nil
}

// parseDDLStatements parses SQL file into individual DDL statements
func parseDDLStatements(sql string) []string {
	var statements []string
	var currentStatement strings.Builder

	lines := strings.Split(sql, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and full-line comments
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}

		// Remove inline comments (-- comment)
		if idx := strings.Index(trimmed, "--"); idx >= 0 {
			trimmed = strings.TrimSpace(trimmed[:idx])
		}

		// Add line to current statement
		if currentStatement.Len() > 0 {
			currentStatement.WriteString(" ")
		}
		currentStatement.WriteString(trimmed)

		// If line ends with semicolon, finalize the statement
		if strings.HasSuffix(trimmed, ";") {
			stmt := strings.TrimSpace(currentStatement.String())
			// Remove trailing semicolon
			stmt = strings.TrimSuffix(stmt, ";")
			if stmt != "" {
				statements = append(statements, stmt)
			}
			currentStatement.Reset()
		}
	}

	// Handle any remaining statement without trailing semicolon
	if currentStatement.Len() > 0 {
		stmt := strings.TrimSpace(currentStatement.String())
		if stmt != "" {
			statements = append(statements, stmt)
		}
	}

	return statements
}
