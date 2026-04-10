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

type browserLoginResult struct {
	StorageStatePath   string
	SessionStoragePath string
}

func loginWithBrowser(ctx api.ScrapeContext, login v1.PlaywrightBrowserLogin) (*browserLoginResult, error) {
	conn, err := connection.Get(ctx.Context, login.ConnectionName)
	if err != nil {
		return nil, fmt.Errorf("getting connection %s: %w", login.ConnectionName, err)
	}

	result := &browserLoginResult{}

	if storageState, ok := conn.Properties["storageState"]; ok && storageState != "" {
		logStorageStateSummary(ctx, login.ConnectionName, []byte(storageState))
		path, err := writeStorageState([]byte(storageState))
		if err != nil {
			return nil, err
		}
		result.StorageStatePath = path
	} else if headers, ok := conn.Properties["headers"]; ok && headers != "" {
		ctx.Logger.V(2).Infof("building storageState from headers in connection %s", login.ConnectionName)
		path, err := buildStorageStateFromHeaders(headers, conn.URL)
		if err != nil {
			return nil, err
		}
		result.StorageStatePath = path
	} else {
		return nil, fmt.Errorf("connection %s has no storageState or headers property", login.ConnectionName)
	}

	if sessionStorage, ok := conn.Properties["sessionStorage"]; ok && sessionStorage != "" {
		path, err := writeSessionStorage([]byte(sessionStorage))
		if err != nil {
			ctx.Logger.Errorf("failed to write sessionStorage from connection %s: %v", login.ConnectionName, err)
		} else {
			result.SessionStoragePath = path
			logSessionStorageSummary(ctx, login.ConnectionName, []byte(sessionStorage))
		}
	}

	return result, nil
}

func writeSessionStorage(data []byte) (string, error) {
	f, err := os.CreateTemp("", "playwright-session-*.json")
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck
	if _, err := f.Write(data); err != nil {
		os.Remove(f.Name()) //nolint:errcheck
		return "", err
	}
	return f.Name(), nil
}

func logSessionStorageSummary(ctx api.ScrapeContext, connName string, data []byte) {
	var parsed struct {
		Origin string            `json:"origin"`
		Items  map[string]string `json:"items"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		ctx.Logger.Errorf("sessionStorage from connection %s is not valid JSON (%d bytes): %v", connName, len(data), err)
		return
	}
	ctx.Logger.Infof("sessionStorage from connection %s: origin=%s, %d items", connName, parsed.Origin, len(parsed.Items))
}

func logStorageStateSummary(ctx api.ScrapeContext, connName string, data []byte) {
	var parsed struct {
		Cookies []struct {
			Name   string `json:"name"`
			Domain string `json:"domain"`
		} `json:"cookies"`
		Origins []struct {
			Origin       string `json:"origin"`
			LocalStorage []any  `json:"localStorage"`
		} `json:"origins"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		ctx.Logger.Errorf("storageState from connection %s is not valid JSON (%d bytes): %v", connName, len(data), err)
		return
	}

	domains := map[string]int{}
	for _, c := range parsed.Cookies {
		domains[c.Domain]++
	}
	ctx.Logger.Infof("storageState from connection %s: %d bytes, %d cookies across %d domains, %d origins",
		connName, len(data), len(parsed.Cookies), len(domains), len(parsed.Origins))
	for d, n := range domains {
		ctx.Logger.Infof("  cookie domain %s: %d", d, n)
	}
	for _, o := range parsed.Origins {
		ctx.Logger.Infof("  origin %s: %d localStorage items", o.Origin, len(o.LocalStorage))
	}
}

func writeStorageState(data []byte) (string, error) {
	f, err := os.CreateTemp("", "playwright-storage-*.json")
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck

	if _, err := f.Write(data); err != nil {
		os.Remove(f.Name()) //nolint:errcheck
		return "", err
	}
	return f.Name(), nil
}

func buildStorageStateFromHeaders(headers string, connURL string) (string, error) {
	domain := ""
	if connURL != "" {
		u, err := url.Parse(connURL)
		if err != nil {
			return "", fmt.Errorf("parsing connection URL %q: %w", connURL, err)
		}
		domain = u.Hostname()
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

	if len(cookies) == 0 {
		return "", fmt.Errorf("no cookies found in headers")
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
