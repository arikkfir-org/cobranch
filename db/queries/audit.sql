-- SetPrincipal names the principal the transaction acts for, which trigger audit() records; local to the transaction.
-- name: SetPrincipal :exec
SELECT set_config('cobranch.principal_id', @principal_id::text, true);
