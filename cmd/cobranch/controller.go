package main

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/adapters/blob"
	"cobranch.dev/internal/adapters/checkout"
	"cobranch.dev/internal/adapters/controller"
	"cobranch.dev/internal/adapters/jobcluster"
	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/adapters/server"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/runner"
	"cobranch.dev/internal/services/secrets"
	"cobranch.dev/internal/services/taskrunner"
	"cobranch.dev/internal/system/buildinfo"
	"cobranch.dev/internal/system/config"
	"cobranch.dev/internal/system/telemetry"
)

// runController runs the jobs' runs in the cluster it runs in, and serves the health endpoints, until ctx ends.
func runController(ctx context.Context, logs io.Writer) error {
	flush, err := telemetry.Setup(ctx, logs, "controller", buildinfo.Version())
	if err != nil {
		return fmt.Errorf("setting up telemetry: %w", err)
	}
	defer flushTelemetry(ctx, flush)
	cfg, err := config.LoadController()
	if err != nil {
		return fmt.Errorf("configuring: %w", err)
	}
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to the database: %w", err)
	}
	defer pool.Close()
	schema, err := postgres.NewSchema(pool, migrations.FS, migrations.Go...)
	if err != nil {
		return fmt.Errorf("reading the schema's version: %w", err)
	}
	defer func() { _ = schema.Close() }() // holds nothing the controller wrote
	runners, closeRuns, err := runServices(ctx, cfg, pool)
	if err != nil {
		return err
	}
	defer closeRuns()
	work, stop := context.WithCancel(ctx)
	worked := make(chan struct{})
	go func() {
		defer close(worked)
		controller.Run(work, cfg.PollInterval, cfg.SweepInterval, runners...)
	}()
	// The loop stops with the server, even one that failed before ctx ended, and the run in hand finishes its step
	// before the pool closes.
	defer func() { stop(); <-worked }()
	return serve(ctx, cfg.HTTPAddr, cfg.ShutdownTimeout, schema, server.Handler("/", http.NotFoundHandler(),
		map[string]server.Check{"schema": schema.Check, "database": postgres.Ping(pool)}))
}

// runServices are the domains of jobs' runs and of agents' tasks over pool's database, the cluster the controller runs
// in, the secrets the key opens and the object store, which the returned func closes.
func runServices(ctx context.Context, cfg config.Controller, pool *pgxpool.Pool) ([]controller.Runner, func(), error) {
	runsCluster, tasksCluster, err := jobsCluster(cfg)
	if err != nil {
		return nil, nil, err
	}
	box, err := sealer(cfg.SecretsKey)
	if err != nil {
		return nil, nil, fmt.Errorf("configuring: %w", err)
	}
	bucket, err := blob.Open(ctx, cfg.BlobURL, pool)
	if err != nil {
		return nil, nil, fmt.Errorf("opening the object store: %w", err)
	}
	vault := secrets.NewService(postgres.NewSecretStore(pool), auth.NewAuthorizer(postgres.NewOrgStore(pool)), box)
	j := cfg.Jobs
	settings := runner.Settings{Pods: j.Pods, Lease: j.Lease, RoomRetry: j.RoomRetry, DefaultTimeout: j.Timeout, MaxTimeout: j.MaxTimeout,
		ScriptImage: j.ScriptImage, RESTURL: j.RESTURL, GitURL: j.GitURL, LogLimit: int64(j.LogsMaxBytes), Vanished: j.Vanished}
	runs := runner.NewService(postgres.NewRunStore(pool), runsCluster, vault, blob.Logs{Bucket: bucket}, settings)
	a := cfg.Agents
	tasks := taskrunner.NewService(postgres.NewTaskPodStore(pool), tasksCluster, vault, taskrunner.Settings{Pods: j.Pods, Lease: j.Lease,
		RoomRetry: j.RoomRetry, Timeout: a.Timeout, Vanished: j.Vanished, RESTURL: j.RESTURL, GitURL: j.GitURL, MaxTurns: a.MaxTurns,
		ShellTimeout: a.ShellTimeout, MaxTokens: a.MaxTokens})
	return []controller.Runner{runs, tasks}, func() { _ = bucket.Close() }, nil // a store closed as the controller exits has nothing left to flush
}

// jobsCluster is where runs and agents' tasks execute: the cluster the controller runs in, through its pod's token, or,
// for an installation that runs no pods, whose controller mounts no token, none.
func jobsCluster(cfg config.Controller) (runner.Cluster, taskrunner.Cluster, error) {
	j := cfg.Jobs
	if !j.Pods {
		return runner.NoPods{}, taskrunner.NoPods{}, nil
	}
	kube, err := rest.InClusterConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("reaching the cluster from its pod: %w", err)
	}
	client, err := kubernetes.NewForConfig(kube)
	if err != nil {
		return nil, nil, fmt.Errorf("making the cluster's client: %w", err)
	}
	cluster := jobcluster.New(client, jobcluster.Config{Prefix: j.NamespacePrefix, Installation: j.Installation,
		PodSecurity: j.PodSecurity, Labels: j.NamespaceLabels.Value, Home: j.Home, Controller: j.ServiceAccount, Release: j.Release,
		RunnerRole: j.RunnerRole, OwnerRole: j.OwnerRole, EgressExcept: j.EgressExcept, DNS: j.EgressDNS.Value, Resources: j.Resources.Value, Quota: j.Quota.Value,
		NodeSelector: j.NodeSelector.Value, Tolerations: j.Tolerations.Value, Affinity: j.Affinity.Value, CheckoutImage: j.CheckoutImage,
		AgentImage: cfg.Agents.Image, AgentResources: cfg.Agents.Resources.Value, ImagePullSecrets: j.ImagePullSecrets,
		ProviderURLs: cfg.Agents.ProviderURLs.Value})
	return cluster, cluster, nil
}

// runCheckout fetches a run's commit into its workspace, the first container of a run's pod, from the run's
// variables, with git's output on stderr.
func runCheckout(ctx context.Context, stderr io.Writer) error {
	cfg, err := config.LoadCheckout()
	if err != nil {
		return fmt.Errorf("configuring: %w", err)
	}
	if err := checkout.Run(ctx, checkout.Commit{URL: cfg.GitURL, Token: cfg.Token, SHA: cfg.SHA, Dir: cfg.Workspace}, stderr); err != nil {
		return fmt.Errorf("checking %s out: %w", cfg.SHA, err)
	}
	return nil
}
