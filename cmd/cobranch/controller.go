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
	"cobranch.dev/internal/adapters/webhook"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/runner"
	"cobranch.dev/internal/services/secrets"
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
	runs, closeRuns, err := runService(ctx, cfg, pool)
	if err != nil {
		return err
	}
	defer closeRuns()
	work, stop := context.WithCancel(ctx)
	worked := make(chan struct{})
	go func() {
		defer close(worked)
		controller.Run(work, runs, cfg.PollInterval, cfg.SweepInterval)
	}()
	// The loop stops with the server, even one that failed before ctx ended, and the run in hand finishes its step
	// before the pool closes.
	defer func() { stop(); <-worked }()
	return serve(ctx, cfg.HTTPAddr, cfg.ShutdownTimeout, schema, server.Handler("/", http.NotFoundHandler(),
		map[string]server.Check{"schema": schema.Check, "database": postgres.Ping(pool)}))
}

// runService is the runs' domain over pool's database, the cluster the controller runs in, unless it runs no pods, the
// secrets the key opens, the object store, which the returned func closes, and the webhooks' receivers.
func runService(ctx context.Context, cfg config.Controller, pool *pgxpool.Pool) (*runner.Service, func(), error) {
	j := cfg.Jobs
	cluster, err := jobsCluster(j)
	if err != nil {
		return nil, nil, err
	}
	poster, err := webhook.New(j.EgressExcept, j.WebhooksAllowedCIDRs)
	if err != nil {
		return nil, nil, fmt.Errorf("configuring the webhooks: %w", err)
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
	settings := runner.Settings{Pods: j.Pods, Lease: j.Lease, RoomRetry: j.RoomRetry, DefaultTimeout: j.Timeout, MaxTimeout: j.MaxTimeout,
		ScriptImage: j.ScriptImage, RESTURL: j.RESTURL, GitURL: j.GitURL, LogLimit: int64(j.LogsMaxBytes), Vanished: j.Vanished}
	svc := runner.NewService(postgres.NewRunStore(pool), cluster, vault, blob.Logs{Bucket: bucket}, poster, settings)
	return svc, func() { _ = bucket.Close() }, nil // a store closed as the controller exits has nothing left to flush
}

// jobsCluster is where runs execute: the cluster the controller runs in, through its pod's token, or, for an
// installation that runs no pods, whose controller mounts no token, none.
func jobsCluster(j config.Jobs) (runner.Cluster, error) {
	if !j.Pods {
		return runner.NoPods{}, nil
	}
	client, err := inCluster()
	if err != nil {
		return nil, err
	}
	return jobcluster.New(client, jobcluster.Config{Prefix: j.NamespacePrefix, Installation: j.Installation,
		PodSecurity: j.PodSecurity, Labels: j.NamespaceLabels.Value, Home: j.Home, Controller: j.ServiceAccount, Release: j.Release,
		RunnerRole: j.RunnerRole, OwnerRole: j.OwnerRole, LogsRole: j.LogsRole, LogsReader: j.LogsReader, EgressExcept: j.EgressExcept, DNS: j.EgressDNS.Value,
		Resources: j.Resources.Value, Quota: j.Quota.Value, NodeSelector: j.NodeSelector.Value, Tolerations: j.Tolerations.Value,
		Affinity: j.Affinity.Value, CheckoutImage: j.CheckoutImage}), nil
}

// inCluster is a client of the cluster the workload runs in, as its pod's ServiceAccount.
func inCluster() (kubernetes.Interface, error) {
	kube, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("reaching the cluster from its pod: %w", err)
	}
	client, err := kubernetes.NewForConfig(kube)
	if err != nil {
		return nil, fmt.Errorf("making the cluster's client: %w", err)
	}
	return client, nil
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
