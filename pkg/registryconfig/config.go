// Copyright (c) 2026 Meizon Inc.
//
// Permission to use, copy, modify, and/or distribute this software for any
// purpose with or without fee is hereby granted, provided that the above
// copyright notice and this permission notice appear in all copies.
//
// THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH
// REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY
// AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT,
// INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM
// LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR
// OTHER TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR
// PERFORMANCE OF THIS SOFTWARE.

// Package registryconfig defines the on-disk configuration for registryd. The
// bootstrap binary renders REGISTRYD_* environment variables into this shape as
// YAML; the unit framework loads the "unit" and "registryd" sections. JSON tags
// are kebab-case so the emitted YAML keys are kebab-case too.
package registryconfig

// FullConfig is the root config document.
type FullConfig struct {
	Unit      UnitConfig `json:"unit"`
	Registryd Config     `json:"registryd"`
}

// UnitConfig mirrors the fields the unit framework consumes.
type UnitConfig struct {
	Metrics MetricsConfig `json:"metrics"`
}

// MetricsConfig is the Prometheus metrics listener.
type MetricsConfig struct {
	Addr string `json:"addr"`
}

// Config is the registryd application section (impl.cfg).
type Config struct {
	BaseURL       string   `json:"base-url"`
	EncryptionKey string   `json:"encryption-key"`
	SuperAdmins   []string `json:"super-admins"`
	AdminToken    string   `json:"admin-token"`

	Pg   PgConfig   `json:"pg"`
	API  APIConfig  `json:"api"`
	Auth AuthConfig `json:"auth"`
}

// PgConfig configures the PostgreSQL connection pool.
type PgConfig struct {
	Addr        string `json:"addr"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Database    string `json:"database"`
	PoolSize    int    `json:"pool-size"`
	MinPoolSize int    `json:"min-pool-size"`
}

// APIConfig configures the HTTP API listener and rate limiting.
type APIConfig struct {
	Addr           string   `json:"addr"`
	RateLimitRPM   int      `json:"rate-limit-rpm"`
	RateLimitBurst int      `json:"rate-limit-burst"`
	CookieSecure   bool     `json:"cookie-secure"`
	ConsoleOrigins []string `json:"console-origins"`
}

// AuthConfig configures password hashing and the session cookie (the cookie
// secret is reserved for the console phase).
type AuthConfig struct {
	PasswordPepper     string `json:"password-pepper"`
	PasswordIterations int    `json:"password-iterations"`
	CookieSecret       string `json:"cookie-secret"`
}

// Defaults returns a Config populated with production-sane defaults, overridden
// by the loaded file.
func Defaults() Config {
	return Config{
		BaseURL: "http://localhost:8080",
		Pg: PgConfig{
			Addr:        "localhost:5432",
			Username:    "registryd",
			Password:    "registryd",
			Database:    "registryd",
			PoolSize:    20,
			MinPoolSize: 2,
		},
		API: APIConfig{
			Addr:           "localhost:8080",
			RateLimitRPM:   600,
			RateLimitBurst: 60,
		},
		Auth: AuthConfig{
			PasswordIterations: 600000,
		},
	}
}
