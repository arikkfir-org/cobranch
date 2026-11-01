-- Merges asked of Cobranch: the Git server builds them and records each as a push of the default branch, over
-- transport api (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/branch-status.md, "By asking
-- Cobranch").
-- +goose Up
ALTER TABLE pushes DROP CONSTRAINT pushes_transport_check;
ALTER TABLE pushes ADD CONSTRAINT pushes_transport_check CHECK (transport IN ('https', 'ssh', 'api'));

-- +goose Down
-- Rolling back keeps the pushes of the merges asked of Cobranch, as pushes over https.
UPDATE pushes SET transport = 'https' WHERE transport = 'api';
ALTER TABLE pushes DROP CONSTRAINT pushes_transport_check;
ALTER TABLE pushes ADD CONSTRAINT pushes_transport_check CHECK (transport IN ('https', 'ssh'));
