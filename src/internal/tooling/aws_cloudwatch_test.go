package tooling

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shchepetkov/sherlockops/internal/domain"
)

// ---------------------------------------------------------------------------
// SigV4 signing tests
// ---------------------------------------------------------------------------

func TestSignRequest_KnownVector(t *testing.T) {
	p := sigV4Params{
		method:      "POST",
		host:        "monitoring.us-east-1.amazonaws.com",
		uri:         "/",
		queryString: "",
		headers: map[string]string{
			"Host":         "monitoring.us-east-1.amazonaws.com",
			"Content-Type": "application/x-www-form-urlencoded; charset=utf-8",
			"X-Amz-Date":  "20240115T120000Z",
		},
		body:      []byte("Action=DescribeAlarms&Version=2010-08-01"),
		service:   "monitoring",
		region:    "us-east-1",
		accessKey: "AKIAIOSFODNN7EXAMPLE",
		secretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	p.now = mustParseTime(t, "20240115T120000Z")

	auth := signRequest(p)

	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20240115/us-east-1/monitoring/aws4_request") {
		t.Errorf("unexpected credential prefix in auth header:\n%s", auth)
	}
	if !strings.Contains(auth, "SignedHeaders=content-type;host;x-amz-date") {
		t.Errorf("unexpected signed headers in auth header:\n%s", auth)
	}
	if !strings.Contains(auth, "Signature=") {
		t.Errorf("missing Signature in auth header:\n%s", auth)
	}

	sigIdx := strings.Index(auth, "Signature=")
	sig := auth[sigIdx+len("Signature="):]
	if len(sig) != 64 {
		t.Errorf("expected 64-char hex signature, got %d chars: %s", len(sig), sig)
	}
}

func TestDeriveSigningKey(t *testing.T) {
	key := deriveSigningKey("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "20240115", "us-east-1", "monitoring")
	if len(key) != 32 {
		t.Errorf("expected 32-byte signing key, got %d bytes", len(key))
	}
}

func TestSha256Hex(t *testing.T) {
	result := sha256Hex([]byte(""))
	expected := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if result != expected {
		t.Errorf("sha256Hex empty = %s, want %s", result, expected)
	}
}

func TestSignRequest_DifferentPayloads(t *testing.T) {
	base := sigV4Params{
		method:    "POST",
		host:      "monitoring.us-east-1.amazonaws.com",
		uri:       "/",
		service:   "monitoring",
		region:    "us-east-1",
		accessKey: "AKID",
		secretKey: "SECRET",
	}
	base.now = mustParseTime(t, "20240115T120000Z")
	base.headers = map[string]string{
		"Host":        "monitoring.us-east-1.amazonaws.com",
		"X-Amz-Date": "20240115T120000Z",
	}

	base.body = []byte("body1")
	sig1 := signRequest(base)

	base.body = []byte("body2")
	sig2 := signRequest(base)

	if sig1 == sig2 {
		t.Error("different payloads should produce different signatures")
	}
}

// ---------------------------------------------------------------------------
// ListTools test
// ---------------------------------------------------------------------------

func TestAWSCloudWatchExecutor_ListTools(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewAWSCloudWatchExecutor("us-east-1", "AKID", "SECRET", logger)

	tools, err := exec.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error: %v", err)
	}
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	expected := []string{
		"aws_cloudwatch_get_metrics",
		"aws_cloudwatch_describe_alarms",
		"aws_cloudwatch_get_log_events",
		"aws_ec2_describe_instances",
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestAWSCloudWatchExecutor_UnknownTool(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewAWSCloudWatchExecutor("us-east-1", "AKID", "SECRET", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-1",
		Name: "nonexistent_tool",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for unknown tool")
	}
}

// ---------------------------------------------------------------------------
// GetMetricData test
// ---------------------------------------------------------------------------

func TestAWSCloudWatchExecutor_GetMetrics(t *testing.T) {
	srv := newTestCloudWatchServer(t)
	defer srv.Close()

	exec := newTestAWSExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-metrics",
		Name: "aws_cloudwatch_get_metrics",
		Input: map[string]interface{}{
			"namespace":   "AWS/EC2",
			"metric_name": "CPUUtilization",
			"dimensions":  map[string]interface{}{"InstanceId": "i-1234567890abcdef0"},
			"period":      float64(300),
			"stat":        "Average",
			"start":       "-1h",
			"end":         "now",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "CPUUtilization") {
		t.Errorf("expected CPUUtilization in response, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Datapoints: 2") {
		t.Errorf("expected 2 datapoints, got: %s", result.Content)
	}
}

func TestAWSCloudWatchExecutor_GetMetrics_MissingParams(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewAWSCloudWatchExecutor("us-east-1", "AKID", "SECRET", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:    "call-missing",
		Name:  "aws_cloudwatch_get_metrics",
		Input: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing params")
	}
}

// ---------------------------------------------------------------------------
// DescribeAlarms test
// ---------------------------------------------------------------------------

func TestAWSCloudWatchExecutor_DescribeAlarms(t *testing.T) {
	srv := newTestCloudWatchServer(t)
	defer srv.Close()

	exec := newTestAWSExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-alarms",
		Name: "aws_cloudwatch_describe_alarms",
		Input: map[string]interface{}{
			"alarm_name_prefix": "HighCPU",
			"state_value":       "ALARM",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "HighCPU") {
		t.Errorf("expected HighCPU alarm in response, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "ALARM") {
		t.Errorf("expected ALARM state in response, got: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// DescribeInstances test
// ---------------------------------------------------------------------------

func TestAWSCloudWatchExecutor_DescribeInstances(t *testing.T) {
	srv := newTestCloudWatchServer(t)
	defer srv.Close()

	exec := newTestAWSExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-ec2",
		Name: "aws_ec2_describe_instances",
		Input: map[string]interface{}{
			"instance_ids": []interface{}{"i-1234567890abcdef0"},
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "i-1234567890abcdef0") {
		t.Errorf("expected instance ID in response, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "running") {
		t.Errorf("expected running state, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "10.0.1.100") {
		t.Errorf("expected private IP, got: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// GetLogEvents test
// ---------------------------------------------------------------------------

func TestAWSCloudWatchExecutor_GetLogEvents(t *testing.T) {
	srv := newTestCloudWatchServer(t)
	defer srv.Close()

	exec := newTestAWSExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-logs",
		Name: "aws_cloudwatch_get_log_events",
		Input: map[string]interface{}{
			"log_group":  "/aws/ecs/myapp",
			"log_stream": "stream-1",
			"start":      "-30m",
			"limit":      float64(100),
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Log Events: 2") {
		t.Errorf("expected 2 log events, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "NullPointerException") {
		t.Errorf("expected log message in response, got: %s", result.Content)
	}
}

func TestAWSCloudWatchExecutor_GetLogEvents_MissingParams(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewAWSCloudWatchExecutor("us-east-1", "AKID", "SECRET", logger)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-logs-missing",
		Name: "aws_cloudwatch_get_log_events",
		Input: map[string]interface{}{
			"log_group": "/aws/ecs/myapp",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing params")
	}
}

func TestAWSCloudWatchExecutor_GetLogEvents_NoStart(t *testing.T) {
	srv := newTestCloudWatchServer(t)
	defer srv.Close()

	exec := newTestAWSExecutor(t, srv.URL)

	result, err := exec.Execute(context.Background(), domain.ToolCall{
		ID:   "call-logs-nostart",
		Name: "aws_cloudwatch_get_log_events",
		Input: map[string]interface{}{
			"log_group":  "/aws/ecs/myapp",
			"log_stream": "stream-1",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// Format function tests
// ---------------------------------------------------------------------------

func TestFormatGetMetricData(t *testing.T) {
	xmlBody := `<?xml version="1.0" encoding="UTF-8"?>
<GetMetricDataResponse xmlns="http://monitoring.amazonaws.com/doc/2010-08-01/">
  <GetMetricDataResult>
    <MetricDataResults>
      <member>
        <Id>m1</Id>
        <Label>CPUUtilization</Label>
        <StatusCode>Complete</StatusCode>
        <Timestamps>
          <member>2024-01-15T11:00:00Z</member>
          <member>2024-01-15T11:05:00Z</member>
        </Timestamps>
        <Values>
          <member>45.5</member>
          <member>52.3</member>
        </Values>
      </member>
    </MetricDataResults>
  </GetMetricDataResult>
</GetMetricDataResponse>`

	result, err := formatGetMetricData([]byte(xmlBody), "CPUUtilization", "Average")
	if err != nil {
		t.Fatalf("format error: %v", err)
	}
	if !strings.Contains(result, "CPUUtilization") {
		t.Errorf("expected CPUUtilization, got: %s", result)
	}
	if !strings.Contains(result, "45.5") {
		t.Errorf("expected 45.5, got: %s", result)
	}
	if !strings.Contains(result, "Datapoints: 2") {
		t.Errorf("expected Datapoints: 2, got: %s", result)
	}
}

func TestFormatGetMetricData_Empty(t *testing.T) {
	xmlBody := `<?xml version="1.0" encoding="UTF-8"?>
<GetMetricDataResponse xmlns="http://monitoring.amazonaws.com/doc/2010-08-01/">
  <GetMetricDataResult>
    <MetricDataResults>
      <member>
        <Id>m1</Id>
        <Label>CPUUtilization</Label>
        <StatusCode>Complete</StatusCode>
        <Timestamps/>
        <Values/>
      </member>
    </MetricDataResults>
  </GetMetricDataResult>
</GetMetricDataResponse>`

	result, err := formatGetMetricData([]byte(xmlBody), "CPUUtilization", "Average")
	if err != nil {
		t.Fatalf("format error: %v", err)
	}
	if !strings.Contains(result, "no datapoints") {
		t.Errorf("expected no datapoints message, got: %s", result)
	}
}

func TestFormatDescribeAlarms(t *testing.T) {
	xmlBody := `<?xml version="1.0" encoding="UTF-8"?>
<DescribeAlarmsResponse xmlns="http://monitoring.amazonaws.com/doc/2010-08-01/">
  <DescribeAlarmsResult>
    <MetricAlarms>
      <member>
        <AlarmName>HighCPU</AlarmName>
        <StateValue>ALARM</StateValue>
        <MetricName>CPUUtilization</MetricName>
        <Namespace>AWS/EC2</Namespace>
        <ComparisonOperator>GreaterThanThreshold</ComparisonOperator>
        <Threshold>80</Threshold>
        <StateUpdatedTimestamp>2024-01-15T12:00:00Z</StateUpdatedTimestamp>
        <AlarmDescription>CPU too high</AlarmDescription>
      </member>
    </MetricAlarms>
  </DescribeAlarmsResult>
</DescribeAlarmsResponse>`

	result, err := formatDescribeAlarms([]byte(xmlBody))
	if err != nil {
		t.Fatalf("format error: %v", err)
	}
	if !strings.Contains(result, "HighCPU") {
		t.Errorf("expected HighCPU, got: %s", result)
	}
	if !strings.Contains(result, "CPU too high") {
		t.Errorf("expected description, got: %s", result)
	}
}

func TestFormatDescribeAlarms_Empty(t *testing.T) {
	xmlBody := `<?xml version="1.0" encoding="UTF-8"?>
<DescribeAlarmsResponse xmlns="http://monitoring.amazonaws.com/doc/2010-08-01/">
  <DescribeAlarmsResult>
    <MetricAlarms/>
  </DescribeAlarmsResult>
</DescribeAlarmsResponse>`

	result, err := formatDescribeAlarms([]byte(xmlBody))
	if err != nil {
		t.Fatalf("format error: %v", err)
	}
	if !strings.Contains(result, "0 found") {
		t.Errorf("expected 0 found, got: %s", result)
	}
	if !strings.Contains(result, "no alarms") {
		t.Errorf("expected no alarms message, got: %s", result)
	}
}

func TestFormatDescribeInstances(t *testing.T) {
	xmlBody := `<?xml version="1.0" encoding="UTF-8"?>
<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/">
  <reservationSet>
    <item>
      <instancesSet>
        <item>
          <instanceId>i-abc123</instanceId>
          <instanceType>t3.micro</instanceType>
          <placement><availabilityZone>us-east-1a</availabilityZone></placement>
          <instanceState><name>running</name></instanceState>
          <privateIpAddress>10.0.0.1</privateIpAddress>
          <ipAddress>54.1.2.3</ipAddress>
          <launchTime>2024-01-01T00:00:00Z</launchTime>
        </item>
      </instancesSet>
    </item>
  </reservationSet>
</DescribeInstancesResponse>`

	result, err := formatDescribeInstances([]byte(xmlBody))
	if err != nil {
		t.Fatalf("format error: %v", err)
	}
	if !strings.Contains(result, "i-abc123") {
		t.Errorf("expected i-abc123, got: %s", result)
	}
	if !strings.Contains(result, "54.1.2.3") {
		t.Errorf("expected public IP, got: %s", result)
	}
}

func TestFormatGetLogEvents(t *testing.T) {
	body := `{"events":[{"timestamp":1705320000000,"message":"ERROR: NullPointerException in handler","ingestionTime":1705320001000},{"timestamp":1705320060000,"message":"WARN: retry attempt 3","ingestionTime":1705320061000}]}`

	result, err := formatGetLogEvents([]byte(body))
	if err != nil {
		t.Fatalf("format error: %v", err)
	}
	if !strings.Contains(result, "Log Events: 2") {
		t.Errorf("expected 2 events, got: %s", result)
	}
	if !strings.Contains(result, "NullPointerException") {
		t.Errorf("expected message, got: %s", result)
	}
}

func TestFormatGetLogEvents_Empty(t *testing.T) {
	body := `{"events":[]}`

	result, err := formatGetLogEvents([]byte(body))
	if err != nil {
		t.Fatalf("format error: %v", err)
	}
	if !strings.Contains(result, "no log events") {
		t.Errorf("expected no log events message, got: %s", result)
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	tt, err := time.Parse("20060102T150405Z", s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return tt
}

// newTestAWSExecutor creates an executor that points at a local httptest server.
func newTestAWSExecutor(t *testing.T, serverURL string) *AWSCloudWatchExecutor {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exec := NewAWSCloudWatchExecutor("us-east-1", "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", logger)
	exec.client = &http.Client{Timeout: 5 * time.Second}
	exec.client.Transport = &testRedirectTransport{
		target:    serverURL,
		transport: http.DefaultTransport,
	}
	return exec
}

// testRedirectTransport rewrites request URLs to point at the test server.
type testRedirectTransport struct {
	target    string
	transport http.RoundTripper
}

func (t *testRedirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(t.target)
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host
	return t.transport.RoundTrip(req)
}

// newTestCloudWatchServer creates an httptest server that handles all AWS API actions.
func newTestCloudWatchServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get("Content-Type")

		// JSON API (CloudWatch Logs)
		if strings.Contains(contentType, "x-amz-json") {
			target := r.Header.Get("X-Amz-Target")
			handleLogsRequest(w, target)
			return
		}

		// Form-encoded API (CloudWatch, EC2)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}

		action := r.FormValue("Action")
		switch action {
		case "GetMetricData":
			handleGetMetricData(w)
		case "DescribeAlarms":
			handleDescribeAlarms(w)
		case "DescribeInstances":
			handleDescribeInstances(w)
		default:
			http.Error(w, "unknown action: "+action, http.StatusBadRequest)
		}
	}))
}

func handleGetMetricData(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/xml")
	type member struct {
		ID         string    `xml:"Id"`
		Label      string    `xml:"Label"`
		StatusCode string    `xml:"StatusCode"`
		Timestamps struct {
			Members []string `xml:"member"`
		} `xml:"Timestamps"`
		Values struct {
			Members []float64 `xml:"member"`
		} `xml:"Values"`
	}
	type result struct {
		XMLName xml.Name `xml:"GetMetricDataResponse"`
		Result  struct {
			MetricDataResults struct {
				Members []member `xml:"member"`
			} `xml:"MetricDataResults"`
		} `xml:"GetMetricDataResult"`
	}

	resp := result{}
	m := member{
		ID:         "m1",
		Label:      "CPUUtilization",
		StatusCode: "Complete",
	}
	m.Timestamps.Members = []string{"2024-01-15T11:00:00Z", "2024-01-15T11:05:00Z"}
	m.Values.Members = []float64{45.5, 52.3}
	resp.Result.MetricDataResults.Members = []member{m}

	xml.NewEncoder(w).Encode(resp)
}

func handleDescribeAlarms(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/xml")
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<DescribeAlarmsResponse xmlns="http://monitoring.amazonaws.com/doc/2010-08-01/">
  <DescribeAlarmsResult>
    <MetricAlarms>
      <member>
        <AlarmName>HighCPU</AlarmName>
        <StateValue>ALARM</StateValue>
        <MetricName>CPUUtilization</MetricName>
        <Namespace>AWS/EC2</Namespace>
        <ComparisonOperator>GreaterThanThreshold</ComparisonOperator>
        <Threshold>80</Threshold>
        <StateUpdatedTimestamp>2024-01-15T12:00:00Z</StateUpdatedTimestamp>
        <AlarmDescription>CPU usage exceeds 80%</AlarmDescription>
      </member>
    </MetricAlarms>
  </DescribeAlarmsResult>
</DescribeAlarmsResponse>`))
}

func handleDescribeInstances(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/xml")
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/">
  <reservationSet>
    <item>
      <instancesSet>
        <item>
          <instanceId>i-1234567890abcdef0</instanceId>
          <instanceType>t3.medium</instanceType>
          <placement>
            <availabilityZone>us-east-1a</availabilityZone>
          </placement>
          <instanceState>
            <name>running</name>
          </instanceState>
          <privateIpAddress>10.0.1.100</privateIpAddress>
          <ipAddress>54.123.45.67</ipAddress>
          <launchTime>2024-01-01T00:00:00Z</launchTime>
        </item>
      </instancesSet>
    </item>
  </reservationSet>
</DescribeInstancesResponse>`))
}

func handleLogsRequest(w http.ResponseWriter, target string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")

	switch target {
	case "Logs_20140328.GetLogEvents":
		json.NewEncoder(w).Encode(map[string]interface{}{
			"events": []map[string]interface{}{
				{
					"timestamp":     1705320000000,
					"message":       "ERROR: NullPointerException in handler",
					"ingestionTime": 1705320001000,
				},
				{
					"timestamp":     1705320060000,
					"message":       "WARN: timeout after 30s",
					"ingestionTime": 1705320061000,
				},
			},
			"nextForwardToken":  "f/abc123",
			"nextBackwardToken": "b/abc123",
		})

	default:
		http.Error(w, "unknown target: "+target, http.StatusBadRequest)
	}
}

func TestFormatMetricStatistics(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<GetMetricStatisticsResponse>
  <GetMetricStatisticsResult>
    <Label>CPUUtilization</Label>
    <Datapoints>
      <member>
        <Timestamp>2024-01-15T12:00:00Z</Timestamp>
        <Average>45.5</Average>
        <Sum>182.0</Sum>
        <Minimum>10.0</Minimum>
        <Maximum>90.0</Maximum>
        <Unit>Percent</Unit>
      </member>
      <member>
        <Timestamp>2024-01-15T12:05:00Z</Timestamp>
        <Average>60.2</Average>
        <Sum>240.8</Sum>
        <Minimum>20.0</Minimum>
        <Maximum>95.0</Maximum>
        <Unit>Percent</Unit>
      </member>
    </Datapoints>
  </GetMetricStatisticsResult>
</GetMetricStatisticsResponse>`

	tests := []struct {
		name       string
		stat       string
		expectVal  string
	}{
		{"average", "Average", "45.5000"},
		{"sum", "Sum", "182.0000"},
		{"minimum", "Minimum", "10.0000"},
		{"maximum", "Maximum", "90.0000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := formatMetricStatistics([]byte(xmlData), "CPUUtilization", tt.stat)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(result, "CPUUtilization") {
				t.Errorf("expected metric name in output, got: %s", result)
			}
			if !strings.Contains(result, "Datapoints: 2") {
				t.Errorf("expected 'Datapoints: 2' in output, got: %s", result)
			}
			if !strings.Contains(result, tt.expectVal) {
				t.Errorf("expected value %q in output, got: %s", tt.expectVal, result)
			}
		})
	}
}

func TestFormatMetricStatistics_InvalidXML(t *testing.T) {
	_, err := formatMetricStatistics([]byte("not xml"), "test", "Average")
	if err == nil {
		t.Error("expected error for invalid XML")
	}
}

func TestFormatLogsQueryResults(t *testing.T) {
	data := map[string]interface{}{
		"status": "Complete",
		"results": [][]map[string]string{
			{
				{"field": "@timestamp", "value": "2024-01-15 12:00:00"},
				{"field": "@message", "value": "error: timeout"},
				{"field": "@ptr", "value": "internal-ptr-123"},
			},
			{
				{"field": "@timestamp", "value": "2024-01-15 12:01:00"},
				{"field": "@message", "value": "info: recovered"},
			},
		},
	}
	body, _ := json.Marshal(data)

	result, err := formatLogsQueryResults(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Status: Complete") {
		t.Errorf("expected 'Status: Complete' in output, got: %s", result)
	}
	if !strings.Contains(result, "Results: 2") {
		t.Errorf("expected 'Results: 2' in output, got: %s", result)
	}
	if !strings.Contains(result, "error: timeout") {
		t.Errorf("expected log message in output, got: %s", result)
	}
	if strings.Contains(result, "internal-ptr-123") {
		t.Errorf("@ptr field should be excluded, got: %s", result)
	}
}

func TestFormatLogsQueryResults_Empty(t *testing.T) {
	data := map[string]interface{}{
		"status":  "Complete",
		"results": []interface{}{},
	}
	body, _ := json.Marshal(data)

	result, err := formatLogsQueryResults(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "no log entries found") {
		t.Errorf("expected 'no log entries found', got: %s", result)
	}
}

func TestFormatLogsQueryResults_InvalidJSON(t *testing.T) {
	_, err := formatLogsQueryResults([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
