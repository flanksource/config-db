package api

import (
	"context"
	"errors"
	"fmt"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty"
	dutyCtx "github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type ScrapeContext interface {
	duty.DBContext
	DutyContext() dutyCtx.Context

	IsTrace() bool

	WithContext(ctx context.Context) ScrapeContext

	WithScrapeConfig(scraper *v1.ScrapeConfig) ScrapeContext
	ScrapeConfig() *v1.ScrapeConfig

	Namespace() string
	Kubernetes() kubernetes.Interface
	KubernetesRestConfig() *rest.Config

	GetEnvVarValue(input types.EnvVar) (string, error)
	GetEnvValueFromCache(env types.EnvVar) (string, error)

	HydrateConnection(connectionIdentifier string) (*models.Connection, error)
	HydrateConnectionModel(models.Connection) (*models.Connection, error)
}

type scrapeContext struct {
	context.Context

	db   *gorm.DB
	pool *pgxpool.Pool

	namespace            string
	kubernetes           *kubernetes.Clientset
	kubernetesRestConfig *rest.Config

	scrapeConfig *v1.ScrapeConfig
}

func NewScrapeContext(ctx context.Context, db *gorm.DB, pool *pgxpool.Pool) ScrapeContext {
	return &scrapeContext{
		Context:              ctx,
		namespace:            Namespace,
		kubernetes:           KubernetesClient,
		kubernetesRestConfig: KubernetesRestConfig,
		db:                   db,
		pool:                 pool,
	}
}

func (ctx scrapeContext) WithContext(from context.Context) ScrapeContext {
	ctx.Context = from
	return &ctx
}

func (ctx scrapeContext) WithScrapeConfig(scraper *v1.ScrapeConfig) ScrapeContext {
	ctx.scrapeConfig = scraper
	return &ctx
}

func (ctx scrapeContext) DutyContext() dutyCtx.Context {
	return dutyCtx.NewContext(ctx).
		WithKubernetes(ctx.kubernetes).
		WithDB(ctx.db, ctx.pool).
		WithNamespace(ctx.namespace)
}

func (ctx scrapeContext) DB() *gorm.DB {
	return ctx.db
}

func (ctx scrapeContext) Pool() *pgxpool.Pool {
	return ctx.pool
}

func (ctx scrapeContext) ScrapeConfig() *v1.ScrapeConfig {
	return ctx.scrapeConfig
}

func (ctx scrapeContext) Namespace() string {
	return ctx.namespace
}

func (c scrapeContext) Kubernetes() kubernetes.Interface {
	return c.kubernetes
}

func (c scrapeContext) KubernetesRestConfig() *rest.Config {
	return c.kubernetesRestConfig
}

func (ctx scrapeContext) IsTrace() bool {
	return ctx.scrapeConfig.Spec.IsTrace()
}

func (ctx *scrapeContext) HydrateConnection(connectionName string) (*models.Connection, error) {
	if connectionName == "" {
		return nil, nil
	}

	if ctx.db == nil {
		return nil, errors.New("db has not been initialized")
	}

	if ctx.pool == nil {
		return nil, errors.New("pool has not been initialized")
	}

	if ctx.kubernetes == nil {
		return nil, errors.New("kubernetes clientset has not been initialized")
	}

	connection, err := dutyCtx.HydrateConnectionByURL(ctx.DutyContext(), connectionName)
	if err != nil {
		return nil, err
	}

	// Connection name was explicitly provided but was not found.
	// That's an error.
	if connection == nil {
		return nil, fmt.Errorf("connection %s not found", connectionName)
	}

	return connection, nil
}

func (ctx *scrapeContext) HydrateConnectionModel(c models.Connection) (*models.Connection, error) {
	if ctx.db == nil {
		return nil, errors.New("db has not been initialized")
	}

	if ctx.pool == nil {
		return nil, errors.New("pool has not been initialized")
	}

	if ctx.kubernetes == nil {
		return nil, errors.New("kubernetes clientset has not been initialized")
	}

	connection, err := dutyCtx.HydrateConnection(ctx.DutyContext(), &c)
	if err != nil {
		return nil, err
	}

	// Connection name was explicitly provided but was not found.
	// That's an error.
	if connection == nil {
		return nil, fmt.Errorf("connection %s not found", c.Name)
	}

	return connection, nil
}

func (c *scrapeContext) GetEnvVarValue(input types.EnvVar) (string, error) {
	return duty.GetEnvValueFromCache(c.kubernetes, input, c.namespace)
}

func (ctx *scrapeContext) GetEnvValueFromCache(env types.EnvVar) (string, error) {
	return duty.GetEnvValueFromCache(ctx.kubernetes, env, ctx.namespace)
}
