package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/adapters/blob"
	"cobranch.dev/internal/adapters/checkout"
	"cobranch.dev/internal/adapters/controller"
	"cobranch.dev/internal/adapters/incluster"
	"cobranch.dev/internal/adapters/jobcluster"
	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/adapters/server"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/deliverer"
	"cobranch.dev/internal/services/runner"
	"cobranch.dev/internal/services/secrets"
	"cobranch.dev/internal/system/buildinfo"
	"cobranch.dev/internal/system/config"
	"cobranch.dev/internal/system/telemetry"
)

// runController runs the jobs' runs in the cluster it runs in, delivers the deliveries, and serves the health
// endpoints, until ctx ends.
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
	loops, closeLoops, err := controllerLoops(ctx, cfg, pool)
	if err != nil {
		return err
	}
	defer closeLoops()
	work, stop := context.WithCancel(ctx)
	var worked sync.WaitGroup
	for what, r := range loops {
		worked.Go(func() { controller.Run(work, what, r, cfg.PollInterval, cfg.SweepInterval) })
	}
	// The loops stop with the server, even one that failed before ctx ended, and the run or delivery in hand finishes
	// its step before the pool closes.
	defer func() { stop(); worked.Wait() }()
	return serve(ctx, cfg.HTTPAddr, cfg.ShutdownTimeout, schema, server.Handler("/", http.NotFoundHandler(),
		map[string]server.Check{"schema": schema.Check, "database": postgres.Ping(pool)}))
}

// controllerLoops are the runs' domain and the deliveries' over pool's database, the cluster the controller runs in,
// the secrets the key opens and the object store, which the returned func closes, by what each works through.
func controllerLoops(ctx context.Context, cfg config.Controller, pool *pgxpool.Pool) (map[string]controller.Runner, func(), error) {
	c, err := clusterOf(cfg)
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
	j, d, archive := cfg.Jobs, cfg.Delivery, blob.Logs{Bucket: bucket}
	runs := runner.NewService(postgres.NewRunStore(pool), c.runs, vault, archive, runner.Settings{Pods: j.Pods, Lease: j.Lease,
		RoomRetry: j.RoomRetry, DefaultTimeout: j.Timeout, MaxTimeout: j.MaxTimeout, ScriptImage: j.ScriptImage, RESTURL: j.RESTURL,
		GitURL: j.GitURL, LogLimit: int64(j.LogsMaxBytes), Vanished: j.Vanished})
	deliveries := deliverer.NewService(postgres.NewDeliveryStore(pool), c.renders, c.target, archive, deliverer.Settings{Pods: j.Pods,
		Lease: j.Lease, RoomRetry: j.RoomRetry, Timeout: d.RenderTimeout, RESTURL: j.RESTURL, GitURL: j.GitURL,
		LogLimit: int64(j.LogsMaxBytes), Vanished: j.Vanished, HealthEvery: d.HealthInterval, KeepRenders: d.RendersKeep})
	// A store closed as the controller exits has nothing left to flush.
	return map[string]controller.Runner{"runs": runs, "deliveries": deliveries}, func() { _ = bucket.Close() }, nil
}

// kube is what the controller reaches the cluster it runs in through: the job namespaces, where runs and renders
// execute, and the environments' namespaces, where deliveries apply.
type kube struct {
	runs    runner.Cluster
	renders deliverer.Pods
	target  deliverer.Target
}

// clusterOf is the cluster the controller runs in, through its pod's token, or, for an installation that runs no
// pods, whose controller mounts no token, none. Without in-cluster delivery it has no target.
func clusterOf(cfg config.Controller) (kube, error) {
	j := cfg.Jobs
	if !j.Pods {
		return kube{runs: runner.NoPods{}, renders: deliverer.NoPods{}}, nil
	}
	rc, err := rest.InClusterConfig()
	if err != nil {
		return kube{}, fmt.Errorf("reaching the cluster from its pod: %w", err)
	}
	client, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return kube{}, fmt.Errorf("making the cluster's client: %w", err)
	}
	jobs := jobcluster.New(client, jobcluster.Config{Prefix: j.NamespacePrefix, Installation: j.Installation,
		PodSecurity: j.PodSecurity, Labels: j.NamespaceLabels.Value, Home: j.Home, Controller: j.ServiceAccount, Release: j.Release,
		RunnerRole: j.RunnerRole, OwnerRole: j.OwnerRole, EgressExcept: j.EgressExcept, DNS: j.EgressDNS.Value, Resources: j.Resources.Value,
		Quota: j.Quota.Value, NodeSelector: j.NodeSelector.Value, Tolerations: j.Tolerations.Value, Affinity: j.Affinity.Value,
		CheckoutImage: j.CheckoutImage, RenderResources: cfg.RenderResources.Value})
	k := kube{runs: jobs, renders: jobs}
	if !cfg.InCluster {
		return k, nil
	}
	d := cfg.Delivery
	target, err := incluster.New(rc, incluster.Config{Installation: j.Installation, JobsPrefix: j.NamespacePrefix, PodSecurity: d.DeliveryPodSecurity,
		Labels: d.DeliveryLabels.Value, Home: j.Home, Controller: j.ServiceAccount, Account: d.Account, DeliveryRole: d.DeliveryRole,
		FenceRole: d.FenceRole, IngressFrom: d.IngressFrom.Value, EgressExcept: j.EgressExcept, DNS: j.EgressDNS.Value,
		Resources: d.DeliveryResources.Value, Quota: d.DeliveryQuota.Value})
	if err != nil {
		return kube{}, fmt.Errorf("delivering into the cluster: %w", err)
	}
	k.target = target
	return k, nil
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
