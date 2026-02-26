package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/wuyiadepoju/subscription-management/internal/app/subscription/migrations"
)

func main() {
	var (
		projectID  = flag.String("project", "test-project", "Spanner project ID")
		instanceID = flag.String("instance", "test-instance", "Spanner instance ID")
		databaseID = flag.String("database", "subscription-db", "Spanner database ID")
		timeout    = flag.Duration("timeout", 5*time.Minute, "Timeout for migration operations")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if err := migrations.RunMigrations(ctx, *projectID, *instanceID, *databaseID); err != nil {
		fmt.Fprintf(os.Stderr, "Migration failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("All migrations applied successfully!")
}
