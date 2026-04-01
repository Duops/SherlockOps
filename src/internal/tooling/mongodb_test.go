package tooling

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Duops/SherlockOps/internal/domain"
)

func newTestMongoLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestMongoDBExecutor_ListTools(t *testing.T) {
	// ListTools does not require a live connection; build the executor manually.
	exec := &MongoDBExecutor{
		uri:    "mongodb://localhost:27017",
		logger: newTestMongoLogger(t),
	}

	tools, err := exec.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error: %v", err)
	}

	expectedNames := map[string]bool{
		"mongo_server_status":    true,
		"mongo_current_op":       true,
		"mongo_rs_status":        true,
		"mongo_db_stats":         true,
		"mongo_top":              true,
		"mongo_collection_stats": true,
	}

	if len(tools) != len(expectedNames) {
		t.Fatalf("expected %d tools, got %d", len(expectedNames), len(tools))
	}

	for _, tool := range tools {
		if !expectedNames[tool.Name] {
			t.Errorf("unexpected tool name: %s", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("tool %s has empty description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %s has nil input schema", tool.Name)
		}
	}
}

func TestMongoDBExecutor_UnknownTool(t *testing.T) {
	exec := &MongoDBExecutor{
		uri:    "mongodb://localhost:27017",
		logger: newTestMongoLogger(t),
	}

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-1",
		Name: "unknown_tool",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for unknown tool")
	}
	if result.Content != "unknown tool: unknown_tool" {
		t.Errorf("unexpected error content: %s", result.Content)
	}
}

func TestMongoDBExecutor_DbStats_MissingDatabase(t *testing.T) {
	exec := &MongoDBExecutor{
		uri:    "mongodb://localhost:27017",
		logger: newTestMongoLogger(t),
	}

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-2",
		Name:  "mongo_db_stats",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing database parameter")
	}
	if result.Content != "missing required parameter: database" {
		t.Errorf("unexpected content: %s", result.Content)
	}
}

func TestMongoDBExecutor_CollectionStats_MissingParams(t *testing.T) {
	exec := &MongoDBExecutor{
		uri:    "mongodb://localhost:27017",
		logger: newTestMongoLogger(t),
	}

	tests := []struct {
		name  string
		input map[string]interface{}
	}{
		{"missing both", map[string]interface{}{}},
		{"missing collection", map[string]interface{}{"database": "mydb"}},
		{"missing database", map[string]interface{}{"collection": "users"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := exec.Execute(context.Background(), domain.ToolCall{
				ID:    "call-3",
				Name:  "mongo_collection_stats",
				Input: tt.input,
			})
			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			if !result.IsError {
				t.Error("expected error for missing parameters")
			}
		})
	}
}

func TestFormatServerStatus(t *testing.T) {
	status := bson.M{
		"host":         "mongo-primary:27017",
		"version":      "7.0.5",
		"uptimeMillis": int64(86400000), // 1 day
		"connections": bson.M{
			"current":      int32(42),
			"available":    int32(51158),
			"totalCreated": int64(1234),
		},
		"opcounters": bson.M{
			"insert":  int64(100),
			"query":   int64(5000),
			"update":  int64(200),
			"delete":  int64(10),
			"getmore": int64(300),
			"command": int64(9000),
		},
		"mem": bson.M{
			"resident": int32(512),
			"virtual":  int32(1024),
		},
		"network": bson.M{
			"bytesIn":     int64(123456789),
			"bytesOut":    int64(987654321),
			"numRequests": int64(50000),
		},
	}

	output := formatServerStatus(status)

	checks := []string{
		"MongoDB Server Status",
		"mongo-primary:27017",
		"7.0.5",
		"1d 0h 0m 0s",
		"Current:   42",
		"Available: 51158",
		"query:     5000",
		"Resident: 512",
		"Virtual:  1024",
		"Bytes In:",
		"Requests:",
	}
	for _, check := range checks {
		if !containsStr(output, check) {
			t.Errorf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestFormatCurrentOp_Empty(t *testing.T) {
	result := bson.M{
		"inprog": bson.A{},
	}
	output := formatCurrentOp(result)
	if !containsStr(output, "No active operations") {
		t.Errorf("expected 'No active operations', got:\n%s", output)
	}
}

func TestFormatCurrentOp_WithOps(t *testing.T) {
	result := bson.M{
		"inprog": bson.A{
			bson.M{
				"opid":         int32(12345),
				"type":         "op",
				"ns":           "mydb.users",
				"secs_running": int32(5),
				"op":           "query",
				"desc":         "conn1234",
				"planSummary":  "IXSCAN { _id: 1 }",
			},
		},
	}
	output := formatCurrentOp(result)

	checks := []string{"12345", "mydb.users", "5 sec", "query", "IXSCAN"}
	for _, check := range checks {
		if !containsStr(output, check) {
			t.Errorf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestFormatDBStats(t *testing.T) {
	stats := bson.M{
		"db":          "mydb",
		"collections": int32(15),
		"views":       int32(2),
		"objects":     int64(100000),
		"avgObjSize":  float64(256),
		"dataSize":    float64(25600000),
		"storageSize": float64(30000000),
		"indexes":     int32(20),
		"indexSize":   float64(5000000),
		"totalSize":   float64(35000000),
	}

	output := formatDBStats(stats)

	checks := []string{"mydb", "Collections:  15", "Objects:      100000", "MB"}
	for _, check := range checks {
		if !containsStr(output, check) {
			t.Errorf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestFormatTop(t *testing.T) {
	result := bson.M{
		"totals": bson.M{
			"note": "times in microseconds",
			"mydb.users": bson.M{
				"total": bson.M{"time": int64(50000), "count": int64(100)},
			},
			"mydb.orders": bson.M{
				"total": bson.M{"time": int64(30000), "count": int64(50)},
			},
		},
	}

	output := formatTop(result)

	if !containsStr(output, "mydb.users") {
		t.Errorf("expected mydb.users in output, got:\n%s", output)
	}
	if !containsStr(output, "mydb.orders") {
		t.Errorf("expected mydb.orders in output, got:\n%s", output)
	}
	if containsStr(output, "note") {
		t.Errorf("should not contain 'note' key in output, got:\n%s", output)
	}
}

func TestFormatCollectionStats(t *testing.T) {
	stats := bson.M{
		"ns":             "mydb.users",
		"count":          int64(50000),
		"avgObjSize":     float64(512),
		"size":           float64(25600000),
		"storageSize":    float64(30000000),
		"nindexes":       int32(3),
		"totalIndexSize": float64(2000000),
		"indexSizes": bson.M{
			"_id_":       int64(500000),
			"email_1":    int64(800000),
			"username_1": int64(700000),
		},
	}

	output := formatCollectionStats(stats)

	checks := []string{"mydb.users", "Documents:    50000", "Indexes:      3", "_id_:", "email_1:", "username_1:"}
	for _, check := range checks {
		if !containsStr(output, check) {
			t.Errorf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestFormatRSStatus_NoMembers(t *testing.T) {
	result := bson.M{
		"set":     "rs0",
		"myState": int32(1),
	}
	output := formatRSStatus(result)
	if !containsStr(output, "rs0") {
		t.Errorf("expected 'rs0' in output, got:\n%s", output)
	}
	if !containsStr(output, "No members found") {
		t.Errorf("expected 'No members found', got:\n%s", output)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    float64
		expected string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.00 KB"},
		{1048576, "1.00 MB"},
		{1073741824, "1.00 GB"},
		{1099511627776, "1.00 TB"},
	}
	for _, tt := range tests {
		result := formatBytes(tt.input)
		if result != tt.expected {
			t.Errorf("formatBytes(%f) = %s, want %s", tt.input, result, tt.expected)
		}
	}
}

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		seconds  float64
		expected string
	}{
		{65, "1m 5s"},
		{3661, "1h 1m 1s"},
		{90061, "1d 1h 1m 1s"},
	}
	for _, tt := range tests {
		result := formatUptime(tt.seconds)
		if result != tt.expected {
			t.Errorf("formatUptime(%f) = %s, want %s", tt.seconds, result, tt.expected)
		}
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		input    interface{}
		expected float64
	}{
		{float64(3.14), 3.14},
		{int32(42), 42},
		{int64(100), 100},
		{int(7), 7},
		{"not a number", 0},
		{nil, 0},
	}
	for _, tt := range tests {
		result := toFloat64(tt.input)
		if result != tt.expected {
			t.Errorf("toFloat64(%v) = %f, want %f", tt.input, result, tt.expected)
		}
	}
}

// containsStr is a helper for substring checks in test assertions.
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
