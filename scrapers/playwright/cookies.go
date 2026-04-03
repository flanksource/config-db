package playwright

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/connection"
)

type playwrightCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	HTTPOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
	SameSite string  `json:"sameSite"`
}

type playwrightStorageState struct {
	Cookies []playwrightCookie `json:"cookies"`
	Origins []any              `json:"origins"`
}

func loginWithBrowser(ctx api.ScrapeContext, login v1.PlaywrightBrowserLogin) (string, error) {
	conn, err := connection.Get(ctx.Context, login.ConnectionName)
	if err != nil {
		return "", fmt.Errorf("getting connection %s: %w", login.ConnectionName, err)
	}

	if storageState, ok := conn.Properties["storageState"]; ok && storageState != "" {
		ctx.Logger.V(2).Infof("using storageState from connection %s", login.ConnectionName)
		return writeStorageState([]byte(storageState))
	}

	if headers, ok := conn.Properties["headers"]; ok && headers != "" {
		ctx.Logger.V(2).Infof("building storageState from headers in connection %s", login.ConnectionName)
		return buildStorageStateFromHeaders(headers, conn.URL)
	}

	return "", fmt.Errorf("connection %s has no storageState or headers property", login.ConnectionName)
}

func writeStorageState(data []byte) (string, error) {
	f, err := os.CreateTemp("", "playwright-storage-*.json")
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		os.Remove(f.Name()) //nolint:errcheck
		return "", err
	}
	return f.Name(), nil
}

func buildStorageStateFromHeaders(headers string, connURL string) (string, error) {
	domain := ""
	if connURL != "" {
		if u, err := url.Parse(connURL); err == nil {
			domain = u.Hostname()
		}
	}

	var cookies []playwrightCookie
	for _, line := range strings.Split(headers, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		if !strings.EqualFold(name, "Cookie") {
			continue
		}

		for _, pair := range strings.Split(value, ";") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			kv := strings.SplitN(pair, "=", 2)
			if len(kv) != 2 {
				continue
			}
			cookies = append(cookies, playwrightCookie{
				Name:     strings.TrimSpace(kv[0]),
				Value:    strings.TrimSpace(kv[1]),
				Domain:   domain,
				Path:     "/",
				Expires:  -1,
				SameSite: "Lax",
			})
		}
	}

	state := playwrightStorageState{
		Cookies: cookies,
		Origins: []any{},
	}

	data, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	return writeStorageState(data)
}
