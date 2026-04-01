package tooling

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

const pgQueryTimeout = 10 * time.Second

// PostgresExecutor provides PostgreSQL diagnostic query tools.
type PostgresExecutor struct {
	dsn    string
	db     *sql.DB
	logger *slog.Logger
}

// NewPostgresExecutor creates a new PostgreSQL tool executor.
// It opens a connection and pings the database to verify connectivity.
func NewPostgresExecutor(dsn string, logger *slog.Logger) (*PostgresExecutor, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &PostgresExecutor{
		dsn:    dsn,
		db:     db,
		logger: logger,
	}, nil
}

// Close releases the database connection pool.
func (p *PostgresExecutor) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}

// ListTools returns the available PostgreSQL diagnostic tools.
func (p *PostgresExecutor) ListTools(_ context.Context) ([]domain.Tool, error) {
	return []domain.Tool{
		{
			Name:        "pg_stat_activity",
			Description: "Query active connections and their current queries from pg_stat_activity.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"state_filter": map[string]interface{}{
						"type":        "string",
						"description": "Filter by connection state, e.g. 'active', 'idle in transaction'. If omitted, shows all non-idle connections.",
					},
				},
			},
		},
		{
			Name:        "pg_locks",
			Description: "Show blocking lock chains — which processes are blocking others.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "pg_replication_status",
			Description: "Show replication lag and status for all streaming replicas.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "pg_database_stats",
			Description: "Show database-level statistics: transactions, cache hits, tuples, conflicts, deadlocks.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"database": map[string]interface{}{
						"type":        "string",
						"description": "Database name. If omitted, uses the current database.",
					},
				},
			},
		},
		{
			Name:        "pg_slow_queries",
			Description: "Show top slow queries by mean execution time from pg_stat_statements (requires the extension).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit": map[string]interface{}{
						"type":        "number",
						"description": "Number of slow queries to return (default 10).",
					},
				},
			},
		},
		{
			Name:        "pg_table_stats",
			Description: "Show table statistics for vacuum analysis: dead tuples, sequential vs index scans, last vacuum times.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}, nil
}

// Execute runs a PostgreSQL diagnostic tool call.
func (p *PostgresExecutor) Execute(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	qctx, cancel := context.WithTimeout(ctx, pgQueryTimeout)
	defer cancel()

	switch call.Name {
	case "pg_stat_activity":
		return p.statActivity(qctx, call)
	case "pg_locks":
		return p.locks(qctx, call)
	case "pg_replication_status":
		return p.replicationStatus(qctx, call)
	case "pg_database_stats":
		return p.databaseStats(qctx, call)
	case "pg_slow_queries":
		return p.slowQueries(qctx, call)
	case "pg_table_stats":
		return p.tableStats(qctx, call)
	default:
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("unknown tool: %s", call.Name),
			IsError: true,
		}, nil
	}
}

func (p *PostgresExecutor) statActivity(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	stateFilter, _ := call.Input["state_filter"].(string)

	var query string
	var args []interface{}

	if stateFilter != "" {
		query = `SELECT pid, usename, application_name, state, wait_event_type, wait_event,
		         query_start, LEFT(query, 200) as query
		         FROM pg_stat_activity WHERE state = $1 ORDER BY query_start`
		args = append(args, stateFilter)
	} else {
		query = `SELECT pid, usename, application_name, state, wait_event_type, wait_event,
		         query_start, LEFT(query, 200) as query
		         FROM pg_stat_activity WHERE state != 'idle' ORDER BY query_start`
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return p.errResult(call.ID, "pg_stat_activity query failed", err), nil
	}
	defer rows.Close()

	var sb strings.Builder
	sb.WriteString("PID      | User             | App              | State            | Wait Type        | Wait Event       | Start                | Query\n")
	sb.WriteString(strings.Repeat("-", 160) + "\n")

	count := 0
	for rows.Next() {
		var (
			pid                                                     int
			usename, appName, state, waitType, waitEvent, queryText sql.NullString
			queryStart                                              sql.NullTime
		)
		if err := rows.Scan(&pid, &usename, &appName, &state, &waitType, &waitEvent, &queryStart, &queryText); err != nil {
			p.logger.Warn("scan pg_stat_activity row", "error", err)
			continue
		}
		sb.WriteString(fmt.Sprintf("%-8d | %-16s | %-16s | %-16s | %-16s | %-16s | %-20s | %s\n",
			pid,
			nullStr(usename),
			pgTruncate(nullStr(appName), 16),
			nullStr(state),
			nullStr(waitType),
			nullStr(waitEvent),
			nullTimeStr(queryStart),
			strings.ReplaceAll(nullStr(queryText), "\n", " "),
		))
		count++
	}

	if err := rows.Err(); err != nil {
		return p.errResult(call.ID, "pg_stat_activity iteration failed", err), nil
	}

	sb.WriteString(fmt.Sprintf("\nTotal: %d connections\n", count))
	return &domain.ToolResult{CallID: call.ID, Content: sb.String()}, nil
}

func (p *PostgresExecutor) locks(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	query := `SELECT blocked_locks.pid AS blocked_pid,
	                 blocked_activity.usename AS blocked_user,
	                 blocking_locks.pid AS blocking_pid,
	                 blocking_activity.usename AS blocking_user,
	                 LEFT(blocked_activity.query, 200) AS blocked_query
	          FROM pg_catalog.pg_locks blocked_locks
	          JOIN pg_catalog.pg_stat_activity blocked_activity
	              ON blocked_activity.pid = blocked_locks.pid
	          JOIN pg_catalog.pg_locks blocking_locks
	              ON blocking_locks.locktype = blocked_locks.locktype
	              AND blocking_locks.relation = blocked_locks.relation
	              AND blocking_locks.pid != blocked_locks.pid
	          JOIN pg_catalog.pg_stat_activity blocking_activity
	              ON blocking_activity.pid = blocking_locks.pid
	          WHERE NOT blocked_locks.granted`

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return p.errResult(call.ID, "pg_locks query failed", err), nil
	}
	defer rows.Close()

	var sb strings.Builder
	sb.WriteString("Blocked PID | Blocked User     | Blocking PID | Blocking User    | Blocked Query\n")
	sb.WriteString(strings.Repeat("-", 120) + "\n")

	count := 0
	for rows.Next() {
		var blockedPID, blockingPID int
		var blockedUser, blockingUser, blockedQuery sql.NullString
		if err := rows.Scan(&blockedPID, &blockedUser, &blockingPID, &blockingUser, &blockedQuery); err != nil {
			p.logger.Warn("scan pg_locks row", "error", err)
			continue
		}
		sb.WriteString(fmt.Sprintf("%-11d | %-16s | %-12d | %-16s | %s\n",
			blockedPID,
			nullStr(blockedUser),
			blockingPID,
			nullStr(blockingUser),
			strings.ReplaceAll(nullStr(blockedQuery), "\n", " "),
		))
		count++
	}

	if err := rows.Err(); err != nil {
		return p.errResult(call.ID, "pg_locks iteration failed", err), nil
	}

	if count == 0 {
		sb.WriteString("No blocking locks detected.\n")
	} else {
		sb.WriteString(fmt.Sprintf("\nTotal: %d blocking lock(s)\n", count))
	}
	return &domain.ToolResult{CallID: call.ID, Content: sb.String()}, nil
}

func (p *PostgresExecutor) replicationStatus(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	query := `SELECT client_addr, state, sent_lsn, write_lsn, flush_lsn, replay_lsn,
	                 (sent_lsn - replay_lsn) AS replay_lag
	          FROM pg_stat_replication`

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return p.errResult(call.ID, "pg_replication_status query failed", err), nil
	}
	defer rows.Close()

	var sb strings.Builder
	sb.WriteString("Client Addr      | State        | Sent LSN           | Write LSN          | Flush LSN          | Replay LSN         | Replay Lag (bytes)\n")
	sb.WriteString(strings.Repeat("-", 140) + "\n")

	count := 0
	for rows.Next() {
		var (
			clientAddr, state, sentLSN, writeLSN, flushLSN, replayLSN sql.NullString
			replayLag                                                  sql.NullInt64
		)
		if err := rows.Scan(&clientAddr, &state, &sentLSN, &writeLSN, &flushLSN, &replayLSN, &replayLag); err != nil {
			p.logger.Warn("scan pg_stat_replication row", "error", err)
			continue
		}
		lagStr := "N/A"
		if replayLag.Valid {
			lagStr = strconv.FormatInt(replayLag.Int64, 10)
		}
		sb.WriteString(fmt.Sprintf("%-16s | %-12s | %-18s | %-18s | %-18s | %-18s | %s\n",
			nullStr(clientAddr),
			nullStr(state),
			nullStr(sentLSN),
			nullStr(writeLSN),
			nullStr(flushLSN),
			nullStr(replayLSN),
			lagStr,
		))
		count++
	}

	if err := rows.Err(); err != nil {
		return p.errResult(call.ID, "pg_stat_replication iteration failed", err), nil
	}

	if count == 0 {
		sb.WriteString("No active replication connections.\n")
	} else {
		sb.WriteString(fmt.Sprintf("\nTotal: %d replica(s)\n", count))
	}
	return &domain.ToolResult{CallID: call.ID, Content: sb.String()}, nil
}

func (p *PostgresExecutor) databaseStats(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	dbName, _ := call.Input["database"].(string)

	var query string
	var args []interface{}

	if dbName != "" {
		query = `SELECT datname, numbackends, xact_commit, xact_rollback, blks_read,
		                blks_hit, tup_returned, tup_fetched, tup_inserted, tup_updated,
		                tup_deleted, conflicts, deadlocks
		         FROM pg_stat_database WHERE datname = $1`
		args = append(args, dbName)
	} else {
		query = `SELECT datname, numbackends, xact_commit, xact_rollback, blks_read,
		                blks_hit, tup_returned, tup_fetched, tup_inserted, tup_updated,
		                tup_deleted, conflicts, deadlocks
		         FROM pg_stat_database WHERE datname = current_database()`
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return p.errResult(call.ID, "pg_database_stats query failed", err), nil
	}
	defer rows.Close()

	var sb strings.Builder
	count := 0
	for rows.Next() {
		var (
			datname                                                                       string
			numBackends                                                                   int
			xactCommit, xactRollback, blksRead, blksHit                                   int64
			tupReturned, tupFetched, tupInserted, tupUpdated, tupDeleted, conflicts, deadl int64
		)
		if err := rows.Scan(&datname, &numBackends, &xactCommit, &xactRollback, &blksRead,
			&blksHit, &tupReturned, &tupFetched, &tupInserted, &tupUpdated,
			&tupDeleted, &conflicts, &deadl); err != nil {
			p.logger.Warn("scan pg_stat_database row", "error", err)
			continue
		}

		hitRatio := float64(0)
		if blksHit+blksRead > 0 {
			hitRatio = float64(blksHit) / float64(blksHit+blksRead) * 100
		}

		sb.WriteString(fmt.Sprintf("Database: %s\n", datname))
		sb.WriteString(fmt.Sprintf("  Active backends:    %d\n", numBackends))
		sb.WriteString(fmt.Sprintf("  Commits:            %d\n", xactCommit))
		sb.WriteString(fmt.Sprintf("  Rollbacks:          %d\n", xactRollback))
		sb.WriteString(fmt.Sprintf("  Blocks read:        %d\n", blksRead))
		sb.WriteString(fmt.Sprintf("  Blocks hit:         %d\n", blksHit))
		sb.WriteString(fmt.Sprintf("  Cache hit ratio:    %.2f%%\n", hitRatio))
		sb.WriteString(fmt.Sprintf("  Tuples returned:    %d\n", tupReturned))
		sb.WriteString(fmt.Sprintf("  Tuples fetched:     %d\n", tupFetched))
		sb.WriteString(fmt.Sprintf("  Tuples inserted:    %d\n", tupInserted))
		sb.WriteString(fmt.Sprintf("  Tuples updated:     %d\n", tupUpdated))
		sb.WriteString(fmt.Sprintf("  Tuples deleted:     %d\n", tupDeleted))
		sb.WriteString(fmt.Sprintf("  Conflicts:          %d\n", conflicts))
		sb.WriteString(fmt.Sprintf("  Deadlocks:          %d\n", deadl))
		count++
	}

	if err := rows.Err(); err != nil {
		return p.errResult(call.ID, "pg_stat_database iteration failed", err), nil
	}

	if count == 0 {
		sb.WriteString("No database statistics found.\n")
	}
	return &domain.ToolResult{CallID: call.ID, Content: sb.String()}, nil
}

func (p *PostgresExecutor) slowQueries(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	limit := 10
	if v, ok := call.Input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	if limit > 100 {
		limit = 100
	}

	query := `SELECT LEFT(query, 300) as query, calls, total_exec_time, mean_exec_time, rows
	          FROM pg_stat_statements ORDER BY mean_exec_time DESC LIMIT $1`

	rows, err := p.db.QueryContext(ctx, query, limit)
	if err != nil {
		// pg_stat_statements may not be installed.
		if strings.Contains(err.Error(), "does not exist") ||
			strings.Contains(err.Error(), "pg_stat_statements") {
			return &domain.ToolResult{
				CallID:  call.ID,
				Content: "pg_stat_statements extension is not available. Install it with: CREATE EXTENSION pg_stat_statements;",
			}, nil
		}
		return p.errResult(call.ID, "pg_slow_queries query failed", err), nil
	}
	defer rows.Close()

	var sb strings.Builder
	sb.WriteString("# Top Slow Queries (by mean execution time)\n\n")

	rank := 0
	for rows.Next() {
		var (
			queryText                        sql.NullString
			calls                            int64
			totalExecTime, meanExecTime      float64
			rowsReturned                     int64
		)
		if err := rows.Scan(&queryText, &calls, &totalExecTime, &meanExecTime, &rowsReturned); err != nil {
			p.logger.Warn("scan pg_stat_statements row", "error", err)
			continue
		}
		rank++
		sb.WriteString(fmt.Sprintf("#%d  Mean: %.2f ms | Total: %.2f ms | Calls: %d | Rows: %d\n",
			rank, meanExecTime, totalExecTime, calls, rowsReturned))
		sb.WriteString(fmt.Sprintf("    Query: %s\n\n",
			strings.ReplaceAll(nullStr(queryText), "\n", " ")))
	}

	if err := rows.Err(); err != nil {
		return p.errResult(call.ID, "pg_stat_statements iteration failed", err), nil
	}

	if rank == 0 {
		sb.WriteString("No query statistics available.\n")
	}
	return &domain.ToolResult{CallID: call.ID, Content: sb.String()}, nil
}

func (p *PostgresExecutor) tableStats(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	query := `SELECT schemaname, relname, seq_scan, idx_scan, n_tup_ins, n_tup_upd,
	                 n_tup_del, n_dead_tup, last_vacuum, last_autovacuum
	          FROM pg_stat_user_tables ORDER BY n_dead_tup DESC LIMIT 20`

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return p.errResult(call.ID, "pg_table_stats query failed", err), nil
	}
	defer rows.Close()

	var sb strings.Builder
	sb.WriteString("Schema           | Table                | Seq Scan   | Idx Scan   | Ins        | Upd        | Del        | Dead Tup   | Last Vacuum          | Last Autovacuum\n")
	sb.WriteString(strings.Repeat("-", 180) + "\n")

	count := 0
	for rows.Next() {
		var (
			schemaName, relName                         string
			seqScan, idxScan                            int64
			nTupIns, nTupUpd, nTupDel, nDeadTup         int64
			lastVacuum, lastAutovacuum                  sql.NullTime
		)
		if err := rows.Scan(&schemaName, &relName, &seqScan, &idxScan, &nTupIns, &nTupUpd,
			&nTupDel, &nDeadTup, &lastVacuum, &lastAutovacuum); err != nil {
			p.logger.Warn("scan pg_stat_user_tables row", "error", err)
			continue
		}
		sb.WriteString(fmt.Sprintf("%-16s | %-20s | %-10d | %-10d | %-10d | %-10d | %-10d | %-10d | %-20s | %s\n",
			pgTruncate(schemaName, 16),
			pgTruncate(relName, 20),
			seqScan, idxScan,
			nTupIns, nTupUpd, nTupDel, nDeadTup,
			nullTimeStr(lastVacuum),
			nullTimeStr(lastAutovacuum),
		))
		count++
	}

	if err := rows.Err(); err != nil {
		return p.errResult(call.ID, "pg_stat_user_tables iteration failed", err), nil
	}

	if count == 0 {
		sb.WriteString("No user tables found.\n")
	} else {
		sb.WriteString(fmt.Sprintf("\nTotal: %d table(s)\n", count))
	}
	return &domain.ToolResult{CallID: call.ID, Content: sb.String()}, nil
}

// errResult builds an error ToolResult with a formatted message.
func (p *PostgresExecutor) errResult(callID, msg string, err error) *domain.ToolResult {
	p.logger.Error(msg, "error", err)
	return &domain.ToolResult{
		CallID:  callID,
		Content: fmt.Sprintf("%s: %v", msg, err),
		IsError: true,
	}
}

// nullStr returns the string value or "<null>".
func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return "<null>"
}

// nullTimeStr formats a NullTime or returns "<never>".
func nullTimeStr(nt sql.NullTime) string {
	if nt.Valid {
		return nt.Time.Format("2006-01-02 15:04:05")
	}
	return "<never>"
}

// pgTruncate trims a string to maxLen characters.
func pgTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "~"
}
