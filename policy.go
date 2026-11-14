package imports

import (
	"context"
	"fmt"
	"strings"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/auth"
)

// The package owns no action of its own that a subject has to be given: what
// may be imported or exported is decided by the actions each Definition names,
// which are the application's, given to subjects by its permission module. The
// policy below answers about them.
//
// A subject that holds a Definition's ImportAction may import with it, see its
// runs and download its reports. A subject that holds its ExportAction may
// export, see the exports and download them.

// viewPrefix turns a Definition's action into the action that only reads it.
//
// A Grant issued for an action the policy says does not write cannot save a
// row, store a file or queue a job. The listing of runs and the download of a
// report are issued for the read form, so a bug on those paths cannot start an
// import. The subject is asked about the Definition's own action either way.
const viewPrefix = "imports.view:"

// viewOf is the read form of a Definition's action.
func viewOf(a security.Action) security.Action { return security.Action(viewPrefix) + a }

// ViewOf is the read form of a Definition's action: the action the package
// authorizes to look at a run or a report. It is exported so an application can
// ask its own policy tests what the package asks.
func ViewOf(a security.Action) security.Action { return viewOf(a) }

// baseOf is the action a subject has to hold for a, and whether a is one.
func baseOf(a security.Action) security.Action {
	return security.Action(strings.TrimPrefix(string(a), viewPrefix))
}

// reads says a is a read form.
func reads(a security.Action) bool { return strings.HasPrefix(string(a), viewPrefix) }

// RunPolicy decides about the runs of the Definitions it was built over.
type RunPolicy struct {
	actions map[security.Action]bool
}

var (
	_ security.Policy[Run] = RunPolicy{}
	_ auth.Writes          = RunPolicy{}
)

// NewRunPolicy builds the policy over the actions of the definitions: an action
// no Definition names is refused, whoever holds it.
func NewRunPolicy(defs []Definition) RunPolicy {
	known := map[security.Action]bool{}
	for _, d := range defs {
		if d.CanImport() {
			known[d.ImportAction] = true
		}
		if d.CanExport() {
			known[d.ExportAction] = true
		}
	}
	return RunPolicy{actions: known}
}

// Writes reports whether an action changes anything: every one but the read
// forms.
func (RunPolicy) Writes(a security.Action) bool { return !reads(a) }

// Can decides. It denies by default and has one rule that allows: the subject
// holds the Definition's action and the run, when there is one, is in the
// subject's own tenant.
func (p RunPolicy) Can(_ context.Context, s security.Subject, a security.Action, record Run) error {
	if record.ID != "" && record.TenantID != s.Tenant {
		return fmt.Errorf("the run belongs to another tenant")
	}
	if s.IsGuest() {
		return fmt.Errorf("a guest runs no import")
	}
	base := baseOf(a)
	if !p.actions[base] {
		return fmt.Errorf("no definition declares %s", base)
	}
	if !s.Can(base) {
		return fmt.Errorf("the subject was not granted %s", base)
	}
	return nil
}
