package seeds

import (
	"context"
	"database/sql"
)

// baseline is the seed every later one builds on. It writes nothing yet: Cobranch's first tables come with sign-in.
func baseline(context.Context, *sql.Tx) error {
	return nil
}
