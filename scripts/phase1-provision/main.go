// phase1-provision grants only the isolated acceptance schema. The production
// provision command intentionally targets public; this helper makes the test
// target explicit instead of changing production deployment behavior.
package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
)

func main() {
	if len(os.Args) != 2 || !regexp.MustCompile(`^phase1qa_[0-9]+_[0-9]+$`).MatchString(os.Args[1]) {
		fmt.Fprintln(os.Stderr, "an isolated phase1qa schema argument is required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "isolated acceptance database configuration failed")
		os.Exit(1)
	}
	defer pool.Close()
	if err = operations.ProvisionFor(ctx, pool, os.Args[1], "moni_app", "moni_projection"); err != nil {
		fmt.Fprintln(os.Stderr, "isolated acceptance role provisioning failed")
		os.Exit(1)
	}
	fmt.Println("isolated acceptance schema provisioned")
}
