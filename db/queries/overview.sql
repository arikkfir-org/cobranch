-- OverviewCounts counts, as they stand, the organization's members and agents, its open issues and those opened from
-- since until until, the live branches but main of the repositories of repo_ids that are Ready and Blocked, the agents
-- with a task running, and the tasks queued.
-- name: OverviewCounts :one
SELECT
    (SELECT count(*) FROM memberships m WHERE m.org_id = @org_id)::bigint AS members,
    (SELECT count(*) FROM agents a WHERE a.org_id = @org_id)::bigint AS agents,
    (SELECT count(*) FROM issues i WHERE i.org_id = @org_id AND i.closed_at IS NULL)::bigint AS open_issues,
    (SELECT count(*) FROM issues i WHERE i.org_id = @org_id AND i.created_at >= @since AND i.created_at < @until)::bigint AS opened_issues,
    (SELECT count(*) FROM branches b
     WHERE b.repo_id = ANY (@repo_ids::uuid[]) AND b.deleted_at IS NULL AND b.name <> 'main' AND b.status = 'ready')::bigint AS ready,
    (SELECT count(*) FROM branches b
     WHERE b.repo_id = ANY (@repo_ids::uuid[]) AND b.deleted_at IS NULL AND b.name <> 'main' AND b.status = 'blocked')::bigint AS blocked,
    (SELECT count(DISTINCT t.agent_id) FROM agent_tasks t WHERE t.org_id = @org_id AND t.state = 'running')::bigint AS working,
    (SELECT count(*) FROM agent_tasks t WHERE t.org_id = @org_id AND t.state = 'queued')::bigint AS queued;

-- OverviewDays sums up, by day in the IANA time zone zone, what the organization did from since until until: the tasks
-- queued, what its tasks' calls cost, and its job runs that ended, with the minutes each ran, rounded up. Days without
-- any are left out.
-- name: OverviewDays :many
SELECT x.day::date AS day, sum(x.tasks)::bigint AS tasks, sum(x.spent)::bigint AS spent,
    sum(x.job_minutes)::bigint AS job_minutes, sum(x.jobs)::bigint AS jobs
FROM (
    SELECT (t.created_at AT TIME ZONE @zone::text)::date AS day, 1 AS tasks, 0 AS spent, 0 AS job_minutes, 0 AS jobs
    FROM agent_tasks t
    WHERE t.org_id = @org_id AND t.created_at >= @since AND t.created_at < @until
    UNION ALL
    SELECT (s.created_at AT TIME ZONE @zone::text)::date, 0, s.cost, 0, 0
    FROM task_spend s
    WHERE s.org_id = @org_id AND s.created_at >= @since AND s.created_at < @until
    UNION ALL
    SELECT (r.ended_at AT TIME ZONE @zone::text)::date, 0, 0, ceil(extract(epoch FROM r.ended_at - coalesce(r.started_at, r.ended_at)) / 60), 1
    FROM job_runs r
    WHERE r.org_id = @org_id AND r.ended_at >= @since AND r.ended_at < @until
) x
GROUP BY x.day
ORDER BY x.day;

-- OverviewRepositories sums up what happened from since until until in each of the repositories of repo_ids that saw
-- anything then: the commits first pushed to it, the branches merged into its main, the tasks queued in it, what its
-- tasks' calls cost, and its job runs that ended, with the minutes each ran, rounded up.
-- name: OverviewRepositories :many
SELECT r.id, r.slug, x.commits, x.merged, x.tasks, x.spent, x.job_minutes, x.jobs
FROM repositories r
CROSS JOIN LATERAL (
    SELECT
        (SELECT count(*) FROM pushes p JOIN commits c ON c.first_push = p.id
         WHERE p.repo_id = r.id AND p.committed_at >= @since AND p.committed_at < @until)::bigint AS commits,
        (SELECT coalesce(sum(jsonb_array_length(p.merged)), 0) FROM pushes p
         WHERE p.repo_id = r.id AND p.committed_at >= @since AND p.committed_at < @until)::bigint AS merged,
        (SELECT count(*) FROM agent_tasks t
         WHERE t.org_id = @org_id AND t.repo_id = r.id AND t.created_at >= @since AND t.created_at < @until)::bigint AS tasks,
        (SELECT coalesce(sum(s.cost), 0) FROM task_spend s JOIN agent_tasks t ON t.id = s.task_id
         WHERE s.org_id = @org_id AND t.repo_id = r.id AND s.created_at >= @since AND s.created_at < @until)::bigint AS spent,
        (SELECT coalesce(sum(ceil(extract(epoch FROM j.ended_at - coalesce(j.started_at, j.ended_at)) / 60)), 0) FROM job_runs j
         WHERE j.org_id = @org_id AND j.repo_id = r.id AND j.ended_at >= @since AND j.ended_at < @until)::bigint AS job_minutes,
        (SELECT count(*) FROM job_runs j
         WHERE j.org_id = @org_id AND j.repo_id = r.id AND j.ended_at >= @since AND j.ended_at < @until)::bigint AS jobs
) x
WHERE r.id = ANY (@repo_ids::uuid[])
  AND (x.commits > 0 OR x.merged > 0 OR x.tasks > 0 OR x.spent > 0 OR x.jobs > 0)
ORDER BY x.spent DESC, x.job_minutes DESC, x.commits DESC, r.slug;
