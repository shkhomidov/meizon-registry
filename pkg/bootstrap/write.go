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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"go.meizon.cloud/registry/pkg/registryconfig"
	"sigs.k8s.io/yaml"
)

// WriteConfig renders cfg to path in "yaml" or "json" format with 0600
// permissions, creating parent directories as needed.
func WriteConfig(cfg *registryconfig.FullConfig, path, format string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("cannot create config directory: %w", err)
		}
	}

	var (
		data []byte
		err  error
	)
	switch format {
	case "json":
		data, err = json.MarshalIndent(cfg, "", "  ")
	case "yaml", "":
		data, err = yaml.Marshal(cfg)
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
	if err != nil {
		return fmt.Errorf("cannot marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("cannot write config: %w", err)
	}

	return nil
}
