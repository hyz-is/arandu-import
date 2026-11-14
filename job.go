package imports

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path"
	"time"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/queue"
	"github.com/arandu-io/hesape/queue/jobs"
)

// The jobs the worker runs. Their names are what the queue routes by, so they
// are fixed once the package is published.
const (
	// ImportJob reads the file of a confirmed upload and writes its rows.
	ImportJob = "imports.import"
	// ExportJob writes the file of an export.
	ExportJob = "imports.export"
)

// The reasons a run fails, as codes the screens word in the person's language.
const (
	failFileGone     = "file_gone"
	failWrite        = "write"
	failExportLarge  = "export_large"
	failExportFailed = "export_failed"
	failDefinition   = "definition"
)

// errTooManyRows stops an export that would hold more rows than allowed.
var errTooManyRows = errors.New("imports: the export holds more rows than allowed")

// RegisterJobHandlers gives the worker the two jobs of the package. The worker
// is the application's, started by its worker command.
func (m *Module) RegisterJobHandlers(worker *queue.Worker) error {
	if worker == nil {
		return errors.New("imports: RegisterJobHandlers needs a worker")
	}
	worker.HandleFunc(ImportJob, m.service.handleImport)
	worker.HandleFunc(ExportJob, m.service.handleExport)
	return nil
}

// load reads the payload of a job and the run it names, under the Grant the job
// carries, and checks the Grant is the one the definition's side asks for.
// It answers a nil run when there is nothing to do: the run was cleaned away,
// the definition is not registered any more, or the work already ended.
func (s *Service) load(ctx context.Context, g security.Grant, j *jobs.Job, kind string) (*Run, Definition, error) {
	var msg message
	if err := j.Decode(&msg); err != nil {
		return nil, Definition{}, err
	}
	def, ok := s.cfg.definition(msg.Definition)
	if !ok {
		s.cfg.Logger.Warn("imports: a job names a definition that is not registered", "definition", msg.Definition, "run", msg.RunID)
		return nil, Definition{}, nil
	}
	if err := g.Check(def.actionOf(kind)); err != nil {
		return nil, Definition{}, err
	}
	run, err := Runs(s.db).WhereKey(msg.RunID).Where("definition", "=", def.Key).Where("kind", "=", kind).First(ctx, g)
	if err != nil || run == nil {
		return nil, Definition{}, err
	}
	switch run.Current() {
	case StatusQueued, StatusRunning:
		return run, def, nil
	}
	return nil, Definition{}, nil
}

// progress is the counts of an import as the job holds them, written to the
// run now and then so the screen can show how far along it is.
type progress struct {
	processed, created, updated, unchanged, refused int64
}

func (p progress) values(now time.Time) map[string]any {
	return map[string]any{
		"processed": p.processed, "created": p.created, "updated": p.updated,
		"unchanged": p.unchanged, "refused": p.refused, "updated_at": now,
	}
}

// start moves a run to running and zeroes what an earlier, interrupted attempt
// may have counted: the queue delivers a job at least once, and Write is an
// upsert, so doing the rows again lands in the same place.
func (s *Service) start(ctx context.Context, g security.Grant, run *Run) error {
	now := s.now()
	if _, err := Runs(s.db).WhereKey(run.ID).Update(ctx, g, map[string]any{
		"status": string(StatusRunning), "started_at": now, "updated_at": now,
		"processed": 0, "created": 0, "updated": 0, "unchanged": 0, "refused": 0, "failure": "",
	}); err != nil {
		return err
	}
	run.Status, run.StartedAt = string(StatusRunning), &now
	run.Processed, run.Created, run.Updated, run.Unchanged, run.Refused = 0, 0, 0, 0, 0
	s.changed(ctx, run)
	return nil
}

// fail ends a run that could not be done, and does not hand the error back to
// the queue: what stopped it is not something a second delivery changes, and
// the rows already written are an upsert away from being written again.
func (s *Service) fail(ctx context.Context, g security.Grant, run *Run, code string, cause error) error {
	if cause != nil {
		s.cfg.Logger.Error("imports: a run failed", "run", run.ID, "definition", run.Definition, "kind", run.Kind, "reason", code, "error", cause)
	}
	now := s.now()
	if _, err := Runs(s.db).WhereKey(run.ID).Update(ctx, g, map[string]any{
		"status": string(StatusFailed), "failure": code, "finished_at": now, "updated_at": now,
	}); err != nil {
		return err
	}
	run.Status, run.Failure, run.FinishedAt = string(StatusFailed), code, &now
	event := ActivityImportFailed
	if run.IsExport() {
		event = ActivityExportFailed
	}
	if err := s.recordBy(ctx, run.ActorID, event, run, map[string]any{"reason": code, "processed": run.Processed}); err != nil {
		s.cfg.Logger.Warn("imports: the activity log did not take the failure", "run", run.ID, "error", err)
	}
	s.changed(ctx, run)
	return nil
}

// handleImport is the job of an import: read the file, check each row, write
// the ones that pass, and report.
func (s *Service) handleImport(ctx context.Context, g security.Grant, j *jobs.Job) error {
	run, def, err := s.load(ctx, g, j, KindImport)
	if err != nil || run == nil {
		return err
	}
	if err := s.start(ctx, g, run); err != nil {
		return err
	}
	if err := s.recordBy(ctx, run.ActorID, ActivityImportStarted, run, map[string]any{"total": run.Total}); err != nil {
		return err
	}
	if !run.HasFile() {
		return s.fail(ctx, g, run, failFileGone, nil)
	}
	file, err := s.cfg.Files.Get(ctx, g, run.FileKey)
	if err != nil {
		return s.fail(ctx, g, run, failFileGone, err)
	}
	body, err := io.ReadAll(io.LimitReader(file.Body, s.cfg.MaxFileBytes+1))
	_ = file.Body.Close()
	if err != nil {
		return s.fail(ctx, g, run, failFileGone, err)
	}
	rows, m, err := s.read(def, "source"+extension(run.Format), body)
	if err != nil {
		return s.fail(ctx, g, run, failFileGone, err)
	}

	var (
		count   progress
		refused []Line
	)
	flushed := 0
	flush := func(force bool) error {
		if !force && int(count.processed)-flushed < s.cfg.ProgressEvery {
			return nil
		}
		flushed = int(count.processed)
		if _, err := Runs(s.db).WhereKey(run.ID).Update(ctx, g, count.values(s.now())); err != nil {
			return err
		}
		run.Processed, run.Created, run.Updated, run.Unchanged, run.Refused = count.processed, count.created, count.updated, count.unchanged, count.refused
		s.changed(ctx, run)
		return nil
	}

	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		values := m.row(def.Columns, row.cells)
		count.processed++
		clean, err := def.Validate(ctx, g, cloneRow(values))
		if err == nil {
			var outcome Outcome
			outcome, err = def.Write(ctx, g, clean)
			if err == nil {
				switch outcome {
				case Created:
					count.created++
				case Updated:
					count.updated++
				default:
					count.unchanged++
				}
			}
		}
		if err != nil {
			reason, isRefusal := reasonOf(err)
			if !isRefusal {
				_ = flush(true)
				return s.fail(ctx, g, run, failWrite, err)
			}
			count.refused++
			refused = append(refused, Line{Number: row.number, Values: values, Reason: reason})
		}
		if err := flush(false); err != nil {
			return err
		}
	}

	report := ""
	if len(refused) > 0 {
		report, err = s.writeReport(ctx, g, run, def, refused)
		if err != nil {
			return err
		}
	}
	sample := refused
	if len(sample) > sampleRows {
		sample = sample[:sampleRows]
	}
	encoded, err := json.Marshal(sample)
	if err != nil {
		return err
	}
	now := s.now()
	values := count.values(now)
	values["status"], values["report_key"], values["sample"], values["finished_at"] = string(StatusDone), report, string(encoded), now
	if _, err := Runs(s.db).WhereKey(run.ID).Update(ctx, g, values); err != nil {
		return err
	}
	run.Status, run.FinishedAt, run.ReportKey, run.Sample = string(StatusDone), &now, report, string(encoded)
	run.Processed, run.Created, run.Updated, run.Unchanged, run.Refused = count.processed, count.created, count.updated, count.unchanged, count.refused

	if def.Finish != nil {
		summary := Summary{Definition: def.Key, Total: len(rows), Created: int(count.created), Updated: int(count.updated), Unchanged: int(count.unchanged), Refused: int(count.refused)}
		if err := def.Finish(ctx, g, summary); err != nil {
			s.cfg.Logger.Warn("imports: the definition's Finish failed after the import ended", "definition", def.Key, "run", run.ID, "error", err)
		}
	}
	if err := s.recordBy(ctx, run.ActorID, ActivityImportFinished, run, map[string]any{
		"total": len(rows), "created": count.created, "updated": count.updated, "unchanged": count.unchanged, "refused": count.refused,
	}); err != nil {
		s.cfg.Logger.Warn("imports: the activity log did not take the end of an import", "run", run.ID, "error", err)
	}
	s.changed(ctx, run)
	return nil
}

// writeReport keeps the refused rows as a CSV the person can correct and send
// again: the line they were on, the columns of the definition and the reason.
func (s *Service) writeReport(ctx context.Context, g security.Grant, run *Run, def Definition, refused []Line) (string, error) {
	var buf bytes.Buffer
	w := newCSVWriter(&buf)
	heads := []string{s.labels.T("report.line")}
	for _, c := range def.Columns {
		heads = append(heads, c.Label)
	}
	heads = append(heads, s.labels.T("report.reason"))
	if err := w.Header(heads); err != nil {
		return "", err
	}
	for _, line := range refused {
		cells := []string{itoa(line.Number)}
		for _, c := range def.Columns {
			cells = append(cells, line.Values[c.Key])
		}
		cells = append(cells, line.Reason)
		if err := w.Row(cells); err != nil {
			return "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	key := path.Join("imports", run.ID, "refused.csv")
	if err := s.cfg.Files.Put(ctx, g, key, &buf, contentType(FormatCSV)); err != nil {
		return "", err
	}
	return key, nil
}

// handleExport is the job of an export: ask the definition for its records and
// write them to the file.
func (s *Service) handleExport(ctx context.Context, g security.Grant, j *jobs.Job) error {
	run, def, err := s.load(ctx, g, j, KindExport)
	if err != nil || run == nil {
		return err
	}
	if !def.CanExport() {
		return s.fail(ctx, g, run, failDefinition, nil)
	}
	if err := s.start(ctx, g, run); err != nil {
		return err
	}
	if err := s.recordBy(ctx, run.ActorID, ActivityExportStarted, run, nil); err != nil {
		return err
	}

	var buf bytes.Buffer
	w, err := newTableWriter(run.Format, &buf, def.Label)
	if err != nil {
		return s.fail(ctx, g, run, failExportFailed, err)
	}
	cols := def.Export.Columns
	heads := make([]string, len(cols))
	for i, c := range cols {
		heads[i] = c.Label
	}
	if err := w.Header(heads); err != nil {
		return s.fail(ctx, g, run, failExportFailed, err)
	}
	var written int64
	flushed := int64(0)
	err = def.Export.Each(ctx, g, func(row Row) error {
		if written >= int64(def.Export.MaxRows) {
			return errTooManyRows
		}
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = row[c.Key]
		}
		if err := w.Row(cells); err != nil {
			return err
		}
		written++
		if written-flushed >= int64(s.cfg.ProgressEvery)*20 {
			flushed = written
			if _, err := Runs(s.db).WhereKey(run.ID).Update(ctx, g, map[string]any{"processed": written, "updated_at": s.now()}); err != nil {
				return err
			}
			run.Processed = written
			s.changed(ctx, run)
		}
		return nil
	})
	switch {
	case errors.Is(err, errTooManyRows):
		return s.fail(ctx, g, run, failExportLarge, nil)
	case err != nil:
		return s.fail(ctx, g, run, failExportFailed, err)
	}
	if err := w.Close(); err != nil {
		return s.fail(ctx, g, run, failExportFailed, err)
	}
	size := int64(buf.Len())
	key := path.Join("imports", run.ID, "export"+extension(run.Format))
	if err := s.cfg.Files.Put(ctx, g, key, &buf, contentType(run.Format)); err != nil {
		return s.fail(ctx, g, run, failExportFailed, err)
	}
	now := s.now()
	if _, err := Runs(s.db).WhereKey(run.ID).Update(ctx, g, map[string]any{
		"status": string(StatusDone), "file_key": key, "file_size": size, "total": written, "processed": written,
		"finished_at": now, "updated_at": now,
	}); err != nil {
		return err
	}
	run.Status, run.FileKey, run.FileSize, run.Total, run.Processed, run.FinishedAt = string(StatusDone), key, size, written, written, &now
	if err := s.recordBy(ctx, run.ActorID, ActivityExportFinished, run, map[string]any{"rows": written, "bytes": size}); err != nil {
		s.cfg.Logger.Warn("imports: the activity log did not take the end of an export", "run", run.ID, "error", err)
	}
	s.changed(ctx, run)
	return nil
}
