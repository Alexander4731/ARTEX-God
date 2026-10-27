package evidence

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// Take the shared suite lock before any schema initialization, which otherwise
// races with db, agent and server tests. Hold it on one pinned connection.
func TestMain(m *testing.M) {
	if os.Getenv("ARTEX_PG_DSN") == "" {
		os.Exit(m.Run())
	}
	os.Exit(runEvidenceSuite(m))
}
func runEvidenceSuite(m *testing.M) int {
	pg, err := sql.Open("pgx", os.Getenv("ARTEX_PG_DSN"))
	if err != nil {
		panic(err)
	}
	defer pg.Close()
	conn, err := pg.Conn(context.Background())
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(context.Background(), `SELECT pg_advisory_lock(7337741002)`); err != nil {
		panic(err)
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(7337741002)`)
	initialized, err := db.Open(os.Getenv("ARTEX_PG_DSN"))
	if err != nil {
		panic(err)
	}
	defer initialized.Close()
	return m.Run()
}
