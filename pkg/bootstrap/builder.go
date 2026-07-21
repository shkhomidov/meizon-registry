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

package bootstrap

import (
	"fmt"
	"strings"

	"go.meizon.cloud/registry/pkg/registryconfig"
)

// Builder assembles a FullConfig from REGISTRYD_* environment variables.
type Builder struct {
	resolver *Resolver
}

// NewBuilder returns a builder using the given resolver.
func NewBuilder(resolver *Resolver) *Builder {
	return &Builder{resolver: resolver}
}

// Build reads the environment and returns the rendered configuration. Required
// secrets are validated; a missing secret is a hard error.
func (b *Builder) Build() (*registryconfig.FullConfig, error) {
	r := b.resolver
	defaults := registryconfig.Defaults()

	cfg := &registryconfig.FullConfig{
		Unit: registryconfig.UnitConfig{
			Metrics: registryconfig.MetricsConfig{
				Addr: r.getEnvOrDefault("REGISTRYD_METRICS_ADDR", "localhost:8081"),
			},
		},
		Registryd: registryconfig.Config{
			BaseURL:       r.getEnvOrDefault("REGISTRYD_BASE_URL", defaults.BaseURL),
			EncryptionKey: r.getEnv("REGISTRYD_ENCRYPTION_KEY"),
			SuperAdmins:   splitList(r.getEnvOrDefault("REGISTRYD_SUPER_ADMINS", "")),
			AdminToken:    r.getEnvOrDefault("REGISTRYD_ADMIN_TOKEN", ""),
			Pg: registryconfig.PgConfig{
				Addr:        r.getEnvOrDefault("REGISTRYD_PG_ADDR", defaults.Pg.Addr),
				Username:    r.getEnvOrDefault("REGISTRYD_PG_USERNAME", defaults.Pg.Username),
				Password:    r.getEnvOrDefault("REGISTRYD_PG_PASSWORD", defaults.Pg.Password),
				Database:    r.getEnvOrDefault("REGISTRYD_PG_DATABASE", defaults.Pg.Database),
				PoolSize:    r.getEnvIntOrDefault("REGISTRYD_PG_POOL_SIZE", defaults.Pg.PoolSize),
				MinPoolSize: r.getEnvIntOrDefault("REGISTRYD_PG_MIN_POOL_SIZE", defaults.Pg.MinPoolSize),
			},
			API: registryconfig.APIConfig{
				Addr:           r.getEnvOrDefault("REGISTRYD_API_ADDR", defaults.API.Addr),
				RateLimitRPM:   r.getEnvIntOrDefault("REGISTRYD_API_RATE_LIMIT_RPM", defaults.API.RateLimitRPM),
				RateLimitBurst: r.getEnvIntOrDefault("REGISTRYD_API_RATE_LIMIT_BURST", defaults.API.RateLimitBurst),
				CookieSecure:   r.getEnvOrDefault("REGISTRYD_API_COOKIE_SECURE", "false") == "true",
				ConsoleOrigins: splitList(r.getEnvOrDefault("REGISTRYD_CONSOLE_ORIGINS", "")),
			},
			Auth: registryconfig.AuthConfig{
				PasswordPepper:     r.getEnv("REGISTRYD_AUTH_PASSWORD_PEPPER"),
				PasswordIterations: r.getEnvIntOrDefault("REGISTRYD_AUTH_PASSWORD_ITERATIONS", defaults.Auth.PasswordIterations),
				CookieSecret:       r.getEnv("REGISTRYD_AUTH_COOKIE_SECRET"),
			},
		},
	}

	if err := r.Err(); err != nil {
		return nil, err
	}

	if err := validateRequired(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func validateRequired(cfg *registryconfig.FullConfig) error {
	var missing []string
	if cfg.Registryd.EncryptionKey == "" {
		missing = append(missing, "REGISTRYD_ENCRYPTION_KEY")
	}
	if cfg.Registryd.Auth.PasswordPepper == "" {
		missing = append(missing, "REGISTRYD_AUTH_PASSWORD_PEPPER")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}
	return nil
}

func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
