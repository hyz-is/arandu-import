package imports

import (
	"context"
	"fmt"
	"strings"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/translation"
	activitylog "github.com/hyz-is/arandu-activitylog"
)

// ActivityLogName is the log every entry of the package is written to, and
// ActivityRefType the type of the thing the entries are about.
const (
	ActivityLogName = "imports"
	ActivityRefType = "import_run"
)

// The events the package writes. Each carries counts and identifiers and never
// the content of a row: the rows are the application's records, and the log
// that says an import happened is not a second place to keep them.
const (
	ActivityImportStarted  = "imports.import_started"
	ActivityImportFinished = "imports.import_finished"
	ActivityImportFailed   = "imports.import_failed"
	ActivityExportStarted  = "imports.export_started"
	ActivityExportFinished = "imports.export_finished"
	ActivityExportFailed   = "imports.export_failed"
	ActivityFileRead       = "imports.file_read"
	ActivityFilesCleaned   = "imports.files_cleaned"
)

// recordGrant is the Grant an entry is written under: the log's own action in
// the tenant of the run. It is built from the tenant a run was stored with,
// never from a request.
func recordGrant(tenant string) (security.Grant, error) {
	if !security.ValidTenant(tenant) {
		return security.Grant{}, fmt.Errorf("imports: a run is in the tenant %q, which cannot be one", tenant)
	}
	//arandu:system-grant the log writes under its own action in the tenant the run was stored with; the entry says what the run counted and nothing else.
	return security.SystemGrant(activitylog.ActivityRecord, tenant), nil
}

// record writes one entry about a run, by a subject.
func (c *core) record(ctx context.Context, actor security.Subject, event string, run *Run, properties map[string]any) error {
	return c.recordBy(ctx, actor.ID, event, run, properties)
}

// recordBy writes one entry about a run, by whoever started it.
func (c *core) recordBy(ctx context.Context, actorID, event string, run *Run, properties map[string]any) error {
	if c.cfg.Activity == nil {
		return nil
	}
	g, err := recordGrant(run.TenantID)
	if err != nil {
		return err
	}
	description := c.labels.line("activity."+strings.ReplaceAll(strings.TrimPrefix(event, "imports."), ".", "_"), translation.Replace{"definition": c.labelOf(run.Definition)})
	entry := c.cfg.Activity.In(ctx, g, ActivityLogName).Event(event).
		OnRef(activitylog.Ref{Type: ActivityRefType, ID: run.ID})
	if actorID != "" {
		entry = entry.ByRef(activitylog.Ref{Type: "user", ID: actorID})
	}
	props := map[string]any{"definition": run.Definition, "kind": run.Kind, "format": run.Format}
	for key, value := range properties {
		props[key] = value
	}
	_, err = entry.WithProperties(props).Log(description)
	return err
}

// labelOf is the name of a definition, or its key when it is not registered any
// more.
func (c *core) labelOf(key string) string {
	if def, ok := c.cfg.definition(key); ok {
		return def.Label
	}
	return key
}
