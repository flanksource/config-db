package memory

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	_ "github.com/lib/pq"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	benchNamespace = "config-db-memory-bench"
	scraperName    = "configmap-memory-bench"
)

type benchConfig struct {
	Objects          int
	ChangesPerObject int
	Concurrency      int
	PayloadBytes     int
	ConfigDBBin      string
	DBURL            string
	HTTPPort         int
	ArtifactsDir     string
	DrainTimeout     time.Duration
	WatchWarmupSleep time.Duration
	PostUpdateSleep  time.Duration
}

type benchResult struct {
	Objects                int     `json:"objects"`
	ChangesPerObject       int     `json:"changes_per_object"`
	TotalChanges           int     `json:"total_changes"`
	ConfigDBMaxRSSBytes    uint64  `json:"config_db_max_rss_bytes"`
	DurationSeconds        float64 `json:"duration_seconds"`
	ConfigItems            int64   `json:"config_items"`
	ConfigChangesRows      int64   `json:"config_changes_rows"`
	ConfigChangesTotalSeen int64   `json:"config_changes_total_seen"`
	FinalRevisionItems     int64   `json:"final_revision_items"`
	MaxRevision            int     `json:"max_revision"`
	MaxRevisionItems       int64   `json:"max_revision_items"`
	ScraperID              string  `json:"scraper_id"`
}

func TestConfigDBKubernetesWatchMemory(t *testing.T) {
	if os.Getenv("CONFIG_DB_MEMORY_BENCH") != "1" {
		t.Skip("set CONFIG_DB_MEMORY_BENCH=1 to run the external config-db memory benchmark")
	}

	cfg := loadConfig(t)
	ctx := t.Context()

	if err := os.MkdirAll(cfg.ArtifactsDir, 0o755); err != nil {
		t.Fatalf("create artifacts dir: %v", err)
	}

	start := time.Now()
	if cfg.DBURL == "" {
		cfg.DBURL = startEmbeddedPostgres(t, cfg)
	}
	t.Logf("using postgres: %s", cfg.DBURL)
	runMigrations(ctx, t, cfg)

	t.Logf("starting envtest")
	testEnv := &envtest.Environment{
		BinaryAssetsDirectory: os.Getenv("KUBEBUILDER_ASSETS"),
	}
	restCfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	defer func() {
		if err := testEnv.Stop(); err != nil {
			t.Logf("stop envtest: %v", err)
		}
	}()
	restCfg.QPS = 10000
	restCfg.Burst = 10000

	kubeconfigPath := filepath.Join(cfg.ArtifactsDir, "envtest.kubeconfig")
	writeKubeconfig(t, restCfg, kubeconfigPath)

	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		t.Fatalf("create kube client: %v", err)
	}

	t.Logf("seeding %d configmaps", cfg.Objects)
	seedConfigMaps(ctx, t, client, cfg)

	scraperPath := filepath.Join(cfg.ArtifactsDir, "scraper.yaml")
	writeScraperConfig(t, scraperPath, kubeconfigPath)

	t.Logf("starting config-db: %s", cfg.ConfigDBBin)
	cmd := startConfigDB(ctx, t, cfg, scraperPath)
	defer stopConfigDB(t, cmd)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.HTTPPort)
	waitReady(ctx, t, baseURL)

	db := openDB(t, cfg.DBURL)
	defer db.Close()

	scraperID := waitForScraper(ctx, t, db)
	t.Logf("scraper id: %s", scraperID)

	t.Logf("running initial scrape")
	runScraperNow(ctx, t, baseURL, scraperID)
	waitForConfigItems(t, db, scraperID, int64(cfg.Objects))
	if cfg.WatchWarmupSleep > 0 {
		t.Logf("sleeping %s after initial scrape to let watchers settle", cfg.WatchWarmupSleep)
		time.Sleep(cfg.WatchWarmupSleep)
	}

	t.Logf("applying %d total configmap changes", cfg.Objects*cfg.ChangesPerObject)
	applyChanges(ctx, t, client, cfg)

	if cfg.PostUpdateSleep > 0 {
		t.Logf("sleeping %s after updates before reading DB/RSS", cfg.PostUpdateSleep)
		time.Sleep(cfg.PostUpdateSleep)
	}
	configChangesRows, configChangesTotal := readChangeCounts(t, db, scraperID)
	finalRevisionItems := countFinalRevision(t, db, scraperID, cfg.ChangesPerObject)
	maxRevision, maxRevisionItems := revisionProgress(t, db, scraperID)

	t.Logf("stopping config-db")
	stopConfigDB(t, cmd)

	maxRSS := maxRSSBytes(t, cmd)
	result := benchResult{
		Objects:                cfg.Objects,
		ChangesPerObject:       cfg.ChangesPerObject,
		TotalChanges:           cfg.Objects * cfg.ChangesPerObject,
		ConfigDBMaxRSSBytes:    maxRSS,
		DurationSeconds:        time.Since(start).Seconds(),
		ConfigItems:            countConfigItems(t, db, scraperID),
		ConfigChangesRows:      configChangesRows,
		ConfigChangesTotalSeen: configChangesTotal,
		FinalRevisionItems:     finalRevisionItems,
		MaxRevision:            maxRevision,
		MaxRevisionItems:       maxRevisionItems,
		ScraperID:              scraperID,
	}

	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	resultPath := filepath.Join(cfg.ArtifactsDir, "result.json")
	if err := os.WriteFile(resultPath, b, 0o644); err != nil {
		t.Fatalf("write result: %v", err)
	}

	t.Logf("memory benchmark result written to %s:\n%s", resultPath, string(b))
}

func loadConfig(t *testing.T) benchConfig {
	t.Helper()
	artifacts := getenv("BENCH_ARTIFACTS_DIR", filepath.Join(".", "bench-memory-results", time.Now().Format("20060102-150405")))
	return benchConfig{
		Objects:          getenvInt(t, "BENCH_OBJECTS", 10_000),
		ChangesPerObject: getenvInt(t, "BENCH_CHANGES_PER_OBJECT", 10),
		Concurrency:      getenvInt(t, "BENCH_CONCURRENCY", 100),
		PayloadBytes:     getenvInt(t, "BENCH_PAYLOAD_BYTES", 256),
		ConfigDBBin:      requireEnv(t, "CONFIG_DB_BIN"),
		DBURL:            os.Getenv("BENCH_DB_URL"),
		HTTPPort:         getenvInt(t, "CONFIG_DB_HTTP_PORT", freePort(t)),
		ArtifactsDir:     artifacts,
		DrainTimeout:     getenvDuration(t, "BENCH_DRAIN_TIMEOUT", 60*time.Minute),
		WatchWarmupSleep: getenvDuration(t, "BENCH_WATCH_WARMUP_SLEEP", 10*time.Second),
		PostUpdateSleep:  getenvDuration(t, "BENCH_POST_UPDATE_SLEEP", 30*time.Second),
	}
}

func writeKubeconfig(t *testing.T, cfg *rest.Config, path string) {
	t.Helper()
	clusters := map[string]*api.Cluster{"envtest": {Server: cfg.Host, CertificateAuthorityData: cfg.CAData}}
	authInfos := map[string]*api.AuthInfo{"envtest": {ClientCertificateData: cfg.CertData, ClientKeyData: cfg.KeyData, Token: cfg.BearerToken}}
	contexts := map[string]*api.Context{"envtest": {Cluster: "envtest", AuthInfo: "envtest"}}
	kubeconfig := api.Config{Kind: "Config", APIVersion: "v1", Clusters: clusters, AuthInfos: authInfos, Contexts: contexts, CurrentContext: "envtest"}
	if err := clientcmd.WriteToFile(kubeconfig, path); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
}

func seedConfigMaps(ctx context.Context, t *testing.T, client kubernetes.Interface, cfg benchConfig) {
	t.Helper()
	_, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: benchNamespace}}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}

	payload := strings.Repeat("x", cfg.PayloadBytes)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(cfg.Concurrency)
	var done int64
	for i := 0; i < cfg.Objects; i++ {
		i := i
		g.Go(func() error {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName(i),
					Namespace: benchNamespace,
					Labels: map[string]string{
						"bench.flanksource.com/name": scraperName,
					},
					Annotations: map[string]string{
						"bench.flanksource.com/rev": "0",
					},
				},
				Data: map[string]string{"payload": payload},
			}
			_, err := client.CoreV1().ConfigMaps(benchNamespace).Create(gctx, cm, metav1.CreateOptions{})
			if err != nil && !apierrors.IsAlreadyExists(err) {
				return err
			}
			if n := atomic.AddInt64(&done, 1); n%1000 == 0 {
				t.Logf("seeded %d/%d configmaps", n, cfg.Objects)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		t.Fatalf("seed configmaps: %v", err)
	}
}

func writeScraperConfig(t *testing.T, path, kubeconfigPath string) {
	t.Helper()
	contents := fmt.Sprintf(`apiVersion: configs.flanksource.com/v1
kind: ScrapeConfig
metadata:
  name: %s
spec:
  schedule: "@every 60m"
  kubernetes:
    - clusterName: envtest-memory-bench
      namespace: %s
      scope: namespace
      kubeconfig:
        value: %q
      watch:
        - apiVersion: v1
          kind: ConfigMap
      exclusions:
        name:
          - kube-root-ca.crt
`, scraperName, benchNamespace, kubeconfigPath)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write scraper config: %v", err)
	}
}

func startEmbeddedPostgres(t *testing.T, cfg benchConfig) string {
	t.Helper()
	port := freePort(t)
	database := "config_db_memory_bench"
	runtimePath := filepath.Join(os.TempDir(), fmt.Sprintf("config-db-memory-bench-pg-runtime-%d", time.Now().UnixNano()))
	dataPath := filepath.Join(os.TempDir(), fmt.Sprintf("config-db-memory-bench-pg-data-%d", time.Now().UnixNano()))
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Database(database).
		Port(uint32(port)).
		RuntimePath(runtimePath).
		DataPath(dataPath).
		Logger(io.Discard))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	t.Cleanup(func() {
		if err := pg.Stop(); err != nil {
			t.Logf("stop embedded postgres: %v", err)
		}
	})
	return fmt.Sprintf("postgres://postgres:postgres@localhost:%d/%s?sslmode=disable", port, database)
}

func runMigrations(ctx context.Context, t *testing.T, cfg benchConfig) {
	t.Helper()
	port := freePort(t)
	stdout, err := os.Create(filepath.Join(cfg.ArtifactsDir, "config-db-migrate.stdout.log"))
	if err != nil {
		t.Fatalf("create migrate stdout log: %v", err)
	}
	defer stdout.Close()
	stderr, err := os.Create(filepath.Join(cfg.ArtifactsDir, "config-db-migrate.stderr.log"))
	if err != nil {
		t.Fatalf("create migrate stderr log: %v", err)
	}
	defer stderr.Close()

	cmd := exec.CommandContext(ctx, cfg.ConfigDBBin,
		"serve",
		"--db", "DB_URL",
		"--db-migrations",
		"--disable-postgrest",
		"--httpPort", strconv.Itoa(port),
	)
	cmd.Env = envWith(map[string]string{"DB_URL": cfg.DBURL})
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start config-db migration run: %v", err)
	}
	waitReady(ctx, t, fmt.Sprintf("http://127.0.0.1:%d", port))
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

func startConfigDB(ctx context.Context, t *testing.T, cfg benchConfig, scraperPath string) *exec.Cmd {
	t.Helper()
	stdout, err := os.Create(filepath.Join(cfg.ArtifactsDir, "config-db.stdout.log"))
	if err != nil {
		t.Fatalf("create stdout log: %v", err)
	}
	stderr, err := os.Create(filepath.Join(cfg.ArtifactsDir, "config-db.stderr.log"))
	if err != nil {
		t.Fatalf("create stderr log: %v", err)
	}

	cmd := exec.CommandContext(ctx, cfg.ConfigDBBin,
		"serve",
		"--db", "DB_URL",
		"--disable-postgrest",
		"--httpPort", strconv.Itoa(cfg.HTTPPort),
		"--default-schedule", "@every 60m",
		scraperPath,
	)
	cmd.Env = envWith(map[string]string{
		"DB_URL":     cfg.DBURL,
		"KUBECONFIG": "",
		"GOGC":       getenv("BENCH_GOGC", "100"),
	})
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start config-db: %v", err)
	}
	return cmd
}

func stopConfigDB(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd == nil || cmd.Process == nil || cmd.ProcessState != nil {
		return
	}
	_ = cmd.Process.Kill()
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Logf("config-db wait error: %v", err)
		}
	}
}

func waitReady(ctx context.Context, t *testing.T, baseURL string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/ready", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("config-db did not become ready at %s", baseURL)
}

func openDB(t *testing.T, dbURL string) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	return db
}

func waitForScraper(ctx context.Context, t *testing.T, db *sql.DB) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		var id string
		err := db.QueryRowContext(ctx, `select id::text from config_scrapers where source = 'ConfigFile' and deleted_at is null order by created_at desc limit 1`).Scan(&id)
		if err == nil && id != "" {
			return id
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Logf("query scraper: %v", err)
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("scraper was not persisted")
	return ""
}

func runScraperNow(ctx context.Context, t *testing.T, baseURL, scraperID string) {
	t.Helper()
	body := bytes.NewBufferString(`{"async":false}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/run/"+scraperID, body)
	if err != nil {
		t.Fatalf("create run request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("run scraper: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("run scraper status=%d body=%s", resp.StatusCode, string(b))
	}
}

func waitForConfigItems(t *testing.T, db *sql.DB, scraperID string, expected int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		count := countConfigItems(t, db, scraperID)
		if count >= expected {
			return
		}
		t.Logf("waiting for config items: got=%d expected=%d", count, expected)
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("config item count did not reach %d; got %d", expected, countConfigItems(t, db, scraperID))
}

func applyChanges(ctx context.Context, t *testing.T, client kubernetes.Interface, cfg benchConfig) {
	t.Helper()
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(cfg.Concurrency)
	total := int64(cfg.Objects * cfg.ChangesPerObject)
	var done int64
	for rev := 1; rev <= cfg.ChangesPerObject; rev++ {
		rev := rev
		for i := 0; i < cfg.Objects; i++ {
			i := i
			g.Go(func() error {
				patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"bench.flanksource.com/rev":"%d"}}}`, rev))
				_, err := client.CoreV1().ConfigMaps(benchNamespace).Patch(gctx, configMapName(i), types.MergePatchType, patch, metav1.PatchOptions{})
				if err != nil {
					return err
				}
				if n := atomic.AddInt64(&done, 1); n%10000 == 0 {
					t.Logf("applied %d/%d changes", n, total)
				}
				return nil
			})
		}
	}
	if err := g.Wait(); err != nil {
		t.Fatalf("apply changes: %v", err)
	}
}

func countConfigItems(t *testing.T, db *sql.DB, scraperID string) int64 {
	t.Helper()
	var count int64
	err := db.QueryRow(`
select count(*)
from config_items
where scraper_id::text = $1
  and type = 'Kubernetes::ConfigMap'
  and deleted_at is null
  and config::jsonb #>> '{metadata,labels,bench.flanksource.com/name}' = $2`, scraperID, scraperName).Scan(&count)
	if err != nil {
		t.Fatalf("count config items: %v", err)
	}
	return count
}

func countFinalRevision(t *testing.T, db *sql.DB, scraperID string, revision int) int64 {
	t.Helper()
	var count int64
	err := db.QueryRow(`
select count(*)
from config_items
where scraper_id::text = $1
  and type = 'Kubernetes::ConfigMap'
  and deleted_at is null
  and config::jsonb #>> '{metadata,labels,bench.flanksource.com/name}' = $2
  and config::jsonb #>> '{metadata,annotations,bench.flanksource.com/rev}' = $3`, scraperID, scraperName, strconv.Itoa(revision)).Scan(&count)
	if err != nil {
		t.Fatalf("count final revision: %v", err)
	}
	return count
}

func revisionProgress(t *testing.T, db *sql.DB, scraperID string) (maxRevision int, countAtMax int64) {
	t.Helper()
	err := db.QueryRow(`
with revisions as (
  select nullif(config::jsonb #>> '{metadata,annotations,bench.flanksource.com/rev}', '')::int as revision
  from config_items
  where scraper_id::text = $1
    and type = 'Kubernetes::ConfigMap'
    and deleted_at is null
    and config::jsonb #>> '{metadata,labels,bench.flanksource.com/name}' = $2
)
select coalesce(max(revision), 0), count(*) filter (where revision = (select max(revision) from revisions))
from revisions`, scraperID, scraperName).Scan(&maxRevision, &countAtMax)
	if err != nil {
		t.Fatalf("read revision progress: %v", err)
	}
	return maxRevision, countAtMax
}

func readChangeCounts(t *testing.T, db *sql.DB, scraperID string) (rows int64, total int64) {
	t.Helper()
	err := db.QueryRow(`
select count(*), coalesce(sum(config_changes.count), 0)
from config_changes
join config_items on config_items.id::text = config_changes.config_id::text
where config_items.scraper_id::text = $1
  and config_items.type = 'Kubernetes::ConfigMap'
  and config_items.config::jsonb #>> '{metadata,labels,bench.flanksource.com/name}' = $2`, scraperID, scraperName).Scan(&rows, &total)
	if err != nil {
		t.Fatalf("read change counts: %v", err)
	}
	return rows, total
}

func maxRSSBytes(t *testing.T, cmd *exec.Cmd) uint64 {
	t.Helper()
	if cmd == nil || cmd.ProcessState == nil {
		t.Fatalf("config-db process state is not available")
	}
	usage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok || usage == nil {
		t.Fatalf("process rusage is not available")
	}
	maxRSS := uint64(usage.Maxrss)
	if runtime.GOOS == "linux" {
		maxRSS *= 1024
	}
	return maxRSS
}

func configMapName(i int) string {
	return fmt.Sprintf("cm-%05d", i)
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func requireEnv(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Fatalf("%s is required", key)
	}
	return v
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envWith(overrides map[string]string) []string {
	out := make([]string, 0, len(os.Environ())+len(overrides))
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if _, ok := overrides[key]; ok {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

func getenvInt(t *testing.T, key string, def int) int {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("invalid %s=%q: %v", key, v, err)
	}
	return i
}

func getenvDuration(t *testing.T, key string, def time.Duration) time.Duration {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		t.Fatalf("invalid %s=%q: %v", key, v, err)
	}
	return d
}
