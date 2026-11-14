package imports

import (
	"context"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/hesape/database/migrations"
	"github.com/arandu-io/hesape/database/schema"
)

// Migrations declares the schema this module owns: the runs. The queue's own
// table is the application's, migrated with the queue.
//
// They are returned in the order their names sort in, which is the order they
// apply in.
func (m *Module) Migrations() []foundation.Migration {
	return []foundation.Migration{createRuns{}}
}

// The migration is reversible, and the assertion is here rather than discovered
// at rollback: the migrator tests for Down with a type assertion, so a Down with
// the wrong signature would leave a rollback that silently does nothing.
var _ migrations.ReversibleMigration = createRuns{}

// createRuns is the table of runs and the indexes its reads go by.
type createRuns struct{ migrations.BaseMigration }

// GetName is the migration's identity, and it carries the order. It is fixed
// once the package is published.
func (createRuns) GetName() string { return "20261113_0001_create_import_runs" }

// Up creates the table. The timestamps have no database default: the value comes
// from Go.
func (createRuns) Up(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().Create(ctx, runsTable, func(table *schema.Blueprint) {
		table.String("id", maxIdentifierLen).Primary()
		table.String("tenant_id", maxIdentifierLen)
		table.String("definition", maxIdentifierLen)
		table.String("kind", 16)
		table.String("status", 16)
		table.String("format", 8)
		table.String("file_name", maxNameLen).Default("")
		table.String("file_key", maxKeyLen).Default("")
		table.BigInteger("file_size").Default(0)
		table.String("report_key", maxKeyLen).Default("")
		table.BigInteger("total").Default(0)
		table.BigInteger("processed").Default(0)
		table.BigInteger("created").Default(0)
		table.BigInteger("updated").Default(0)
		table.BigInteger("unchanged").Default(0)
		table.BigInteger("refused").Default(0)
		table.Text("preview").Default("")
		table.Text("sample").Default("")
		table.String("failure", maxFailureLen).Default("")
		table.String("actor_id", maxIdentifierLen).Default("")
		table.Timestamp("created_at")
		table.Timestamp("updated_at")
		table.Timestamp("started_at").Nullable()
		table.Timestamp("finished_at").Nullable()
		table.Timestamp("expires_at")

		table.Index([]string{"tenant_id", "created_at", "id"}, "import_runs_tenant_created_idx")
		table.Index([]string{"tenant_id", "definition", "kind", "created_at"}, "import_runs_tenant_definition_idx")
		table.Index([]string{"expires_at"}, "import_runs_expires_idx")
	})
}

// Down drops the table.
func (createRuns) Down(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().DropIfExists(ctx, runsTable)
}
