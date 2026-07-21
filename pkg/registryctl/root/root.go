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

// Package root assembles the registryctl command tree. registryctl drives the
// authoring and governance lifecycle directly against the database, mirroring
// how proboctl operates the GRC. Actions that require an actor take --actor
// (an email) so separation of duties and region scoping are enforced exactly as
// they would be through the API.
package root

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/gid"
	"go.meizon.cloud/registry/pkg/registry"
	"go.meizon.cloud/registry/pkg/registryctl/cmdutil"
)

// NewCmdRoot builds the root command.
func NewCmdRoot(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "registryctl <command> [flags]",
		Short:         "Operate the Meizon Framework Registry",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.PersistentFlags().StringVar(&f.PgAddr, "pg-addr", envOr("REGISTRYD_PG_ADDR", "localhost:5432"), "postgres address")
	cmd.PersistentFlags().StringVar(&f.PgUser, "pg-user", envOr("REGISTRYD_PG_USERNAME", "registryd"), "postgres user")
	cmd.PersistentFlags().StringVar(&f.PgPassword, "pg-password", envOr("REGISTRYD_PG_PASSWORD", "registryd"), "postgres password")
	cmd.PersistentFlags().StringVar(&f.PgDatabase, "pg-database", envOr("REGISTRYD_PG_DATABASE", "registryd"), "postgres database")

	cmd.AddCommand(newSuperAdminCmd(f))
	cmd.AddCommand(newUserCmd(f))
	cmd.AddCommand(newRoleCmd(f))
	cmd.AddCommand(newFrameworkCmd(f))
	cmd.AddCommand(newBundleCmd(f))
	cmd.AddCommand(newSeedCmd(f))
	cmd.AddCommand(newTokenCmd(f))
	cmd.AddCommand(newSigningKeyCmd(f))
	cmd.AddCommand(newAuditCmd(f))

	// Consumer-side commands (no database access needed).
	cmd.AddCommand(newPullCmd())
	cmd.AddCommand(newVerifyCmd())

	return cmd
}

func newSuperAdminCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{Use: "superadmin", Short: "Superadmin bootstrap"}

	var email, name, password string
	boot := &cobra.Command{
		Use:   "bootstrap",
		Short: "Create or promote a superadmin (must be in REGISTRYD_SUPER_ADMINS)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := f.Service(cmd.Context())
			if err != nil {
				return err
			}
			id, err := svc.BootstrapSuperAdmin(cmd.Context(), registry.CreateIdentityRequest{Email: email, FullName: name, Password: password})
			if err != nil {
				return err
			}
			fmt.Printf("superadmin ready: %s (%s)\n", email, id)
			return nil
		},
	}
	boot.Flags().StringVar(&email, "email", "", "email (required)")
	boot.Flags().StringVar(&name, "name", "", "full name (required)")
	boot.Flags().StringVar(&password, "password", "", "password (required)")
	_ = boot.MarkFlagRequired("email")
	_ = boot.MarkFlagRequired("name")
	_ = boot.MarkFlagRequired("password")

	cmd.AddCommand(boot)
	return cmd
}

func newUserCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{Use: "user", Short: "Manage users"}

	var actor, email, name, password, role, regions string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a user and assign a role (superadmin actor)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := f.Service(cmd.Context())
			if err != nil {
				return err
			}
			actorID, err := svc.IdentityIDByEmail(cmd.Context(), actor)
			if err != nil {
				return fmt.Errorf("cannot resolve actor %q: %w", actor, err)
			}
			id, err := svc.CreateUser(cmd.Context(), actorID,
				registry.CreateIdentityRequest{Email: email, FullName: name, Password: password},
				role, splitRegions(regions))
			if err != nil {
				return err
			}
			fmt.Printf("created user %s (%s) role=%s regions=%s\n", email, id, role, regions)
			return nil
		},
	}
	create.Flags().StringVar(&actor, "actor", "", "email of the acting superadmin (required)")
	create.Flags().StringVar(&email, "email", "", "new user email (required)")
	create.Flags().StringVar(&name, "name", "", "new user full name (required)")
	create.Flags().StringVar(&password, "password", "", "new user password (required)")
	create.Flags().StringVar(&role, "role", "", "role: superadmin|moderator|auditor (required)")
	create.Flags().StringVar(&regions, "regions", "", "comma-separated regions (e.g. EU,US)")
	for _, r := range []string{"actor", "email", "name", "password", "role"} {
		_ = create.MarkFlagRequired(r)
	}

	cmd.AddCommand(create)
	return cmd
}

func newRoleCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{Use: "role", Short: "Manage roles"}

	var actor, email, role, regions string
	assign := &cobra.Command{
		Use:   "assign",
		Short: "Assign a role and regions to an existing user (superadmin actor)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := f.Service(cmd.Context())
			if err != nil {
				return err
			}
			actorID, err := svc.IdentityIDByEmail(cmd.Context(), actor)
			if err != nil {
				return fmt.Errorf("cannot resolve actor %q: %w", actor, err)
			}
			if err := svc.AssignRole(cmd.Context(), actorID, email, role, splitRegions(regions)); err != nil {
				return err
			}
			fmt.Printf("assigned %s role=%s regions=%s\n", email, role, regions)
			return nil
		},
	}
	assign.Flags().StringVar(&actor, "actor", "", "email of the acting superadmin (required)")
	assign.Flags().StringVar(&email, "email", "", "target user email (required)")
	assign.Flags().StringVar(&role, "role", "", "role: superadmin|moderator|auditor (required)")
	assign.Flags().StringVar(&regions, "regions", "", "comma-separated regions")
	for _, r := range []string{"actor", "email", "role"} {
		_ = assign.MarkFlagRequired(r)
	}

	cmd.AddCommand(assign)
	return cmd
}

func newFrameworkCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{Use: "framework", Short: "Author and drive frameworks"}
	cmd.AddCommand(newFrameworkCreateCmd(f))
	cmd.AddCommand(newFrameworkAddControlCmd(f))
	cmd.AddCommand(newFrameworkTransitionCmd(f, "submit", "Submit the latest version for review", transitionSubmit))
	cmd.AddCommand(newFrameworkTransitionCmd(f, "approve", "Approve the latest version", transitionApprove))
	cmd.AddCommand(newFrameworkTransitionCmd(f, "publish", "Publish the latest version", transitionPublish))
	cmd.AddCommand(newFrameworkListCmd(f))
	return cmd
}

func newFrameworkCreateCmd(f *cmdutil.Factory) *cobra.Command {
	var actor string
	var req registry.CreateFrameworkRequest
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a framework and open its first draft",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := f.Service(cmd.Context())
			if err != nil {
				return err
			}
			actorID, err := svc.IdentityIDByEmail(cmd.Context(), actor)
			if err != nil {
				return fmt.Errorf("cannot resolve actor %q: %w", actor, err)
			}
			out, err := svc.CreateFramework(cmd.Context(), actorID, req)
			if err != nil {
				return err
			}
			fmt.Printf("created framework %s (%s) draft %s (%s)\n", req.ReferenceID, out.FrameworkID, out.Version, out.VersionID)
			return nil
		},
	}
	cmd.Flags().StringVar(&actor, "actor", "", "email of the authoring user (required)")
	cmd.Flags().StringVar(&req.ReferenceID, "reference", "", "reference id, e.g. nist-800-171-r2 (required)")
	cmd.Flags().StringVar(&req.Name, "name", "", "framework name (required)")
	cmd.Flags().StringVar(&req.ShortName, "short-name", "", "short name")
	cmd.Flags().StringVar(&req.Region, "region", "", "region, e.g. EU|US|APAC (required)")
	cmd.Flags().StringVar(&req.Authority, "authority", "", "issuing authority")
	cmd.Flags().StringVar(&req.License, "license", "public-domain", "license: public-domain|statutory|proprietary")
	cmd.Flags().StringVar(&req.Description, "description", "", "description")
	for _, r := range []string{"actor", "reference", "name", "region"} {
		_ = cmd.MarkFlagRequired(r)
	}
	return cmd
}

func newFrameworkAddControlCmd(f *cmdutil.Factory) *cobra.Command {
	var actor, framework string
	var req registry.AddControlRequest
	cmd := &cobra.Command{
		Use:   "add-control",
		Short: "Add a control to the latest draft version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := f.Service(cmd.Context())
			if err != nil {
				return err
			}
			actorID, err := svc.IdentityIDByEmail(cmd.Context(), actor)
			if err != nil {
				return fmt.Errorf("cannot resolve actor %q: %w", actor, err)
			}
			versionID, err := resolveLatestVersion(cmd, svc, framework)
			if err != nil {
				return err
			}
			req.VersionID = versionID
			id, err := svc.AddControl(cmd.Context(), actorID, req)
			if err != nil {
				return err
			}
			fmt.Printf("added control %s (%s)\n", req.RefID, id)
			return nil
		},
	}
	cmd.Flags().StringVar(&actor, "actor", "", "email of the authoring user (required)")
	cmd.Flags().StringVar(&framework, "framework", "", "framework reference id (required)")
	cmd.Flags().StringVar(&req.RefID, "ref", "", "control reference id, e.g. 3.1.1 (required)")
	cmd.Flags().StringVar(&req.Name, "name", "", "control name (required)")
	cmd.Flags().StringVar(&req.Description, "description", "", "control description")
	cmd.Flags().StringVar(&req.Section, "section", "", "section")
	cmd.Flags().StringVar(&req.Guidance, "guidance", "", "guidance")
	cmd.Flags().StringVar(&req.ParentRefID, "parent", "", "parent control reference id")
	for _, r := range []string{"actor", "framework", "ref", "name"} {
		_ = cmd.MarkFlagRequired(r)
	}
	return cmd
}

type transitionKind int

const (
	transitionSubmit transitionKind = iota
	transitionApprove
	transitionPublish
)

func newFrameworkTransitionCmd(f *cmdutil.Factory, use, short string, kind transitionKind) *cobra.Command {
	var actor, framework, comment string
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := f.Service(cmd.Context())
			if err != nil {
				return err
			}
			actorID, err := svc.IdentityIDByEmail(cmd.Context(), actor)
			if err != nil {
				return fmt.Errorf("cannot resolve actor %q: %w", actor, err)
			}
			versionID, err := resolveLatestVersion(cmd, svc, framework)
			if err != nil {
				return err
			}
			switch kind {
			case transitionSubmit:
				err = svc.Submit(cmd.Context(), actorID, versionID)
			case transitionApprove:
				err = svc.Approve(cmd.Context(), actorID, versionID, comment)
			case transitionPublish:
				err = svc.Publish(cmd.Context(), actorID, versionID)
			}
			if err != nil {
				return err
			}
			fmt.Printf("%s ok: %s\n", use, framework)
			return nil
		},
	}
	cmd.Flags().StringVar(&actor, "actor", "", "email of the acting user (required)")
	cmd.Flags().StringVar(&framework, "framework", "", "framework reference id (required)")
	if kind == transitionApprove {
		cmd.Flags().StringVar(&comment, "comment", "", "approval comment")
	}
	_ = cmd.MarkFlagRequired("actor")
	_ = cmd.MarkFlagRequired("framework")
	return cmd
}

func newFrameworkListCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List frameworks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := f.Service(cmd.Context())
			if err != nil {
				return err
			}
			frameworks, err := svc.ListFrameworks(cmd.Context())
			if err != nil {
				return err
			}
			for _, fw := range frameworks {
				fmt.Printf("%-24s %-8s %-14s public=%v  %s\n", fw.ReferenceID, fw.Region, fw.License, fw.Public, fw.Name)
			}
			return nil
		},
	}
}

func newBundleCmd(f *cmdutil.Factory) *cobra.Command {
	var framework, version, out string
	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "Export a signed .mzfw.json bundle",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := f.Service(cmd.Context())
			if err != nil {
				return err
			}
			versionID, err := resolveVersion(cmd, svc, framework, version)
			if err != nil {
				return err
			}
			bundle, err := svc.ExportBundle(cmd.Context(), versionID)
			if err != nil {
				return err
			}
			return writeJSONOut(bundle, out)
		},
	}
	cmd.Flags().StringVar(&framework, "framework", "", "framework reference id (required)")
	cmd.Flags().StringVar(&version, "version", "", "specific version (defaults to latest)")
	cmd.Flags().StringVar(&out, "out", "", "output file (defaults to stdout)")
	_ = cmd.MarkFlagRequired("framework")
	return cmd
}

func newSeedCmd(f *cmdutil.Factory) *cobra.Command {
	var framework, version, out string
	cmd := &cobra.Command{
		Use:   "seed",
		Short: "Export the flat GRC seed JSON",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := f.Service(cmd.Context())
			if err != nil {
				return err
			}
			versionID, err := resolveVersion(cmd, svc, framework, version)
			if err != nil {
				return err
			}
			seed, err := svc.ExportSeed(cmd.Context(), versionID)
			if err != nil {
				return err
			}
			return writeJSONOut(seed, out)
		},
	}
	cmd.Flags().StringVar(&framework, "framework", "", "framework reference id (required)")
	cmd.Flags().StringVar(&version, "version", "", "specific version (defaults to latest)")
	cmd.Flags().StringVar(&out, "out", "", "output file (defaults to stdout)")
	_ = cmd.MarkFlagRequired("framework")
	return cmd
}

func newTokenCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{Use: "token", Short: "Manage distribution tokens"}

	var actor, name, regions string
	issue := &cobra.Command{
		Use:   "issue",
		Short: "Issue a distribution token for a GRC instance (superadmin actor)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := f.Service(cmd.Context())
			if err != nil {
				return err
			}
			actorID, err := svc.IdentityIDByEmail(cmd.Context(), actor)
			if err != nil {
				return fmt.Errorf("cannot resolve actor %q: %w", actor, err)
			}
			token, err := svc.IssueToken(cmd.Context(), actorID, name, splitRegions(regions))
			if err != nil {
				return err
			}
			fmt.Printf("token (store securely, shown once):\n%s\n", token)
			return nil
		},
	}
	issue.Flags().StringVar(&actor, "actor", "", "email of the acting superadmin (required)")
	issue.Flags().StringVar(&name, "name", "", "GRC instance name (required)")
	issue.Flags().StringVar(&regions, "regions", "", "comma-separated region scope (empty = all)")
	_ = issue.MarkFlagRequired("actor")
	_ = issue.MarkFlagRequired("name")

	cmd.AddCommand(issue)
	return cmd
}

func newSigningKeyCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{Use: "signing-key", Short: "Manage ed25519 signing keys"}

	var actor, keyID string
	gen := &cobra.Command{
		Use:   "generate",
		Short: "Generate a new active signing key (superadmin actor)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := f.Service(cmd.Context())
			if err != nil {
				return err
			}
			actorID, err := svc.IdentityIDByEmail(cmd.Context(), actor)
			if err != nil {
				return fmt.Errorf("cannot resolve actor %q: %w", actor, err)
			}
			if err := svc.GenerateSigningKey(cmd.Context(), actorID, keyID); err != nil {
				return err
			}
			fmt.Printf("generated signing key %s\n", keyID)
			return nil
		},
	}
	gen.Flags().StringVar(&actor, "actor", "", "email of the acting superadmin (required)")
	gen.Flags().StringVar(&keyID, "key-id", "", "stable key id, e.g. reg-2026 (required)")
	_ = gen.MarkFlagRequired("actor")
	_ = gen.MarkFlagRequired("key-id")

	cmd.AddCommand(gen)
	return cmd
}

func newAuditCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "audit",
		Short: "Show the recent audit log",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := f.Service(cmd.Context())
			if err != nil {
				return err
			}
			entries, err := svc.RecentAudit(cmd.Context(), 100)
			if err != nil {
				return err
			}
			for _, e := range entries {
				fmt.Printf("%s  %-26s %-40s %s\n", e.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), e.Action, e.TargetID, e.Detail)
			}
			return nil
		},
	}
}

// resolveLatestVersion maps a framework reference id to its newest version id.
func resolveLatestVersion(cmd *cobra.Command, svc *registry.Service, reference string) (gid.GID, error) {
	fw, err := svc.FrameworkByReference(cmd.Context(), reference)
	if err != nil {
		return gid.Nil, fmt.Errorf("cannot find framework %q: %w", reference, err)
	}
	return svc.LatestVersionID(cmd.Context(), fw.ID)
}

// resolveVersion maps a framework reference and optional version string to a
// version id.
func resolveVersion(cmd *cobra.Command, svc *registry.Service, reference, version string) (gid.GID, error) {
	fw, err := svc.FrameworkByReference(cmd.Context(), reference)
	if err != nil {
		return gid.Nil, fmt.Errorf("cannot find framework %q: %w", reference, err)
	}
	versions, err := svc.VersionsOf(cmd.Context(), fw.ID)
	if err != nil {
		return gid.Nil, err
	}
	if version == "" {
		if len(versions) == 0 {
			return gid.Nil, coredata.ErrResourceNotFound
		}
		return versions[0].ID, nil
	}
	for _, v := range versions {
		if v.Version == version {
			return v.ID, nil
		}
	}
	return gid.Nil, fmt.Errorf("version %q not found", version)
}

func writeJSONOut(v any, out string) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if out == "" {
		fmt.Println(string(data))
		return nil
	}
	return os.WriteFile(out, data, 0o644)
}

func splitRegions(v string) []string {
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

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
