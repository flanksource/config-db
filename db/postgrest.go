package db

import (
	"fmt"
	"strconv"

	"github.com/flanksource/commons/deps"
	"github.com/flanksource/commons/logger"
)

var (
	PostgRESTVersion         = "v10.0.0"
	PostgRESTServerPort      = 3000
	PostgRESTAdminServerPort = 3001
)

func PostgRESTEndpoint() string {
	return fmt.Sprintf("http://localhost:%d", PostgRESTServerPort)
}

func PostgRESTAdminEndpoint() string {
	return fmt.Sprintf("http://localhost:%d", PostgRESTAdminServerPort)
}

// GoOffline ...
func GoOffline() error {
	return getBinary()("--help")
}

func getBinary() deps.BinaryFunc {
	return deps.BinaryWithEnv("postgREST", PostgRESTVersion, ".bin", map[string]string{
		"PGRST_DB_URI":                   ConnectionString,
		"PGRST_DB_SCHEMA":                Schema,
		"PGRST_DB_ANON_ROLE":             "postgrest_api",
		"PGRST_OPENAPI_SERVER_PROXY_URI": HTTPEndpoint,
		"PGRST_DB_PORT":                  strconv.Itoa(PostgRESTServerPort),
		"PGRST_LOG_LEVEL":                PGRSTLogLevel,
		"PGRST_ADMIN_SERVER_PORT":        strconv.Itoa(PostgRESTAdminServerPort),
	})
}

// StartPostgrest ...
func StartPostgrest() {
	if err := getBinary()(""); err != nil {
		logger.Errorf("Failed to start postgREST: %v", err)
	}
}
