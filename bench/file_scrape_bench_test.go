//go:build bench
package bench

import (
	stdcontext "context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	configdbapi "github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers"
	dutycontext "github.com/flanksource/duty/context"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/shirou/gopsutil/v3/process"
	"gorm.io/gorm"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

var benchmarkCallbackRegistration sync.Once

type benchmarkCollectorContextKey struct{}

const benchmarkQueryStartKey = "bench:query_start"

type benchmarkRunMetrics struct {
	Scenario          string                     `json:"scenario"`
	Iterations        int                        `json:"iterations"`
	BeforeMemory      benchmarkMemorySnapshot    `json:"before_memory"`
	AfterMemory       benchmarkMemorySnapshot    `json:"after_memory"`
	During            benchmarkDuringMetrics     `json:"during"`
	Summary           *v1.ScrapeSummary          `json:"summary,omitempty"`
	Snapshots         *v1.ScrapeSnapshotPair     `json:"snapshots,omitempty"`
	QueryMetrics      benchmarkQueryMetrics      `json:"query_metrics"`
	PGStatStatements  *benchmarkPGStatStatements `json:"pg_stat_statements,omitempty"`
	QueryLog          benchmarkQueryLogSettings  `json:"query_log"`
	Preseed           benchmarkEntityCounts      `json:"preseed"`
	Scrape            benchmarkEntityCounts      `json:"scrape"`
	OverlapConfigRows int                        `json:"overlap_config_rows"`
}

type benchmarkMemorySnapshot struct {
	RSSBytes     uint64 `json:"rss_bytes"`
	HeapAlloc    uint64 `json:"heap_alloc_bytes"`
	HeapInuse    uint64 `json:"heap_inuse_bytes"`
	HeapObjects  uint64 `json:"heap_objects"`
	Goroutines   int    `json:"goroutines"`
	NumGC        uint32 `json:"num_gc"`
	PauseTotalNs uint64 `json:"pause_total_ns"`
	TotalAlloc   uint64 `json:"total_alloc_bytes"`
	SystemBytes  uint64 `json:"system_bytes"`
	StackInuse   uint64 `json:"stack_inuse_bytes"`
}

type benchmarkDuringMetrics struct {
	SampleInterval string  `json:"sample_interval"`
	SampleCount    int     `json:"sample_count"`
	PeakRSSBytes   uint64  `json:"peak_rss_bytes"`
	PeakHeapAlloc  uint64  `json:"peak_heap_alloc_bytes"`
	PeakGoroutines int     `json:"peak_goroutines"`
	MaxCPUPercent  float64 `json:"max_cpu_percent"`
	AvgCPUPercent  float64 `json:"avg_cpu_percent"`
}

type benchmarkQueryMetrics struct {
	Count         int64                          `json:"count"`
	TotalDuration time.Duration                  `json:"total_duration"`
	SlowCount     int64                          `json:"slow_count"`
	ByOperation   map[string]benchmarkQueryStats `json:"by_operation,omitempty"`
	TopStatements []benchmarkStatementStats      `json:"top_statements,omitempty"`
}

type benchmarkQueryStats struct {
	Count         int64         `json:"count"`
	RowsAffected  int64         `json:"rows_affected"`
	TotalDuration time.Duration `json:"total_duration"`
}

type benchmarkStatementStats struct {
	Statement     string        `json:"statement"`
	Count         int64         `json:"count"`
	RowsAffected  int64         `json:"rows_affected"`
	TotalDuration time.Duration `json:"total_duration"`
}

type benchmarkPGStatStatements struct {
	Available     bool                       `json:"available"`
	Calls         int64                      `json:"calls"`
	Rows          int64                      `json:"rows"`
	TotalExecMS   float64                    `json:"total_exec_ms"`
	SharedHit     int64                      `json:"shared_hit_blocks"`
	SharedRead    int64                      `json:"shared_read_blocks"`
	TopStatements []benchmarkPGStatementStat `json:"top_statements,omitempty"`
}

type benchmarkPGStatementStat struct {
	Query       string  `json:"query"`
	Calls       int64   `json:"calls"`
	Rows        int64   `json:"rows"`
	TotalExecMS float64 `json:"total_exec_ms"`
	SharedHit   int64   `json:"shared_hit_blocks"`
	SharedRead  int64   `json:"shared_read_blocks"`
}

type benchmarkQueryLogSettings struct {
	Mode                    string `json:"mode"`
	Available               bool   `json:"available"`
	Enabled                 bool   `json:"enabled"`
	LogStatement            string `json:"log_statement,omitempty"`
	LogMinDurationStatement string `json:"log_min_duration_statement,omitempty"`
	LogDestination          string `json:"log_destination,omitempty"`
	LoggingCollector        string `json:"logging_collector,omitempty"`
	Error                   string `json:"error,omitempty"`
}

type benchmarkQueryCollector struct {
	mu          sync.Mutex
	totalCount  int64
	totalTime   time.Duration
	slowCount   int64
	byOperation map[string]*benchmarkQueryStats
	statements  map[string]*benchmarkStatementStats
}

type benchmarkProcessSampler struct {
	process  *process.Process
	interval time.Duration

	done chan struct{}
	wg   sync.WaitGroup

	mu             sync.Mutex
	sampleCount    int
	peakRSS        uint64
	peakHeapAlloc  uint64
	peakGoroutines int
	maxCPUPercent  float64
	totalCPU       float64
}

func BenchmarkFileScrapeScenarios(b *testing.B) {
	ensureBenchmarkCallbacks(b)
	scenarios := loadBenchmarkScenarios(b)

	for _, scenario := range scenarios {
		scenario := scenario
		b.Run(scenario.Name, func(b *testing.B) {
			runFileScrapeScenarioBenchmark(b, scenario)
		})
	}
}

func runFileScrapeScenarioBenchmark(b *testing.B, scenario benchmarkScenario) {
	b.Helper()
	b.ReportAllocs()
	b.SetBytes(int64(max(scenario.Scrape.Configs, 1)))

	runScraperID := mustDeterministicUUID("bench-run-scraper:" + scenario.Name)
	backgroundScraperID := mustDeterministicUUID("bench-background-scraper:" + scenario.Name)

	ensureBenchScraperRow(b, runScraperID, "bench/file-scrape/"+scenario.Name)
	ensureBenchScraperRow(b, backgroundScraperID, "bench/background/"+scenario.Name)
	defer cleanupBenchmarkScenarioData(b, runScraperID, backgroundScraperID)

	queryLog := captureQueryLogSettings(b, scenario.DBMetrics.QueryLog)
	pgStatsState := preparePGStatStatements(b, scenario.DBMetrics.PGStatStatements)

	var lastRun benchmarkRunMetrics
	var aggregate benchmarkRunMetrics
	var totalElapsed time.Duration
	aggregate.Scenario = scenario.Name
	aggregate.Preseed = scenario.Preseed
	aggregate.Scrape = scenario.Scrape
	aggregate.QueryMetrics.ByOperation = map[string]benchmarkQueryStats{}

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cleanupBenchmarkScenarioData(b, runScraperID, backgroundScraperID)

		preseed := buildPreseedDataset(b, scenario, backgroundScraperID)
		if err := seedBenchmarkDataset(preseed); err != nil {
			b.Fatalf("failed to seed benchmark dataset: %v", err)
		}

		scrapeDataset := buildScrapeDataset(b, scenario, benchmarkConfigRefs(preseed.Configs))
		payloadPath := writeScenarioScrapeFile(b, scenario, scrapeDataset)

		runMetrics := benchmarkRunMetrics{
			Scenario:          scenario.Name,
			Iterations:        1,
			QueryLog:          queryLog,
			Preseed:           scenario.Preseed,
			Scrape:            scenario.Scrape,
			OverlapConfigRows: scrapeDataset.OverlapConfig,
		}
		runMetrics.BeforeMemory = captureMemorySnapshot(b)

		beforeSnapshot, err := db.CaptureScrapeSnapshot(newBenchScrapeContextForFileBenchmark(b, scenario, runScraperID, payloadPath, nil), time.Now())
		if err != nil {
			b.Fatalf("failed to capture pre-run snapshot: %v", err)
		}

		collector := newBenchmarkQueryCollector()
		scrapeCtx := newBenchScrapeContextForFileBenchmark(b, scenario, runScraperID, payloadPath, collector)
		if pgStatsState.available {
			if err := resetPGStatStatements(scrapeCtx.Context); err != nil {
				if pgStatsState.required {
					b.Fatalf("failed to reset pg_stat_statements: %v", err)
				}
				pgStatsState.available = false
			}
		}

		sampler := startBenchmarkProcessSampler(b, scenario.SampleInterval.Duration)

		b.StartTimer()
		start := time.Now()
		output, err := scrapers.RunScraper(scrapeCtx)
		b.StopTimer()
		elapsed := time.Since(start)

		runMetrics.AfterMemory = captureMemorySnapshot(b)
		runMetrics.During = sampler.Stop()
		if err != nil {
			b.Fatalf("scraper run failed: %v", err)
		}
		if output == nil {
			b.Fatalf("scraper run returned nil output")
		}
		runMetrics.Summary = &output.Summary

		afterSnapshot, err := db.CaptureScrapeSnapshot(scrapeCtx, start)
		if err != nil {
			b.Fatalf("failed to capture post-run snapshot: %v", err)
		}
		runMetrics.Snapshots = &v1.ScrapeSnapshotPair{
			Before: beforeSnapshot,
			After:  afterSnapshot,
			Diff:   v1.DiffSnapshots(beforeSnapshot, afterSnapshot),
		}
		runMetrics.QueryMetrics = collector.Snapshot()

		if pgStatsState.available {
			pgStats, err := capturePGStatStatements(scrapeCtx.Context)
			if err != nil {
				if pgStatsState.required {
					b.Fatalf("failed to capture pg_stat_statements: %v", err)
				}
			} else {
				runMetrics.PGStatStatements = pgStats
			}
		}

		lastRun = runMetrics
		aggregateBenchmarkMetrics(&aggregate, runMetrics)
		totalElapsed += elapsed
	}

	finalizeAggregateMetrics(&aggregate, b.N)
	reportBenchmarkMetrics(b, aggregate, totalElapsed/time.Duration(max(b.N, 1)))
	writeBenchmarkReport(b, scenario, aggregate, lastRun)
}

func ensureBenchScraperRow(tb testing.TB, scraperID uuid.UUID, name string) {
	tb.Helper()

	var existing dutymodels.ConfigScraper
	if err := testCtx.DB().Where("id = ?", scraperID).First(&existing).Error; err == nil {
		return
	}

	row := dutymodels.ConfigScraper{
		ID:        scraperID,
		Name:      name,
		Namespace: "bench",
		Spec:      "{}",
		Source:    dutymodels.SourceUI,
	}
	if err := testCtx.DB().Create(&row).Error; err != nil {
		tb.Fatalf("failed to create benchmark scraper row %s: %v", name, err)
	}
}

func newBenchScrapeContextForFileBenchmark(tb testing.TB, scenario benchmarkScenario, scraperID uuid.UUID, payloadPath string, collector *benchmarkQueryCollector) configdbapi.ScrapeContext {
	tb.Helper()

	baseCtx := testCtx
	if collector != nil {
		baseCtx = baseCtx.WithValue(benchmarkCollectorContextKey{}, collector)
	}

	sc := v1.ScrapeConfig{}
	sc.Name = scenario.Name
	sc.Namespace = "bench"
	sc.SetUID(k8stypes.UID(scraperID.String()))
	sc.Spec = v1.ScraperSpec{
		Full:     true,
		LogLevel: "error",
		File: []v1.File{{
			BaseScraper: v1.BaseScraper{
				CustomScraperBase: v1.CustomScraperBase{
					Items: "$.items[*]",
					ID:    "$.id",
					Name:  "$.name",
					Type:  "$.type",
					Class: "$.class",
				},
			},
			Format: "json",
			Paths:  []string{payloadPath},
		}},
	}

	ctx := configdbapi.NewScrapeContext(baseCtx).WithScrapeConfig(&sc)
	ctx, err := ctx.InitTempCache()
	if err != nil {
		tb.Fatalf("failed to initialize scrape context temp cache: %v", err)
	}
	return ctx
}

func cleanupBenchmarkScenarioData(tb testing.TB, scraperIDs ...uuid.UUID) {
	tb.Helper()

	for _, scraperID := range scraperIDs {
		queries := []string{
			"DELETE FROM config_relationships WHERE config_id IN (SELECT id FROM config_items WHERE scraper_id = ?) OR related_id IN (SELECT id FROM config_items WHERE scraper_id = ?)",
			"DELETE FROM config_access_logs WHERE scraper_id = ?",
			"DELETE FROM config_access WHERE scraper_id = ?",
			"DELETE FROM config_analysis WHERE scraper_id = ?",
			"DELETE FROM external_user_groups WHERE external_user_id IN (SELECT id FROM external_users WHERE scraper_id = ?) OR external_group_id IN (SELECT id FROM external_groups WHERE scraper_id = ?)",
			"DELETE FROM external_roles WHERE scraper_id = ?",
			"DELETE FROM external_groups WHERE scraper_id = ?",
			"DELETE FROM external_users WHERE scraper_id = ?",
			"DELETE FROM config_changes WHERE config_id IN (SELECT id FROM config_items WHERE scraper_id = ?)",
			"DELETE FROM config_items_last_scraped_time WHERE config_id IN (SELECT id FROM config_items WHERE scraper_id = ?)",
			"DELETE FROM config_items WHERE scraper_id = ?",
		}
		for _, query := range queries {
			var err error
			if strings.Count(query, "?") == 2 {
				err = testCtx.DB().Exec(query, scraperID.String(), scraperID.String()).Error
			} else {
				err = testCtx.DB().Exec(query, scraperID.String()).Error
			}
			if err != nil {
				tb.Fatalf("cleanup failed for scraper %s: %v", scraperID, err)
			}
		}
	}
}

func seedBenchmarkDataset(dataset benchmarkDataset) error {
	dbConn := testCtx.DB()
	if len(dataset.Configs) > 0 {
		if err := dbConn.CreateInBatches(dataset.Configs, 500).Error; err != nil {
			return fmt.Errorf("insert config_items: %w", err)
		}
	}
	if len(dataset.Users) > 0 {
		if err := dbConn.CreateInBatches(dataset.Users, 500).Error; err != nil {
			return fmt.Errorf("insert external_users: %w", err)
		}
	}
	if len(dataset.Roles) > 0 {
		if err := dbConn.CreateInBatches(dataset.Roles, 500).Error; err != nil {
			return fmt.Errorf("insert external_roles: %w", err)
		}
	}
	if len(dataset.ConfigAccess) > 0 {
		if err := dbConn.CreateInBatches(dataset.ConfigAccess, 500).Error; err != nil {
			return fmt.Errorf("insert config_access: %w", err)
		}
	}
	if len(dataset.AccessLogs) > 0 {
		if err := dbConn.CreateInBatches(dataset.AccessLogs, 500).Error; err != nil {
			return fmt.Errorf("insert config_access_logs: %w", err)
		}
	}
	if len(dataset.Changes) > 0 {
		if err := dbConn.CreateInBatches(dataset.Changes, 500).Error; err != nil {
			return fmt.Errorf("insert config_changes: %w", err)
		}
	}
	if len(dataset.Analysis) > 0 {
		if err := dbConn.CreateInBatches(dataset.Analysis, 500).Error; err != nil {
			return fmt.Errorf("insert config_analysis: %w", err)
		}
	}
	return nil
}

func captureMemorySnapshot(tb testing.TB) benchmarkMemorySnapshot {
	tb.Helper()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	proc, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		tb.Fatalf("failed to inspect current process: %v", err)
	}
	info, err := proc.MemoryInfo()
	if err != nil {
		tb.Fatalf("failed to collect memory info: %v", err)
	}

	return benchmarkMemorySnapshot{
		RSSBytes:     info.RSS,
		HeapAlloc:    mem.HeapAlloc,
		HeapInuse:    mem.HeapInuse,
		HeapObjects:  mem.HeapObjects,
		Goroutines:   runtime.NumGoroutine(),
		NumGC:        mem.NumGC,
		PauseTotalNs: mem.PauseTotalNs,
		TotalAlloc:   mem.TotalAlloc,
		SystemBytes:  mem.Sys,
		StackInuse:   mem.StackInuse,
	}
}

func startBenchmarkProcessSampler(tb testing.TB, interval time.Duration) *benchmarkProcessSampler {
	tb.Helper()

	proc, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		tb.Fatalf("failed to create process sampler: %v", err)
	}
	sampler := &benchmarkProcessSampler{
		process:  proc,
		interval: interval,
		done:     make(chan struct{}),
	}
	sampler.recordSample()
	sampler.wg.Add(1)
	go func() {
		defer sampler.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				sampler.recordSample()
			case <-sampler.done:
				return
			}
		}
	}()
	return sampler
}

func (s *benchmarkProcessSampler) Stop() benchmarkDuringMetrics {
	close(s.done)
	s.wg.Wait()
	s.recordSample()

	s.mu.Lock()
	defer s.mu.Unlock()

	avgCPU := 0.0
	if s.sampleCount > 0 {
		avgCPU = s.totalCPU / float64(s.sampleCount)
	}
	return benchmarkDuringMetrics{
		SampleInterval: s.interval.String(),
		SampleCount:    s.sampleCount,
		PeakRSSBytes:   s.peakRSS,
		PeakHeapAlloc:  s.peakHeapAlloc,
		PeakGoroutines: s.peakGoroutines,
		MaxCPUPercent:  s.maxCPUPercent,
		AvgCPUPercent:  avgCPU,
	}
}

func (s *benchmarkProcessSampler) recordSample() {
	info, err := s.process.MemoryInfo()
	if err != nil {
		return
	}
	cpuPercent, err := s.process.CPUPercent()
	if err != nil {
		cpuPercent = 0
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.sampleCount++
	s.totalCPU += cpuPercent
	if cpuPercent > s.maxCPUPercent {
		s.maxCPUPercent = cpuPercent
	}
	if info.RSS > s.peakRSS {
		s.peakRSS = info.RSS
	}
	if mem.HeapAlloc > s.peakHeapAlloc {
		s.peakHeapAlloc = mem.HeapAlloc
	}
	goroutines := runtime.NumGoroutine()
	if goroutines > s.peakGoroutines {
		s.peakGoroutines = goroutines
	}
}

func ensureBenchmarkCallbacks(tb testing.TB) {
	tb.Helper()

	benchmarkCallbackRegistration.Do(func() {
		dbConn := testCtx.DB()
		mustRegisterBenchmarkCallback(tb, dbConn.Callback().Query().Before("gorm:query").Register("benchmark-before-query", benchmarkBeforeCallback("query")), "benchmark-before-query")
		mustRegisterBenchmarkCallback(tb, dbConn.Callback().Query().After("gorm:query").Register("benchmark-after-query", benchmarkAfterCallback("query")), "benchmark-after-query")
		mustRegisterBenchmarkCallback(tb, dbConn.Callback().Raw().Before("gorm:raw").Register("benchmark-before-raw", benchmarkBeforeCallback("raw")), "benchmark-before-raw")
		mustRegisterBenchmarkCallback(tb, dbConn.Callback().Raw().After("gorm:raw").Register("benchmark-after-raw", benchmarkAfterCallback("raw")), "benchmark-after-raw")
		mustRegisterBenchmarkCallback(tb, dbConn.Callback().Create().Before("gorm:create").Register("benchmark-before-create", benchmarkBeforeCallback("create")), "benchmark-before-create")
		mustRegisterBenchmarkCallback(tb, dbConn.Callback().Create().After("gorm:create").Register("benchmark-after-create", benchmarkAfterCallback("create")), "benchmark-after-create")
		mustRegisterBenchmarkCallback(tb, dbConn.Callback().Update().Before("gorm:update").Register("benchmark-before-update", benchmarkBeforeCallback("update")), "benchmark-before-update")
		mustRegisterBenchmarkCallback(tb, dbConn.Callback().Update().After("gorm:update").Register("benchmark-after-update", benchmarkAfterCallback("update")), "benchmark-after-update")
		mustRegisterBenchmarkCallback(tb, dbConn.Callback().Delete().Before("gorm:delete").Register("benchmark-before-delete", benchmarkBeforeCallback("delete")), "benchmark-before-delete")
		mustRegisterBenchmarkCallback(tb, dbConn.Callback().Delete().After("gorm:delete").Register("benchmark-after-delete", benchmarkAfterCallback("delete")), "benchmark-after-delete")
		mustRegisterBenchmarkCallback(tb, dbConn.Callback().Row().Before("gorm:row").Register("benchmark-before-row", benchmarkBeforeCallback("row")), "benchmark-before-row")
		mustRegisterBenchmarkCallback(tb, dbConn.Callback().Row().After("gorm:row").Register("benchmark-after-row", benchmarkAfterCallback("row")), "benchmark-after-row")
	})
}

func mustRegisterBenchmarkCallback(tb testing.TB, err error, name string) {
	tb.Helper()
	if err != nil {
		tb.Fatalf("failed to register benchmark callback %s: %v", name, err)
	}
}

func benchmarkBeforeCallback(_ string) func(*gorm.DB) {
	return func(tx *gorm.DB) {
		collector := benchmarkCollectorFromContext(tx.Statement.Context)
		if collector == nil {
			return
		}
		tx.InstanceSet(benchmarkQueryStartKey, time.Now())
	}
}

func benchmarkAfterCallback(operation string) func(*gorm.DB) {
	return func(tx *gorm.DB) {
		collector := benchmarkCollectorFromContext(tx.Statement.Context)
		if collector == nil {
			return
		}

		startAny, ok := tx.InstanceGet(benchmarkQueryStartKey)
		if !ok {
			return
		}
		start, ok := startAny.(time.Time)
		if !ok {
			return
		}

		sql := tx.Statement.SQL.String()
		if sql == "" {
			return
		}
		collector.Record(operation, normalizeBenchmarkSQL(sql), tx.Statement.RowsAffected, time.Since(start))
	}
}

func benchmarkCollectorFromContext(ctx stdcontext.Context) *benchmarkQueryCollector {
	if ctx == nil {
		return nil
	}
	collector, _ := ctx.Value(benchmarkCollectorContextKey{}).(*benchmarkQueryCollector)
	return collector
}

func newBenchmarkQueryCollector() *benchmarkQueryCollector {
	return &benchmarkQueryCollector{
		byOperation: map[string]*benchmarkQueryStats{},
		statements:  map[string]*benchmarkStatementStats{},
	}
}

func (c *benchmarkQueryCollector) Record(operation, statement string, rowsAffected int64, elapsed time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.totalCount++
	c.totalTime += elapsed
	if elapsed >= time.Second {
		c.slowCount++
	}

	opStat := c.byOperation[operation]
	if opStat == nil {
		opStat = &benchmarkQueryStats{}
		c.byOperation[operation] = opStat
	}
	opStat.Count++
	opStat.RowsAffected += rowsAffected
	opStat.TotalDuration += elapsed

	stmt := c.statements[statement]
	if stmt == nil {
		stmt = &benchmarkStatementStats{Statement: statement}
		c.statements[statement] = stmt
	}
	stmt.Count++
	stmt.RowsAffected += rowsAffected
	stmt.TotalDuration += elapsed
}

func (c *benchmarkQueryCollector) Snapshot() benchmarkQueryMetrics {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := benchmarkQueryMetrics{
		Count:         c.totalCount,
		TotalDuration: c.totalTime,
		SlowCount:     c.slowCount,
		ByOperation:   make(map[string]benchmarkQueryStats, len(c.byOperation)),
	}
	for op, stats := range c.byOperation {
		out.ByOperation[op] = *stats
	}

	top := make([]benchmarkStatementStats, 0, len(c.statements))
	for _, stmt := range c.statements {
		top = append(top, *stmt)
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].TotalDuration == top[j].TotalDuration {
			return top[i].Count > top[j].Count
		}
		return top[i].TotalDuration > top[j].TotalDuration
	})
	if len(top) > 10 {
		top = top[:10]
	}
	out.TopStatements = top
	return out
}

var benchmarkWhitespaceRegexp = regexp.MustCompile(`\s+`)

func normalizeBenchmarkSQL(sql string) string {
	sql = strings.TrimSpace(sql)
	if sql == "" {
		return sql
	}
	return benchmarkWhitespaceRegexp.ReplaceAllString(sql, " ")
}

type benchmarkPGStatState struct {
	mode      string
	required  bool
	available bool
}

func preparePGStatStatements(tb testing.TB, mode string) benchmarkPGStatState {
	tb.Helper()

	state := benchmarkPGStatState{
		mode:     mode,
		required: mode == "required",
	}
	if mode == "off" {
		return state
	}
	available, err := hasPGStatStatements(testCtx)
	if err != nil {
		if state.required {
			tb.Fatalf("failed to check pg_stat_statements: %v", err)
		}
		return state
	}
	if state.required && !available {
		tb.Fatalf("pg_stat_statements is required but unavailable")
	}
	state.available = available
	return state
}

func hasPGStatStatements(ctx dutycontext.Context) (bool, error) {
	var available bool
	err := ctx.DB().Raw(`
		SELECT EXISTS (
			SELECT 1
			FROM pg_extension
			WHERE extname = 'pg_stat_statements'
		)
	`).Scan(&available).Error
	return available, err
}

func resetPGStatStatements(ctx dutycontext.Context) error {
	return ctx.DB().Exec("SELECT pg_stat_statements_reset()").Error
}

func capturePGStatStatements(ctx dutycontext.Context) (*benchmarkPGStatStatements, error) {
	type aggregate struct {
		Calls       int64   `gorm:"column:calls"`
		Rows        int64   `gorm:"column:rows"`
		TotalExecMS float64 `gorm:"column:total_exec_ms"`
		SharedHit   int64   `gorm:"column:shared_hit_blocks"`
		SharedRead  int64   `gorm:"column:shared_read_blocks"`
	}
	type row struct {
		Query       string  `gorm:"column:query"`
		Calls       int64   `gorm:"column:calls"`
		Rows        int64   `gorm:"column:rows"`
		TotalExecMS float64 `gorm:"column:total_exec_ms"`
		SharedHit   int64   `gorm:"column:shared_hit_blocks"`
		SharedRead  int64   `gorm:"column:shared_read_blocks"`
	}

	var total aggregate
	if err := ctx.DB().Raw(`
		SELECT
			COALESCE(SUM(calls), 0) AS calls,
			COALESCE(SUM(rows), 0) AS rows,
			COALESCE(SUM(total_exec_time), 0) AS total_exec_ms,
			COALESCE(SUM(shared_blks_hit), 0) AS shared_hit_blocks,
			COALESCE(SUM(shared_blks_read), 0) AS shared_read_blocks
		FROM pg_stat_statements
	`).Scan(&total).Error; err != nil {
		return nil, err
	}

	var topRows []row
	if err := ctx.DB().Raw(`
		SELECT
			query,
			calls,
			rows,
			total_exec_time AS total_exec_ms,
			shared_blks_hit AS shared_hit_blocks,
			shared_blks_read AS shared_read_blocks
		FROM pg_stat_statements
		ORDER BY total_exec_time DESC
		LIMIT 10
	`).Scan(&topRows).Error; err != nil {
		return nil, err
	}

	stats := &benchmarkPGStatStatements{
		Available:   true,
		Calls:       total.Calls,
		Rows:        total.Rows,
		TotalExecMS: total.TotalExecMS,
		SharedHit:   total.SharedHit,
		SharedRead:  total.SharedRead,
	}
	for _, row := range topRows {
		stats.TopStatements = append(stats.TopStatements, benchmarkPGStatementStat{
			Query:       normalizeBenchmarkSQL(row.Query),
			Calls:       row.Calls,
			Rows:        row.Rows,
			TotalExecMS: row.TotalExecMS,
			SharedHit:   row.SharedHit,
			SharedRead:  row.SharedRead,
		})
	}
	return stats, nil
}

func captureQueryLogSettings(tb testing.TB, mode string) benchmarkQueryLogSettings {
	tb.Helper()

	settings := benchmarkQueryLogSettings{
		Mode: mode,
	}
	if mode == "off" {
		return settings
	}
	rows := []struct {
		Name  string
		Value *string
	}{
		{Name: "log_statement", Value: &settings.LogStatement},
		{Name: "log_min_duration_statement", Value: &settings.LogMinDurationStatement},
		{Name: "log_destination", Value: &settings.LogDestination},
		{Name: "logging_collector", Value: &settings.LoggingCollector},
	}
	for _, row := range rows {
		if err := testCtx.DB().Raw("SHOW " + row.Name).Scan(row.Value).Error; err != nil {
			settings.Error = err.Error()
			if mode == "required" {
				tb.Fatalf("failed to read query log setting %s: %v", row.Name, err)
			}
			return settings
		}
	}

	settings.Available = true
	settings.Enabled =
		(strings.TrimSpace(settings.LogStatement) != "" && settings.LogStatement != "none") ||
			(strings.TrimSpace(settings.LogMinDurationStatement) != "" && settings.LogMinDurationStatement != "-1") ||
			strings.EqualFold(settings.LoggingCollector, "on")

	if mode == "required" && !settings.Enabled {
		tb.Fatalf("query log metrics are required but server logging is disabled")
	}
	return settings
}

func reportBenchmarkMetrics(b *testing.B, metrics benchmarkRunMetrics, elapsed time.Duration) {
	b.ReportMetric(float64(metrics.BeforeMemory.RSSBytes)/(1024*1024), "rss_before_mb")
	b.ReportMetric(float64(metrics.AfterMemory.RSSBytes)/(1024*1024), "rss_after_mb")
	b.ReportMetric(float64(metrics.During.PeakRSSBytes)/(1024*1024), "rss_peak_mb")
	b.ReportMetric(float64(metrics.BeforeMemory.HeapAlloc)/(1024*1024), "heap_before_mb")
	b.ReportMetric(float64(metrics.AfterMemory.HeapAlloc)/(1024*1024), "heap_after_mb")
	b.ReportMetric(float64(metrics.During.PeakHeapAlloc)/(1024*1024), "heap_peak_mb")
	b.ReportMetric(float64(metrics.During.PeakGoroutines), "goroutines_peak")
	b.ReportMetric(metrics.During.MaxCPUPercent, "cpu_peak_pct")
	b.ReportMetric(metrics.During.AvgCPUPercent, "cpu_avg_pct")
	b.ReportMetric(float64(metrics.QueryMetrics.Count), "db_queries")
	b.ReportMetric(float64(metrics.QueryMetrics.TotalDuration.Milliseconds()), "db_time_ms")
	b.ReportMetric(float64(metrics.QueryMetrics.SlowCount), "db_slow_queries")
	if metrics.PGStatStatements != nil {
		b.ReportMetric(float64(metrics.PGStatStatements.Calls), "pg_calls")
		b.ReportMetric(metrics.PGStatStatements.TotalExecMS, "pg_exec_ms")
	}
	if metrics.Snapshots != nil {
		configDelta := 0
		for _, counts := range metrics.Snapshots.Diff.PerConfigType {
			configDelta += counts.Total
		}
		b.ReportMetric(float64(configDelta), "snapshot_configs")
		b.ReportMetric(float64(metrics.Snapshots.Diff.ConfigAccess.Total), "snapshot_access")
		b.ReportMetric(float64(metrics.Snapshots.Diff.ConfigAccessLogs.Total), "snapshot_logs")
	}
	b.ReportMetric(float64(elapsed.Milliseconds()), "run_ms")
}

func aggregateBenchmarkMetrics(aggregate *benchmarkRunMetrics, run benchmarkRunMetrics) {
	aggregate.Iterations++
	aggregate.Preseed = run.Preseed
	aggregate.Scrape = run.Scrape
	aggregate.OverlapConfigRows = run.OverlapConfigRows

	aggregate.BeforeMemory.RSSBytes += run.BeforeMemory.RSSBytes
	aggregate.BeforeMemory.HeapAlloc += run.BeforeMemory.HeapAlloc
	aggregate.AfterMemory.RSSBytes += run.AfterMemory.RSSBytes
	aggregate.AfterMemory.HeapAlloc += run.AfterMemory.HeapAlloc

	aggregate.During.SampleCount += run.During.SampleCount
	if run.During.PeakRSSBytes > aggregate.During.PeakRSSBytes {
		aggregate.During.PeakRSSBytes = run.During.PeakRSSBytes
	}
	if run.During.PeakHeapAlloc > aggregate.During.PeakHeapAlloc {
		aggregate.During.PeakHeapAlloc = run.During.PeakHeapAlloc
	}
	if run.During.PeakGoroutines > aggregate.During.PeakGoroutines {
		aggregate.During.PeakGoroutines = run.During.PeakGoroutines
	}
	if run.During.MaxCPUPercent > aggregate.During.MaxCPUPercent {
		aggregate.During.MaxCPUPercent = run.During.MaxCPUPercent
	}
	aggregate.During.AvgCPUPercent += run.During.AvgCPUPercent
	aggregate.During.SampleInterval = run.During.SampleInterval

	aggregate.QueryMetrics.Count += run.QueryMetrics.Count
	aggregate.QueryMetrics.TotalDuration += run.QueryMetrics.TotalDuration
	aggregate.QueryMetrics.SlowCount += run.QueryMetrics.SlowCount
	if aggregate.QueryMetrics.ByOperation == nil {
		aggregate.QueryMetrics.ByOperation = map[string]benchmarkQueryStats{}
	}
	for op, stats := range run.QueryMetrics.ByOperation {
		existing := aggregate.QueryMetrics.ByOperation[op]
		existing.Count += stats.Count
		existing.RowsAffected += stats.RowsAffected
		existing.TotalDuration += stats.TotalDuration
		aggregate.QueryMetrics.ByOperation[op] = existing
	}

	aggregate.QueryLog = run.QueryLog
	aggregate.Summary = run.Summary
	aggregate.Snapshots = run.Snapshots
	if run.PGStatStatements != nil {
		if aggregate.PGStatStatements == nil {
			aggregate.PGStatStatements = &benchmarkPGStatStatements{Available: true}
		}
		aggregate.PGStatStatements.Calls += run.PGStatStatements.Calls
		aggregate.PGStatStatements.Rows += run.PGStatStatements.Rows
		aggregate.PGStatStatements.TotalExecMS += run.PGStatStatements.TotalExecMS
		aggregate.PGStatStatements.SharedHit += run.PGStatStatements.SharedHit
		aggregate.PGStatStatements.SharedRead += run.PGStatStatements.SharedRead
		aggregate.PGStatStatements.TopStatements = run.PGStatStatements.TopStatements
	}
	aggregate.QueryMetrics.TopStatements = run.QueryMetrics.TopStatements
}

func finalizeAggregateMetrics(aggregate *benchmarkRunMetrics, iterations int) {
	if iterations <= 0 {
		return
	}

	div := uint64(iterations)
	aggregate.BeforeMemory.RSSBytes /= div
	aggregate.BeforeMemory.HeapAlloc /= div
	aggregate.AfterMemory.RSSBytes /= div
	aggregate.AfterMemory.HeapAlloc /= div
	aggregate.During.AvgCPUPercent /= float64(iterations)
	aggregate.QueryMetrics.Count /= int64(iterations)
	aggregate.QueryMetrics.TotalDuration /= time.Duration(iterations)
	aggregate.QueryMetrics.SlowCount /= int64(iterations)
	for op, stats := range aggregate.QueryMetrics.ByOperation {
		stats.Count /= int64(iterations)
		stats.RowsAffected /= int64(iterations)
		stats.TotalDuration /= time.Duration(iterations)
		aggregate.QueryMetrics.ByOperation[op] = stats
	}
	if aggregate.PGStatStatements != nil {
		aggregate.PGStatStatements.Calls /= int64(iterations)
		aggregate.PGStatStatements.Rows /= int64(iterations)
		aggregate.PGStatStatements.TotalExecMS /= float64(iterations)
		aggregate.PGStatStatements.SharedHit /= int64(iterations)
		aggregate.PGStatStatements.SharedRead /= int64(iterations)
	}
}

func writeBenchmarkReport(tb testing.TB, scenario benchmarkScenario, aggregate, last benchmarkRunMetrics) {
	tb.Helper()

	reportDir := strings.TrimSpace(os.Getenv("CONFIG_DB_BENCH_REPORT_DIR"))
	if reportDir == "" {
		reportDir = filepath.Join(os.TempDir(), "config-db-bench-reports")
	}
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		tb.Fatalf("failed to create benchmark report dir: %v", err)
	}

	payload := map[string]any{
		"scenario":      scenario,
		"aggregate_run": aggregate,
		"last_run":      last,
		"generated_at":  time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		tb.Fatalf("failed to marshal benchmark report: %v", err)
	}

	path := filepath.Join(reportDir, scenario.Name+".json")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		tb.Fatalf("failed to write benchmark report: %v", err)
	}
}
