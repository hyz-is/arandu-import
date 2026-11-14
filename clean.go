package imports

import (
	"context"
	"errors"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/security"
)

// CleanAction is the action the clean runs under, in the tenant of the runs it
// reaches. No subject holds it: it is issued to the scheduler and to nobody
// else.
const CleanAction security.Action = "imports.clean"

// Schedule declares the daily clean of the files. The files of an upload, a
// report and an export are deleted when the run expires: they hold the rows of
// the person's records, and a copy that nobody remembers is a copy that leaks.
// CleanSpec "-" declares none.
func (m *Module) Schedule() []foundation.Task {
	if m.cfg.CleanSpec == "-" {
		return nil
	}
	return []foundation.Task{{
		ID:        "imports.files.clean",
		Spec:      m.cfg.CleanSpec,
		Scope:     foundation.Global,
		Timeout:   15 * time.Minute,
		Singleton: true,
		Action:    CleanAction,
		Run: func(ctx context.Context, _ security.Grant) error {
			return m.Clean(ctx)
		},
	}}
}

// Clean deletes, in every tenant, the files of the runs that expired, and
// forgets where they were. An upload that nobody confirmed ends discarded. The
// run itself stays: the history keeps the counts. Schedule runs it daily; an
// application with a scheduler of its own calls it from there.
func (m *Module) Clean(ctx context.Context) error {
	return m.service.Clean(ctx)
}

// Clean is Module.Clean over the service.
func (s *Service) Clean(ctx context.Context) error {
	now := s.now()
	return eachTenant(ctx, s.db, runsTable, func(tenant string) error {
		//arandu:system-grant the tenant has runs in this module's table; the clean deletes only the files of its runs that expired, under the action nothing else is issued.
		g := security.SystemGrant(CleanAction, tenant)
		expired, err := Runs(s.db).Where("expires_at", "<=", now).Where(func(has *RunQuery) {
			has.Where("file_key", "<>", "").OrWhere("report_key", "<>", "")
		}).Limit(500).Get(ctx, g)
		if err != nil {
			return err
		}
		var cleaned int64
		for _, run := range expired {
			if err := s.sweep(ctx, g, run, now); err != nil {
				return err
			}
			cleaned++
		}
		if cleaned == 0 {
			return nil
		}
		if err := s.cleaned(ctx, tenant, cleaned); err != nil {
			s.cfg.Logger.Warn("imports: the activity log did not take the clean", "tenant", tenant, "error", err)
		}
		return nil
	})
}

// sweep deletes the files of one expired run.
func (s *Service) sweep(ctx context.Context, g security.Grant, run *Run, now time.Time) error {
	for _, key := range []string{run.FileKey, run.ReportKey} {
		if key == "" {
			continue
		}
		if err := s.cfg.Files.Delete(ctx, g, key); err != nil {
			return err
		}
	}
	values := map[string]any{"file_key": "", "report_key": "", "updated_at": now}
	if run.Current() == StatusUploaded {
		values["status"] = string(StatusDiscarded)
	}
	_, err := Runs(s.db).WhereKey(run.ID).Update(ctx, g, values)
	return err
}

// cleaned writes the one entry that says how many runs lost their files.
func (s *Service) cleaned(ctx context.Context, tenant string, count int64) error {
	if s.cfg.Activity == nil {
		return nil
	}
	return s.recordBy(ctx, "", ActivityFilesCleaned, &Run{ID: "clean", TenantID: tenant, Definition: "-", Kind: "clean"}, map[string]any{"runs": count})
}

// eachTenant runs fn once for every valid tenant with a row in the table. It
// reads the tenant column and nothing else; every row fn reaches, it reaches
// with a Grant of that tenant.
func eachTenant(ctx context.Context, db *data.DB, table string, fn func(tenant string) error) error {
	rows, err := db.QueryContext(ctx, "SELECT DISTINCT tenant_id FROM "+table)
	if err != nil {
		return err
	}
	var tenants []string
	for rows.Next() {
		var tenant string
		if err := rows.Scan(&tenant); err != nil {
			_ = rows.Close()
			return err
		}
		if security.ValidTenant(tenant) {
			tenants = append(tenants, tenant)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, tenant := range tenants {
		if err := fn(tenant); err != nil {
			return err
		}
	}
	return nil
}
