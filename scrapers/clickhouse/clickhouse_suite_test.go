//go:build integration

package clickhouse

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestClickhouse(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ClickHouse Scraper Suite")
}
