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

package root

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"go.meizon.cloud/registry/pkg/distclient"
	"go.meizon.cloud/registry/pkg/fwschema"
)

// newPullCmd is the reference GRC-side consumer: discover, download, verify and
// emit import-ready seeds. It does not need database access, so it is registered
// directly (no Factory).
func newPullCmd() *cobra.Command {
	var registryURL, token, region, outDir string
	var pubkeys []string

	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Sync frameworks from a registry, verifying every signature (GRC-side consumer)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			keys, err := parsePinnedKeys(pubkeys)
			if err != nil {
				return err
			}
			if len(keys) == 0 {
				return fmt.Errorf("at least one --pubkey keyId:base64 is required to verify bundles")
			}

			client := distclient.New(registryURL, token, keys)
			result, err := distclient.Sync(cmd.Context(), client, region, outDir)
			if err != nil {
				return err
			}

			for _, s := range result.Synced {
				fmt.Printf("verified %-24s %-8s controls=%-3d -> %s\n", s.ID, s.Version, s.Controls, s.SeedPath)
			}
			for _, fl := range result.Failures {
				fmt.Fprintf(os.Stderr, "REJECTED %s: %s\n", fl.ID, fl.Error)
			}
			fmt.Printf("synced %d, rejected %d\n", len(result.Synced), len(result.Failures))
			if len(result.Failures) > 0 {
				return fmt.Errorf("%d framework(s) failed verification", len(result.Failures))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&registryURL, "url", "", "registry base URL, e.g. https://registry.example.com (required)")
	cmd.Flags().StringVar(&token, "token", "", "distribution bearer token (required)")
	cmd.Flags().StringVar(&region, "region", "", "restrict to a region")
	cmd.Flags().StringVar(&outDir, "out-dir", "./synced-frameworks", "directory for verified seeds and bundles")
	cmd.Flags().StringArrayVar(&pubkeys, "pubkey", nil, "pinned key as keyId:base64 (repeatable, required)")
	_ = cmd.MarkFlagRequired("url")
	_ = cmd.MarkFlagRequired("token")
	return cmd
}

// newVerifyCmd verifies a local .mzfw.json bundle against pinned keys, offline —
// the air-gapped import check.
func newVerifyCmd() *cobra.Command {
	var file string
	var pubkeys []string

	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify a signed bundle file against pinned keys (air-gapped import check)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			keys, err := parsePinnedKeys(pubkeys)
			if err != nil {
				return err
			}

			raw, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("cannot read bundle: %w", err)
			}
			var bundle fwschema.Framework
			if err := json.Unmarshal(raw, &bundle); err != nil {
				return fmt.Errorf("cannot parse bundle: %w", err)
			}

			if err := bundle.Verify(keys); err != nil {
				return fmt.Errorf("verification FAILED: %w", err)
			}
			if err := bundle.Validate(); err != nil {
				return fmt.Errorf("schema validation FAILED: %w", err)
			}

			fmt.Printf("OK: %s@%s verified, %d controls, key=%s\n", bundle.ID, bundle.Version, len(bundle.Controls), bundle.Signature.KeyID)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "path to a .mzfw.json bundle (required)")
	cmd.Flags().StringArrayVar(&pubkeys, "pubkey", nil, "pinned key as keyId:base64 (repeatable, required)")
	_ = cmd.MarkFlagRequired("file")
	_ = cmd.MarkFlagRequired("pubkey")
	return cmd
}

// parsePinnedKeys parses repeated "keyId:base64" pairs into a key set.
func parsePinnedKeys(pairs []string) (map[string]ed25519.PublicKey, error) {
	keys := make(map[string]ed25519.PublicKey, len(pairs))
	for _, p := range pairs {
		id, b64, ok := strings.Cut(p, ":")
		if !ok || id == "" || b64 == "" {
			return nil, fmt.Errorf("invalid --pubkey %q; expected keyId:base64", p)
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 for key %q: %w", id, err)
		}
		if len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("key %q is not a 32-byte ed25519 public key", id)
		}
		keys[id] = ed25519.PublicKey(raw)
	}
	return keys, nil
}
