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
	"encoding/json"
	"time"

	"go.gearno.de/kit/pg"
	"go.meizon.cloud/registry/pkg/coredata"
	"go.meizon.cloud/registry/pkg/docextract"
	"go.meizon.cloud/registry/pkg/gid"
)

// jobStore mirrors an in-memory job to the database.
//
// Writes are best-effort and never block the pipeline: losing a progress tick
// is cosmetic, whereas failing a 106-page OCR because a status row could not be
// written would be absurd. The terminal transition matters more than the ticks,
// and is the one worth reading back.
type jobStore struct {
	svc *Service
}

// JobView is one run on the jobs page.
type JobView struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	Status       string    `json:"status"`
	Step         string    `json:"step,omitempty"`
	Current      int       `json:"current"`
	Total        int       `json:"total"`
	Label        string    `json:"label,omitempty"`
	FrameworkRef string    `json:"frameworkRef,omitempty"`
	Error        string    `json:"error,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// start records a job as running.
func (s *Service) startJobRecord(ctx context.Context, jobID, kind, label, frameworkRef string, actorID gid.GID) {
	id, err := gid.ParseGID(jobID)
	if err != nil {
		return
	}
	now := time.Now()
	row := coredata.IngestJob{
		ID: id, Kind: kind, Status: coredata.JobStatusRunning,
		Label: label, FrameworkRef: frameworkRef,
		ActorID: actorID.String(), CreatedAt: now, UpdatedAt: now,
	}
	if err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		return row.Insert(ctx, tx, s.platformScope())
	}); err != nil {
		s.logger.WarnCtx(ctx, "cannot record job start: "+err.Error())
	}
}

func (st *jobStore) progress(jobID, step string, current, total int) {
	id, err := gid.ParseGID(jobID)
	if err != nil {
		return
	}
	ctx := context.Background()
	_ = st.svc.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		return (coredata.IngestJob{}).UpdateProgress(ctx, conn, st.svc.platformScope(), id, step, current, total)
	})
}

func (st *jobStore) finish(jobID, status string, res *GeneratedFramework, diff map[string]string, baseline, errText string) {
	id, err := gid.ParseGID(jobID)
	if err != nil {
		return
	}

	// The whole proposal is stored, not just a summary: it is what the reviewer
	// comes back to, and re-running the pipeline to recover it would cost
	// another OCR pass and every LLM call.
	var payload []byte
	if res != nil {
		if b, merr := json.Marshal(IngestStatus{
			Status: status, Result: res, Diff: diff, Baseline: baseline,
		}); merr == nil {
			payload = b
		}
	}

	ctx := context.Background()
	write := func(status string, payload []byte, errText string) error {
		return st.svc.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
			return (coredata.IngestJob{}).Finish(ctx, conn, st.svc.platformScope(), id, status, payload, errText)
		})
	}

	err = write(status, payload, errText)
	if err == nil {
		return
	}

	// The write failed — most likely a character jsonb rejects (a NUL byte,
	// SQLSTATE 22P05) that slipped into the model's output or a diff key even
	// though the source text was sanitized. Before giving up the result, retry
	// with the SAME payload sanitized. Losing a 106-page OCR generation over one
	// byte is expensive; the sanitized copy keeps the work minus the offending
	// character.
	//
	// Sanitize the DECODED strings, never the raw JSON: a NUL byte in the data
	// marshals to a 6-char backslash-u escape, and stripping that escape from the
	// bytes would corrupt valid content. Round-tripping through a generic value
	// strips only actual NUL runes, leaving any literal escape text intact.
	if len(payload) > 0 {
		if clean, ok := sanitizeJSONStrings(payload); ok {
			if werr := write(status, clean, errText); werr == nil {
				st.svc.logger.WarnCtx(ctx, "job result saved after stripping characters jsonb rejected: "+err.Error())
				return
			}
		}
	}

	// The job MUST reach a terminal state. Leaving the row at "running" is worse
	// than losing the payload: the console polls it forever, and an operator sees
	// a job that is still working when nothing is. That is exactly what happened
	// when a PDF's text carried a NUL and jsonb rejected the payload (22P05) —
	// the generation had already succeeded and the run looked hung for hours.
	//
	// So retry without the payload. A visible failure beats a phantom.
	st.svc.logger.ErrorCtx(ctx, "cannot record job completion, marking job failed: "+err.Error())
	if rerr := write("error", nil, "the result could not be saved: "+err.Error()); rerr != nil {
		st.svc.logger.ErrorCtx(ctx, "cannot mark job failed either, it will stay running: "+rerr.Error())
	}
}

// sanitizeJSONStrings strips characters jsonb rejects (NUL, invalid UTF-8) from
// every string VALUE in a JSON document, operating on the decoded structure so a
// literal escape sequence in the text is preserved. Returns ok=false only if the
// payload does not decode, which cannot happen for bytes json.Marshal produced.
func sanitizeJSONStrings(raw []byte) ([]byte, bool) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false
	}
	out, err := json.Marshal(sanitizeValue(v))
	if err != nil {
		return nil, false
	}
	return out, true
}

func sanitizeValue(v any) any {
	switch t := v.(type) {
	case string:
		return docextract.Sanitize(t)
	case []any:
		for i := range t {
			t[i] = sanitizeValue(t[i])
		}
		return t
	case map[string]any:
		for k, val := range t {
			t[k] = sanitizeValue(val)
		}
		return t
	default:
		return v
	}
}

// JobList returns recent runs for the status page.
func (s *Service) JobList(ctx context.Context, limit int) ([]JobView, error) {
	out := []JobView{}
	err := s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		var jobs coredata.IngestJobs
		if err := jobs.LoadRecent(ctx, conn, s.platformScope(), limit); err != nil {
			return err
		}
		for _, j := range jobs {
			out = append(out, JobView{
				ID: j.ID.String(), Kind: j.Kind, Status: j.Status, Step: j.Step,
				Current: j.Current, Total: j.Total, Label: j.Label,
				FrameworkRef: j.FrameworkRef, Error: j.Error,
				CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt,
			})
		}
		return nil
	})
	return out, err
}

// jobStatusFromStore reconstructs a job's status from the database, for a job
// this process does not hold in memory — a different tab, or after a restart.
func (s *Service) jobStatusFromStore(ctx context.Context, jobID string) (IngestStatus, error) {
	id, err := gid.ParseGID(jobID)
	if err != nil {
		return IngestStatus{}, coredata.ErrResourceNotFound
	}

	var out IngestStatus
	err = s.db.WithConn(ctx, func(ctx context.Context, conn pg.Querier) error {
		scope := s.platformScope()

		var row coredata.IngestJob
		if err := row.LoadByID(ctx, conn, scope, id); err != nil {
			return err
		}
		out = IngestStatus{
			Status:   row.Status,
			Step:     row.Step,
			Progress: IngestProgress{Current: row.Current, Total: row.Total},
			Error:    row.Error,
		}
		if row.Status != coredata.JobStatusDone {
			return nil
		}
		payload, err := (coredata.IngestJob{}).Result(ctx, conn, scope, id)
		if err != nil || len(payload) == 0 {
			return nil // the run finished but its document was not stored
		}
		var stored IngestStatus
		if err := json.Unmarshal(payload, &stored); err == nil {
			stored.Status = row.Status
			out = stored
		}
		return nil
	})
	return out, err
}

// FailOrphanedJobs marks runs still flagged "running" from a previous process.
// Called at startup: a job whose goroutine died with the process will never
// finish, and showing it as in-progress makes a user wait for nothing.
func (s *Service) FailOrphanedJobs(ctx context.Context) {
	err := s.db.WithTx(ctx, func(ctx context.Context, tx pg.Tx) error {
		n, err := (coredata.IngestJob{}).FailOrphaned(ctx, tx, s.platformScope(),
			"the server restarted while this job was running")
		if err == nil && n > 0 {
			s.logger.InfoCtx(ctx, "marked interrupted jobs as failed")
		}
		return err
	})
	if err != nil {
		s.logger.WarnCtx(ctx, "cannot reconcile interrupted jobs: "+err.Error())
	}
}
