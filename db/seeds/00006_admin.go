package seeds

import (
	"context"
	"database/sql"

	"github.com/google/uuid"

	"cobranch.dev/db/seeds/queries"
)

// demoAdmin is the demo installation's administrator, whom a reviewer signs in as to try the Back-office. The seed
// grants nothing: a demo installation lists the email among the chart's auth.admins, which grants Installation admin
// at each sign-in, as it does a real installation's first administrators.
var demoAdmin = demoUser{id: uuid.MustParse("00000000-0000-7000-8000-00000000d005"), subject: "admin", name: "Ada Admin", email: "ada@cobranch.example"}

// admin seeds the demo administrator, as users seeds the others.
func admin(ctx context.Context, tx *sql.Tx) error {
	return seedUser(ctx, queries.New(tx), demoAdmin)
}
