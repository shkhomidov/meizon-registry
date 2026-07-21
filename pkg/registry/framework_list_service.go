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

package registry

import (
	"context"
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/gid"
)

// FrameworkListItem is the Frameworks page row: the framework plus the
// provenance an auditor needs to judge it — who authored it, when it last
// moved, and whether the source document it came from is still on file.
type FrameworkListItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Region   string `json:"region"`
	License  string `json:"license"`
	Public   bool   `json:"public"`
	Language string `json:"language,omitempty"`

	// Author is whoever created the newest version — the person to ask about
	// this content. Email is the stable identifier; Name may be empty.
	AuthorName  string `json:"authorName,omitempty"`
	AuthorEmail string `json:"authorEmail,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	LatestVersion string `json:"latestVersion,omitempty"`
	LatestStatus  string `json:"latestStatus,omitempty"`
	Versions      int    `json:"versions"`

	// Source describes the uploaded document this framework was generated
	// from, when one was kept. Absent for hand-authored or imported frameworks.
	Source *SourceDocumentView `json:"source,omitempty"`
}

// FrameworkList returns the console's Frameworks page rows.
//
// Assembled in one pass rather than per-row lookups: the page is a list, and a
// query per framework would turn a 2-row page into a dozen round trips the
// moment the registry grows.
func (s *Service) FrameworkList(ctx context.Context) ([]FrameworkListItem, error) {
	out := []FrameworkListItem{}

	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var frameworks coredata.Frameworks
		if err := frameworks.LoadAll(ctx, conn, scope); err != nil {
			return err
		}
		if len(frameworks) == 0 {
			return nil
		}

		sources, err := s.sourcesByFramework(ctx, conn)
		if err != nil {
			return err
		}

		// Identity lookups are cached across rows: several frameworks usually
		// share one author.
		people := map[gid.GID]coredata.Identity{}
		lookup := func(id gid.GID) coredata.Identity {
			if id == gid.Nil {
				return coredata.Identity{}
			}
			if p, ok := people[id]; ok {
				return p
			}
			var p coredata.Identity
			if err := p.LoadByID(ctx, conn, scope, id); err != nil {
				p = coredata.Identity{} // a deleted account shows as unknown, not an error
			}
			people[id] = p
			return p
		}

		for _, f := range frameworks {
			item := FrameworkListItem{
				ID: f.ReferenceID, Name: f.Name, Region: f.Region,
				License: f.License, Public: f.Public, Language: f.SourceLanguage,
				CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt,
			}

			var versions coredata.FrameworkVersions
			if err := versions.LoadAllByFramework(ctx, conn, scope, f.ID); err != nil {
				return err
			}
			item.Versions = len(versions)
			if len(versions) > 0 {
				latest := versions[0]
				item.LatestVersion = latest.Version
				item.LatestStatus = string(latest.Status)
				// The framework row's updated_at tracks metadata edits; the
				// version's tracks content. Content is what a reviewer cares
				// about, so the later of the two is shown.
				if latest.UpdatedAt.After(item.UpdatedAt) {
					item.UpdatedAt = latest.UpdatedAt
				}
				author := lookup(latest.AuthorID)
				item.AuthorName, item.AuthorEmail = author.FullName, author.Email
			}

			if src, ok := sources[f.ID]; ok {
				v := sourceView(src)
				item.Source = &v
			}
			out = append(out, item)
		}
		return nil
	})
	return out, err
}
