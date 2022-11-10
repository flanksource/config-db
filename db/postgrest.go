package db

import (
	"strconv"

	"github.com/flanksource/commons/deps"
	"github.com/flanksource/commons/logger"
)

// PostgRESTVersion ...
var PostgRESTVersion = "v9.0.0"

var PostgRESTServerPort = 3000

func PostgRESTEndpoint() string {
	return "http://localhost:" + strconv.Itoa(PostgRESTServerPort)
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
		"PGRST_LOG_LEVEL":                LogLevel,
	})
}

// StartPostgrest ...
func StartPostgrest() {
	if err := getBinary()(""); err != nil {
		logger.Errorf("Failed to start postgREST: %v", err)
	}
}
