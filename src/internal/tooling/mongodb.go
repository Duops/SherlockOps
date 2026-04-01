package tooling

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

const mongoCommandTimeout = 10 * time.Second

// MongoDBExecutor provides MongoDB diagnostic tools.
type MongoDBExecutor struct {
	uri    string
	client *mongo.Client
	logger *slog.Logger
}

// NewMongoDBExecutor creates a new MongoDB tool executor and establishes a connection.
func NewMongoDBExecutor(uri string, logger *slog.Logger) (*MongoDBExecutor, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mongoCommandTimeout)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("mongodb connect: %w", err)
	}

	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("mongodb ping: %w", err)
	}

	return &MongoDBExecutor{
		uri:    uri,
		client: client,
		logger: logger,
	}, nil
}

// Close disconnects the MongoDB client.
func (m *MongoDBExecutor) Close(ctx context.Context) error {
	if m.client != nil {
		return m.client.Disconnect(ctx)
	}
	return nil
}

// ListTools returns the available MongoDB diagnostic tools.
func (m *MongoDBExecutor) ListTools(_ context.Context) ([]domain.Tool, error) {
	return []domain.Tool{
		{
			Name:        "mongo_server_status",
			Description: "Run db.serverStatus() to get MongoDB server health: uptime, connections, opcounters, memory, and network stats.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "mongo_current_op",
			Description: "Run db.currentOp() to list active operations with opid, type, namespace, running time, and query plan.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"active_only": map[string]interface{}{
						"type":        "boolean",
						"description": "If true, show only active operations (default: true)",
					},
				},
			},
		},
		{
			Name:        "mongo_rs_status",
			Description: "Run rs.status() (replSetGetStatus) to get replica set members, states, optime lag, and health.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "mongo_db_stats",
			Description: "Run db.stats() on a specific database to get collection count, object count, data size, index size, and storage size.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"database": map[string]interface{}{
						"type":        "string",
						"description": "Name of the database to inspect",
					},
				},
				"required": []interface{}{"database"},
			},
		},
		{
			Name:        "mongo_top",
			Description: "Run adminCommand({top:1}) to get per-collection read/write/command counts and times. Shows top 10 busiest collections.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "mongo_collection_stats",
			Description: "Run collStats on a specific collection to get document count, size, indexes, and index sizes.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"database": map[string]interface{}{
						"type":        "string",
						"description": "Name of the database",
					},
					"collection": map[string]interface{}{
						"type":        "string",
						"description": "Name of the collection",
					},
				},
				"required": []interface{}{"database", "collection"},
			},
		},
	}, nil
}

// Execute runs a MongoDB diagnostic tool call.
func (m *MongoDBExecutor) Execute(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	switch call.Name {
	case "mongo_server_status":
		return m.serverStatus(ctx, call)
	case "mongo_current_op":
		return m.currentOp(ctx, call)
	case "mongo_rs_status":
		return m.rsStatus(ctx, call)
	case "mongo_db_stats":
		return m.dbStats(ctx, call)
	case "mongo_top":
		return m.top(ctx, call)
	case "mongo_collection_stats":
		return m.collectionStats(ctx, call)
	default:
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("unknown tool: %s", call.Name),
			IsError: true,
		}, nil
	}
}

func (m *MongoDBExecutor) runAdminCommand(ctx context.Context, cmd bson.D) (bson.M, error) {
	ctx, cancel := context.WithTimeout(ctx, mongoCommandTimeout)
	defer cancel()

	var result bson.M
	err := m.client.Database("admin").RunCommand(ctx, cmd).Decode(&result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (m *MongoDBExecutor) serverStatus(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	result, err := m.runAdminCommand(ctx, bson.D{{Key: "serverStatus", Value: 1}})
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("serverStatus error: %v", err),
			IsError: true,
		}, nil
	}

	return &domain.ToolResult{
		CallID:  call.ID,
		Content: formatServerStatus(result),
	}, nil
}

func (m *MongoDBExecutor) currentOp(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	activeOnly := true
	if v, ok := call.Input["active_only"]; ok {
		if b, ok := v.(bool); ok {
			activeOnly = b
		}
	}

	ctx, cancel := context.WithTimeout(ctx, mongoCommandTimeout)
	defer cancel()

	var result bson.M
	cmd := bson.D{
		{Key: "currentOp", Value: 1},
		{Key: "active", Value: activeOnly},
	}
	err := m.client.Database("admin").RunCommand(ctx, cmd).Decode(&result)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("currentOp error: %v", err),
			IsError: true,
		}, nil
	}

	return &domain.ToolResult{
		CallID:  call.ID,
		Content: formatCurrentOp(result),
	}, nil
}

func (m *MongoDBExecutor) rsStatus(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	result, err := m.runAdminCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}})
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("replSetGetStatus error: %v", err),
			IsError: true,
		}, nil
	}

	return &domain.ToolResult{
		CallID:  call.ID,
		Content: formatRSStatus(result),
	}, nil
}

func (m *MongoDBExecutor) dbStats(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	dbName, _ := call.Input["database"].(string)
	if dbName == "" {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: "missing required parameter: database",
			IsError: true,
		}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, mongoCommandTimeout)
	defer cancel()

	var result bson.M
	err := m.client.Database(dbName).RunCommand(ctx, bson.D{{Key: "dbStats", Value: 1}}).Decode(&result)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("dbStats error: %v", err),
			IsError: true,
		}, nil
	}

	return &domain.ToolResult{
		CallID:  call.ID,
		Content: formatDBStats(result),
	}, nil
}

func (m *MongoDBExecutor) top(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	result, err := m.runAdminCommand(ctx, bson.D{{Key: "top", Value: 1}})
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("top error: %v", err),
			IsError: true,
		}, nil
	}

	return &domain.ToolResult{
		CallID:  call.ID,
		Content: formatTop(result),
	}, nil
}

func (m *MongoDBExecutor) collectionStats(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	dbName, _ := call.Input["database"].(string)
	collName, _ := call.Input["collection"].(string)
	if dbName == "" || collName == "" {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: "missing required parameters: database, collection",
			IsError: true,
		}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, mongoCommandTimeout)
	defer cancel()

	var result bson.M
	cmd := bson.D{{Key: "collStats", Value: collName}}
	err := m.client.Database(dbName).RunCommand(ctx, cmd).Decode(&result)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("collStats error: %v", err),
			IsError: true,
		}, nil
	}

	return &domain.ToolResult{
		CallID:  call.ID,
		Content: formatCollectionStats(result),
	}, nil
}

// --- Formatting functions ---

func formatServerStatus(r bson.M) string {
	var sb strings.Builder
	sb.WriteString("=== MongoDB Server Status ===\n\n")

	sb.WriteString(fmt.Sprintf("Host: %v\n", r["host"]))
	sb.WriteString(fmt.Sprintf("Version: %v\n", r["version"]))
	sb.WriteString(fmt.Sprintf("Uptime: %s\n", formatUptime(getFloat64(r, "uptimeMillis")/1000)))

	// Connections
	if conns, ok := r["connections"].(bson.M); ok {
		sb.WriteString("\n--- Connections ---\n")
		sb.WriteString(fmt.Sprintf("  Current:   %v\n", conns["current"]))
		sb.WriteString(fmt.Sprintf("  Available: %v\n", conns["available"]))
		sb.WriteString(fmt.Sprintf("  Total created: %v\n", conns["totalCreated"]))
	}

	// Opcounters
	if ops, ok := r["opcounters"].(bson.M); ok {
		sb.WriteString("\n--- Opcounters ---\n")
		for _, key := range []string{"insert", "query", "update", "delete", "getmore", "command"} {
			if v, exists := ops[key]; exists {
				sb.WriteString(fmt.Sprintf("  %-10s %v\n", key+":", v))
			}
		}
	}

	// Memory
	if mem, ok := r["mem"].(bson.M); ok {
		sb.WriteString("\n--- Memory (MB) ---\n")
		sb.WriteString(fmt.Sprintf("  Resident: %v\n", mem["resident"]))
		sb.WriteString(fmt.Sprintf("  Virtual:  %v\n", mem["virtual"]))
	}

	// Network
	if net, ok := r["network"].(bson.M); ok {
		sb.WriteString("\n--- Network ---\n")
		sb.WriteString(fmt.Sprintf("  Bytes In:       %v\n", net["bytesIn"]))
		sb.WriteString(fmt.Sprintf("  Bytes Out:      %v\n", net["bytesOut"]))
		sb.WriteString(fmt.Sprintf("  Requests:       %v\n", net["numRequests"]))
	}

	return sb.String()
}

func formatCurrentOp(r bson.M) string {
	var sb strings.Builder
	sb.WriteString("=== MongoDB Current Operations ===\n\n")

	inprog, _ := r["inprog"].(bson.A)
	if len(inprog) == 0 {
		sb.WriteString("No active operations.\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Active operations: %d\n\n", len(inprog)))

	for i, raw := range inprog {
		op, ok := raw.(bson.M)
		if !ok {
			continue
		}
		sb.WriteString(fmt.Sprintf("--- Operation %d ---\n", i+1))
		sb.WriteString(fmt.Sprintf("  OpID:         %v\n", op["opid"]))
		sb.WriteString(fmt.Sprintf("  Type:         %v\n", op["type"]))
		sb.WriteString(fmt.Sprintf("  Namespace:    %v\n", op["ns"]))
		sb.WriteString(fmt.Sprintf("  Running for:  %v sec\n", op["secs_running"]))
		sb.WriteString(fmt.Sprintf("  Op:           %v\n", op["op"]))

		if desc, ok := op["desc"].(string); ok {
			sb.WriteString(fmt.Sprintf("  Description:  %s\n", desc))
		}
		if planSummary, ok := op["planSummary"].(string); ok {
			sb.WriteString(fmt.Sprintf("  Plan Summary: %s\n", planSummary))
		}
		if cmd, ok := op["command"].(bson.M); ok {
			cmdJSON, _ := json.Marshal(cmd)
			if len(cmdJSON) > 500 {
				cmdJSON = append(cmdJSON[:497], []byte("...")...)
			}
			sb.WriteString(fmt.Sprintf("  Command:      %s\n", string(cmdJSON)))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func formatRSStatus(r bson.M) string {
	var sb strings.Builder
	sb.WriteString("=== Replica Set Status ===\n\n")

	sb.WriteString(fmt.Sprintf("Set:     %v\n", r["set"]))
	sb.WriteString(fmt.Sprintf("MyState: %v\n", r["myState"]))

	if date, ok := r["date"].(time.Time); ok {
		sb.WriteString(fmt.Sprintf("Date:    %s\n", date.Format(time.RFC3339)))
	}

	members, _ := r["members"].(bson.A)
	if len(members) == 0 {
		sb.WriteString("\nNo members found.\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("\nMembers: %d\n\n", len(members)))

	for _, raw := range members {
		member, ok := raw.(bson.M)
		if !ok {
			continue
		}
		sb.WriteString(fmt.Sprintf("  [%v] %v\n", member["_id"], member["name"]))
		sb.WriteString(fmt.Sprintf("    State:       %v (%v)\n", member["stateStr"], member["state"]))
		sb.WriteString(fmt.Sprintf("    Health:      %v\n", member["health"]))

		if optime, ok := member["optimeDate"].(time.Time); ok {
			sb.WriteString(fmt.Sprintf("    Optime Date: %s\n", optime.Format(time.RFC3339)))
		}
		if lastHB, ok := member["lastHeartbeat"].(time.Time); ok {
			sb.WriteString(fmt.Sprintf("    Last HB:     %s\n", lastHB.Format(time.RFC3339)))
		}
		if pingMs, ok := member["pingMs"]; ok {
			sb.WriteString(fmt.Sprintf("    Ping:        %v ms\n", pingMs))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func formatDBStats(r bson.M) string {
	var sb strings.Builder
	sb.WriteString("=== Database Stats ===\n\n")

	sb.WriteString(fmt.Sprintf("Database:     %v\n", r["db"]))
	sb.WriteString(fmt.Sprintf("Collections:  %v\n", r["collections"]))
	sb.WriteString(fmt.Sprintf("Views:        %v\n", r["views"]))
	sb.WriteString(fmt.Sprintf("Objects:      %v\n", r["objects"]))
	sb.WriteString(fmt.Sprintf("Avg Obj Size: %s\n", formatBytes(getFloat64(r, "avgObjSize"))))
	sb.WriteString(fmt.Sprintf("Data Size:    %s\n", formatBytes(getFloat64(r, "dataSize"))))
	sb.WriteString(fmt.Sprintf("Storage Size: %s\n", formatBytes(getFloat64(r, "storageSize"))))
	sb.WriteString(fmt.Sprintf("Index Count:  %v\n", r["indexes"]))
	sb.WriteString(fmt.Sprintf("Index Size:   %s\n", formatBytes(getFloat64(r, "indexSize"))))
	sb.WriteString(fmt.Sprintf("Total Size:   %s\n", formatBytes(getFloat64(r, "totalSize"))))

	return sb.String()
}

func formatTop(r bson.M) string {
	var sb strings.Builder
	sb.WriteString("=== MongoDB Top (busiest collections) ===\n\n")

	totals, ok := r["totals"].(bson.M)
	if !ok {
		sb.WriteString("No top data available.\n")
		return sb.String()
	}

	type collStat struct {
		name      string
		totalTime float64
	}

	var stats []collStat
	for ns, raw := range totals {
		// Skip the "note" key that top returns.
		if ns == "note" {
			continue
		}
		data, ok := raw.(bson.M)
		if !ok {
			continue
		}
		var totalTime float64
		if total, ok := data["total"].(bson.M); ok {
			totalTime = getFloat64(total, "time")
		}
		stats = append(stats, collStat{name: ns, totalTime: totalTime})
	}

	sort.Slice(stats, func(i, j int) bool {
		return stats[i].totalTime > stats[j].totalTime
	})

	limit := 10
	if len(stats) < limit {
		limit = len(stats)
	}

	sb.WriteString(fmt.Sprintf("%-50s %15s\n", "Collection", "Total Time (us)"))
	sb.WriteString(strings.Repeat("-", 66) + "\n")

	for _, s := range stats[:limit] {
		sb.WriteString(fmt.Sprintf("%-50s %15.0f\n", s.name, s.totalTime))
	}

	if len(stats) > limit {
		sb.WriteString(fmt.Sprintf("\n... and %d more collections\n", len(stats)-limit))
	}

	return sb.String()
}

func formatCollectionStats(r bson.M) string {
	var sb strings.Builder
	sb.WriteString("=== Collection Stats ===\n\n")

	sb.WriteString(fmt.Sprintf("Namespace:    %v\n", r["ns"]))
	sb.WriteString(fmt.Sprintf("Documents:    %v\n", r["count"]))
	sb.WriteString(fmt.Sprintf("Avg Obj Size: %s\n", formatBytes(getFloat64(r, "avgObjSize"))))
	sb.WriteString(fmt.Sprintf("Data Size:    %s\n", formatBytes(getFloat64(r, "size"))))
	sb.WriteString(fmt.Sprintf("Storage Size: %s\n", formatBytes(getFloat64(r, "storageSize"))))
	sb.WriteString(fmt.Sprintf("Indexes:      %v\n", r["nindexes"]))
	sb.WriteString(fmt.Sprintf("Index Size:   %s\n", formatBytes(getFloat64(r, "totalIndexSize"))))

	if indexSizes, ok := r["indexSizes"].(bson.M); ok && len(indexSizes) > 0 {
		sb.WriteString("\n--- Index Sizes ---\n")
		for name, size := range indexSizes {
			sb.WriteString(fmt.Sprintf("  %-30s %s\n", name+":", formatBytes(toFloat64(size))))
		}
	}

	return sb.String()
}

// --- Helpers ---

func getFloat64(m bson.M, key string) float64 {
	return toFloat64(m[key])
}

func toFloat64(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case int:
		return float64(n)
	default:
		return 0
	}
}

func formatBytes(b float64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
		tb = gb * 1024
	)
	switch {
	case b >= tb:
		return fmt.Sprintf("%.2f TB", b/tb)
	case b >= gb:
		return fmt.Sprintf("%.2f GB", b/gb)
	case b >= mb:
		return fmt.Sprintf("%.2f MB", b/mb)
	case b >= kb:
		return fmt.Sprintf("%.2f KB", b/kb)
	default:
		return fmt.Sprintf("%.0f B", b)
	}
}

func formatUptime(seconds float64) string {
	d := int(seconds) / 86400
	h := (int(seconds) % 86400) / 3600
	m := (int(seconds) % 3600) / 60
	s := int(seconds) % 60

	if d > 0 {
		return fmt.Sprintf("%dd %dh %dm %ds", d, h, m, s)
	}
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	return fmt.Sprintf("%dm %ds", m, s)
}
