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

package coredata

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/jackc/pgx/v5"
	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/gid"
)

type (
	// PolicyTemplate is a reusable policy document template with a markdown body
	// (extension over the universal template, which stores only a name/ref).
	PolicyTemplate struct {
		ID          gid.GID   `db:"id"`
		FrameworkID *gid.GID  `db:"framework_id"`
		Name        string    `db:"name"`
		Body        string    `db:"body"`
		Origin      string    `db:"origin"`
		CreatedAt   time.Time `db:"created_at"`
	}

	PolicyTemplates []*PolicyTemplate

	// PolicyControlLink relates a policy template to a control (M:N).
	PolicyControlLink struct {
		PolicyTemplateID gid.GID `db:"policy_template_id"`
		ControlID        gid.GID `db:"control_id"`
	}

	PolicyControlLinks []*PolicyControlLink
)

const policyTemplateColumns = `id, framework_id, name, body, origin, created_at`

func (p PolicyTemplate) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `
INSERT INTO policy_templates (tenant_id, id, framework_id, name, body, origin, created_at)
VALUES (@tenant_id, @id, @framework_id, @name, @body, @origin, @created_at);`

	args := pgx.StrictNamedArgs{
		"tenant_id":    scope.GetTenantID(),
		"id":           p.ID,
		"framework_id": p.FrameworkID,
		"name":         p.Name,
		"body":         p.Body,
		"origin":       orManual(p.Origin),
		"created_at":   p.CreatedAt,
	}

	_, err := conn.Exec(ctx, q, args)
	return err
}

func (p *PolicyTemplate) Update(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := fmt.Sprintf(`UPDATE policy_templates SET name = @name, body = @body WHERE %s AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": p.ID, "name": p.Name, "body": p.Body}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

func (p PolicyTemplate) Delete(ctx context.Context, conn pg.Tx, scope Scoper, id gid.GID) error {
	q := fmt.Sprintf(`DELETE FROM policy_templates WHERE %s AND id = @id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"id": id}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadAllByFramework returns a framework's policy templates, ordered by name.
func (ps *PolicyTemplates) LoadAllByFramework(ctx context.Context, conn pg.Querier, scope Scoper, frameworkID gid.GID) error {
	q := fmt.Sprintf(`SELECT %s FROM policy_templates WHERE %s AND framework_id = @framework_id ORDER BY name ASC;`,
		policyTemplateColumns, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"framework_id": frameworkID}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query policy templates: %w", err)
	}

	templates, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[PolicyTemplate])
	if err != nil {
		return fmt.Errorf("cannot collect policy templates: %w", err)
	}

	*ps = templates
	return nil
}

func (l PolicyControlLink) Insert(ctx context.Context, conn pg.Tx, scope Scoper) error {
	q := `INSERT INTO policy_template_controls (tenant_id, policy_template_id, control_id) VALUES (@tenant_id, @policy_template_id, @control_id) ON CONFLICT DO NOTHING;`
	args := pgx.StrictNamedArgs{"tenant_id": scope.GetTenantID(), "policy_template_id": l.PolicyTemplateID, "control_id": l.ControlID}
	_, err := conn.Exec(ctx, q, args)
	return err
}

// DeleteByTemplate removes every control link of a template (used on replace).
func (l PolicyControlLink) DeleteByTemplate(ctx context.Context, conn pg.Tx, scope Scoper, templateID gid.GID) error {
	q := fmt.Sprintf(`DELETE FROM policy_template_controls WHERE %s AND policy_template_id = @template_id;`, scope.SQLFragment())
	args := pgx.StrictNamedArgs{"template_id": templateID}
	maps.Copy(args, scope.SQLArguments())
	_, err := conn.Exec(ctx, q, args)
	return err
}

// LoadAllByTemplates returns the control links of the given templates.
func (ls *PolicyControlLinks) LoadAllByTemplates(ctx context.Context, conn pg.Querier, scope Scoper, templateIDs []gid.GID) error {
	q := fmt.Sprintf(`SELECT policy_template_id, control_id FROM policy_template_controls WHERE %s AND policy_template_id = ANY(@ids::text[]);`, scope.SQLFragment())

	args := pgx.StrictNamedArgs{"ids": templateIDs}
	maps.Copy(args, scope.SQLArguments())

	rows, err := conn.Query(ctx, q, args)
	if err != nil {
		return fmt.Errorf("cannot query policy control links: %w", err)
	}

	links, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[PolicyControlLink])
	if err != nil {
		return fmt.Errorf("cannot collect policy control links: %w", err)
	}

	*ls = links
	return nil
}
