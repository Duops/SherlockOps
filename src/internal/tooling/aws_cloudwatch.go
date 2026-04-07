package tooling

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Duops/SherlockOps/internal/domain"
)

// AWSCloudWatchExecutor provides AWS CloudWatch, CloudWatch Logs, and EC2 tools.
// It uses raw HTTP with AWS Signature V4 signing to avoid the full AWS SDK dependency.
type AWSCloudWatchExecutor struct {
	region    string
	accessKey string
	secretKey string
	client    *http.Client
	logger    *slog.Logger
}

// NewAWSCloudWatchExecutor creates a new AWS CloudWatch tool executor.
func NewAWSCloudWatchExecutor(region, accessKey, secretKey string, logger *slog.Logger) *AWSCloudWatchExecutor {
	return &AWSCloudWatchExecutor{
		region:    region,
		accessKey: accessKey,
		secretKey: secretKey,
		client:    &http.Client{Timeout: 30 * time.Second},
		logger:    logger,
	}
}

// ListTools returns the available AWS tools.
func (a *AWSCloudWatchExecutor) ListTools(_ context.Context) ([]domain.Tool, error) {
	return []domain.Tool{
		{
			Name:        "aws_cloudwatch_get_metrics",
			Description: "Query CloudWatch metrics using GetMetricData for a given namespace, metric, and dimensions.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"namespace": map[string]interface{}{
						"type":        "string",
						"description": "CloudWatch namespace, e.g. AWS/EC2, AWS/RDS",
					},
					"metric_name": map[string]interface{}{
						"type":        "string",
						"description": "Metric name, e.g. CPUUtilization",
					},
					"dimensions": map[string]interface{}{
						"type":        "object",
						"description": "Metric dimensions as key-value pairs, e.g. {\"InstanceId\": \"i-123\"}",
					},
					"period": map[string]interface{}{
						"type":        "number",
						"description": "Period in seconds, e.g. 300",
					},
					"stat": map[string]interface{}{
						"type":        "string",
						"description": "Statistic: Average, Sum, Minimum, Maximum, SampleCount",
					},
					"start": map[string]interface{}{
						"type":        "string",
						"description": "Start time, e.g. \"-1h\" or RFC3339",
					},
					"end": map[string]interface{}{
						"type":        "string",
						"description": "End time, e.g. \"now\" or RFC3339",
					},
				},
				"required": []interface{}{"namespace", "metric_name", "start", "end"},
			},
		},
		{
			Name:        "aws_cloudwatch_describe_alarms",
			Description: "List CloudWatch alarms, optionally filtered by state or name prefix.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"alarm_name_prefix": map[string]interface{}{
						"type":        "string",
						"description": "Filter alarms by name prefix",
					},
					"state_value": map[string]interface{}{
						"type":        "string",
						"description": "Filter by state: OK, ALARM, INSUFFICIENT_DATA",
					},
				},
			},
		},
		{
			Name:        "aws_cloudwatch_get_log_events",
			Description: "Retrieve log events from a CloudWatch Logs log stream using GetLogEvents.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"log_group": map[string]interface{}{
						"type":        "string",
						"description": "Log group name, e.g. /aws/ecs/myapp",
					},
					"log_stream": map[string]interface{}{
						"type":        "string",
						"description": "Log stream name, e.g. stream-1",
					},
					"start": map[string]interface{}{
						"type":        "string",
						"description": "Start time, e.g. \"-30m\" or RFC3339",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"description": "Maximum number of log events to return (default 100)",
					},
				},
				"required": []interface{}{"log_group", "log_stream"},
			},
		},
		{
			Name:        "aws_ec2_describe_instances",
			Description: "Get EC2 instance information including state, type, IPs, and availability zone.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"instance_ids": map[string]interface{}{
						"type":        "array",
						"description": "List of instance IDs to describe",
						"items":       map[string]interface{}{"type": "string"},
					},
				},
				"required": []interface{}{"instance_ids"},
			},
		},
	}, nil
}

// Execute runs an AWS tool call.
func (a *AWSCloudWatchExecutor) Execute(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	switch call.Name {
	case "aws_cloudwatch_get_metrics":
		return a.getMetrics(ctx, call)
	case "aws_cloudwatch_describe_alarms":
		return a.describeAlarms(ctx, call)
	case "aws_cloudwatch_get_log_events":
		return a.getLogEvents(ctx, call)
	case "aws_ec2_describe_instances":
		return a.describeInstances(ctx, call)
	default:
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("unknown tool: %s", call.Name),
			IsError: true,
		}, nil
	}
}

// ---------------------------------------------------------------------------
// Tool implementations
// ---------------------------------------------------------------------------

func (a *AWSCloudWatchExecutor) getMetrics(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	namespace, _ := call.Input["namespace"].(string)
	metricName, _ := call.Input["metric_name"].(string)
	startStr, _ := call.Input["start"].(string)
	endStr, _ := call.Input["end"].(string)

	if namespace == "" || metricName == "" || startStr == "" || endStr == "" {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: "missing required parameters: namespace, metric_name, start, end",
			IsError: true,
		}, nil
	}

	now := time.Now()
	startTime, err := parseRelativeTime(startStr, now)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("invalid start time: %v", err), IsError: true}, nil
	}
	endTime, err := parseRelativeTime(endStr, now)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("invalid end time: %v", err), IsError: true}, nil
	}

	period := 300
	if p, ok := call.Input["period"].(float64); ok && p > 0 {
		period = int(p)
	}
	stat := "Average"
	if s, _ := call.Input["stat"].(string); s != "" {
		stat = s
	}

	// Build GetMetricData request using form-encoded query parameters.
	// MetricDataQueries is a list with one entry (member.1).
	params := url.Values{
		"Action":    {"GetMetricData"},
		"Version":   {"2010-08-01"},
		"StartTime": {startTime.UTC().Format(time.RFC3339)},
		"EndTime":   {endTime.UTC().Format(time.RFC3339)},

		"MetricDataQueries.member.1.Id":                        {"m1"},
		"MetricDataQueries.member.1.MetricStat.Metric.Namespace":  {namespace},
		"MetricDataQueries.member.1.MetricStat.Metric.MetricName": {metricName},
		"MetricDataQueries.member.1.MetricStat.Period":            {fmt.Sprintf("%d", period)},
		"MetricDataQueries.member.1.MetricStat.Stat":              {stat},
	}

	// Dimensions from a map, e.g. {"InstanceId": "i-123"}.
	if dims, ok := call.Input["dimensions"].(map[string]interface{}); ok {
		idx := 1
		for k, v := range dims {
			vs, _ := v.(string)
			prefix := fmt.Sprintf("MetricDataQueries.member.1.MetricStat.Metric.Dimensions.member.%d", idx)
			params.Set(prefix+".Name", k)
			params.Set(prefix+".Value", vs)
			idx++
		}
	}

	body, err := a.doAWSRequest(ctx, "monitoring", "monitoring", params)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("CloudWatch API error: %v", err), IsError: true}, nil
	}

	formatted, err := formatGetMetricData(body, metricName, stat)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("format error: %v\nRaw: %s", err, string(body)), IsError: true}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

func (a *AWSCloudWatchExecutor) describeAlarms(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	params := url.Values{
		"Action":  {"DescribeAlarms"},
		"Version": {"2010-08-01"},
	}

	if prefix, _ := call.Input["alarm_name_prefix"].(string); prefix != "" {
		params.Set("AlarmNamePrefix", prefix)
	}
	if state, _ := call.Input["state_value"].(string); state != "" {
		params.Set("StateValue", state)
	}

	body, err := a.doAWSRequest(ctx, "monitoring", "monitoring", params)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("CloudWatch API error: %v", err), IsError: true}, nil
	}

	formatted, err := formatDescribeAlarms(body)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("format error: %v\nRaw: %s", err, string(body)), IsError: true}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

func (a *AWSCloudWatchExecutor) getLogEvents(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	logGroup, _ := call.Input["log_group"].(string)
	logStream, _ := call.Input["log_stream"].(string)

	if logGroup == "" || logStream == "" {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: "missing required parameters: log_group, log_stream",
			IsError: true,
		}, nil
	}

	limit := 100
	if l, ok := call.Input["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	payload := map[string]interface{}{
		"logGroupName":  logGroup,
		"logStreamName": logStream,
		"limit":         limit,
		"startFromHead": true,
	}

	// Parse optional start time to set startTime in milliseconds since epoch.
	if startStr, _ := call.Input["start"].(string); startStr != "" {
		now := time.Now()
		startTime, err := parseRelativeTime(startStr, now)
		if err != nil {
			return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("invalid start time: %v", err), IsError: true}, nil
		}
		payload["startTime"] = startTime.UnixMilli()
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("marshal payload: %v", err), IsError: true}, nil
	}

	respBody, err := a.doAWSJSONRequest(ctx, "logs", "logs", "Logs_20140328.GetLogEvents", body)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("CloudWatch Logs API error: %v", err), IsError: true}, nil
	}

	formatted, err := formatGetLogEvents(respBody)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("format error: %v\nRaw: %s", err, string(respBody)), IsError: true}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

func (a *AWSCloudWatchExecutor) describeInstances(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	params := url.Values{
		"Action":  {"DescribeInstances"},
		"Version": {"2016-11-15"},
	}

	if ids, ok := call.Input["instance_ids"].([]interface{}); ok {
		for i, id := range ids {
			if s, ok := id.(string); ok {
				params.Set(fmt.Sprintf("InstanceId.%d", i+1), s)
			}
		}
	}

	body, err := a.doAWSRequest(ctx, "ec2", "ec2", params)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("EC2 API error: %v", err), IsError: true}, nil
	}

	formatted, err := formatDescribeInstances(body)
	if err != nil {
		return &domain.ToolResult{CallID: call.ID, Content: fmt.Sprintf("format error: %v\nRaw: %s", err, string(body)), IsError: true}, nil
	}

	return &domain.ToolResult{CallID: call.ID, Content: formatted}, nil
}

// ---------------------------------------------------------------------------
// AWS Signature V4
// ---------------------------------------------------------------------------

// sigV4Params holds the parameters for signing an AWS request.
type sigV4Params struct {
	method      string
	host        string
	uri         string
	queryString string
	headers     map[string]string
	body        []byte
	service     string
	region      string
	accessKey   string
	secretKey   string
	now         time.Time
}

// signRequest signs an HTTP request with AWS Signature V4 and returns the
// Authorization header value.
func signRequest(p sigV4Params) string {
	datestamp := p.now.UTC().Format("20060102")
	amzdate := p.now.UTC().Format("20060102T150405Z")

	// Canonical headers -- must be sorted by lowercase header name.
	signedHeaderNames := make([]string, 0, len(p.headers))
	for k := range p.headers {
		signedHeaderNames = append(signedHeaderNames, strings.ToLower(k))
	}
	sort.Strings(signedHeaderNames)

	var canonicalHeaders strings.Builder
	for _, k := range signedHeaderNames {
		for orig, v := range p.headers {
			if strings.ToLower(orig) == k {
				canonicalHeaders.WriteString(k + ":" + strings.TrimSpace(v) + "\n")
				break
			}
		}
	}

	signedHeaders := strings.Join(signedHeaderNames, ";")
	payloadHash := sha256Hex(p.body)

	canonicalRequest := strings.Join([]string{
		p.method,
		p.uri,
		p.queryString,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	credentialScope := datestamp + "/" + p.region + "/" + p.service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzdate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveSigningKey(p.secretKey, datestamp, p.region, p.service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	return fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		p.accessKey, credentialScope, signedHeaders, signature,
	)
}

// deriveSigningKey derives the AWS SigV4 signing key.
func deriveSigningKey(secretKey, datestamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(datestamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	return kSigning
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

// serviceEndpoint returns the endpoint URL for an AWS service.
func (a *AWSCloudWatchExecutor) serviceEndpoint(service string) string {
	return fmt.Sprintf("https://%s.%s.amazonaws.com", service, a.region)
}

// doAWSRequest performs a form-encoded POST to an AWS service.
func (a *AWSCloudWatchExecutor) doAWSRequest(ctx context.Context, service, sigService string, params url.Values) ([]byte, error) {
	endpoint := a.serviceEndpoint(service)
	body := []byte(params.Encode())
	now := time.Now().UTC()

	host := service + "." + a.region + ".amazonaws.com"
	headers := map[string]string{
		"Host":         host,
		"Content-Type": "application/x-www-form-urlencoded; charset=utf-8",
		"X-Amz-Date":  now.Format("20060102T150405Z"),
	}

	auth := signRequest(sigV4Params{
		method:      "POST",
		host:        host,
		uri:         "/",
		queryString: "",
		headers:     headers,
		body:        body,
		service:     sigService,
		region:      a.region,
		accessKey:   a.accessKey,
		secretKey:   a.secretKey,
		now:         now,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/", strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization", auth)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// doAWSJSONRequest performs a JSON POST to an AWS service (used for CloudWatch Logs).
func (a *AWSCloudWatchExecutor) doAWSJSONRequest(ctx context.Context, service, sigService, target string, payload []byte) ([]byte, error) {
	endpoint := a.serviceEndpoint(service)
	now := time.Now().UTC()

	host := service + "." + a.region + ".amazonaws.com"
	headers := map[string]string{
		"Host":          host,
		"Content-Type":  "application/x-amz-json-1.1",
		"X-Amz-Date":   now.Format("20060102T150405Z"),
		"X-Amz-Target": target,
	}

	auth := signRequest(sigV4Params{
		method:      "POST",
		host:        host,
		uri:         "/",
		queryString: "",
		headers:     headers,
		body:        payload,
		service:     sigService,
		region:      a.region,
		accessKey:   a.accessKey,
		secretKey:   a.secretKey,
		now:         now,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/", strings.NewReader(string(payload)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization", auth)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// ---------------------------------------------------------------------------
// Response formatters
// ---------------------------------------------------------------------------

// XML structures for CloudWatch GetMetricData response.
type getMetricDataResult struct {
	XMLName            xml.Name            `xml:"GetMetricDataResponse"`
	MetricDataResults  []metricDataResult  `xml:"GetMetricDataResult>MetricDataResults>member"`
}

type metricDataResult struct {
	ID         string   `xml:"Id"`
	Label      string   `xml:"Label"`
	StatusCode string   `xml:"StatusCode"`
	Timestamps []string `xml:"Timestamps>member"`
	Values     []float64 `xml:"Values>member"`
}

func formatGetMetricData(body []byte, metricName, stat string) (string, error) {
	var resp getMetricDataResult
	if err := xml.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("unmarshal xml: %w", err)
	}

	var sb strings.Builder
	totalPoints := 0
	for _, mdr := range resp.MetricDataResults {
		totalPoints += len(mdr.Timestamps)
	}

	sb.WriteString(fmt.Sprintf("Metric: %s, Statistic: %s, Datapoints: %d\n\n", metricName, stat, totalPoints))

	for _, mdr := range resp.MetricDataResults {
		label := mdr.Label
		if label == "" {
			label = mdr.ID
		}
		sb.WriteString(fmt.Sprintf("Series: %s (status: %s)\n", label, mdr.StatusCode))

		// Build timestamp-value pairs and sort by timestamp.
		type tsVal struct {
			ts  string
			val float64
		}
		pairs := make([]tsVal, 0, len(mdr.Timestamps))
		for i := range mdr.Timestamps {
			if i < len(mdr.Values) {
				pairs = append(pairs, tsVal{ts: mdr.Timestamps[i], val: mdr.Values[i]})
			}
		}
		sort.Slice(pairs, func(i, j int) bool {
			return pairs[i].ts < pairs[j].ts
		})

		for _, p := range pairs {
			sb.WriteString(fmt.Sprintf("  %s => %.4f\n", p.ts, p.val))
		}
	}

	if totalPoints == 0 {
		sb.WriteString("  (no datapoints in the requested time range)\n")
	}

	return sb.String(), nil
}

// XML structures for DescribeAlarms response.
type describeAlarmsResult struct {
	XMLName xml.Name      `xml:"DescribeAlarmsResponse"`
	Alarms  []metricAlarm `xml:"DescribeAlarmsResult>MetricAlarms>member"`
}

type metricAlarm struct {
	AlarmName              string  `xml:"AlarmName"`
	StateValue             string  `xml:"StateValue"`
	MetricName             string  `xml:"MetricName"`
	Namespace              string  `xml:"Namespace"`
	ComparisonOperator     string  `xml:"ComparisonOperator"`
	Threshold              float64 `xml:"Threshold"`
	StateUpdatedTimestamp  string  `xml:"StateUpdatedTimestamp"`
	AlarmDescription       string  `xml:"AlarmDescription"`
}

func formatDescribeAlarms(body []byte) (string, error) {
	var resp describeAlarmsResult
	if err := xml.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("unmarshal xml: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CloudWatch Alarms: %d found\n\n", len(resp.Alarms)))

	for _, alarm := range resp.Alarms {
		sb.WriteString(fmt.Sprintf("Alarm: %s\n", alarm.AlarmName))
		sb.WriteString(fmt.Sprintf("  State: %s\n", alarm.StateValue))
		sb.WriteString(fmt.Sprintf("  Metric: %s/%s\n", alarm.Namespace, alarm.MetricName))
		sb.WriteString(fmt.Sprintf("  Condition: %s %.4f\n", alarm.ComparisonOperator, alarm.Threshold))
		sb.WriteString(fmt.Sprintf("  Last updated: %s\n", alarm.StateUpdatedTimestamp))
		if alarm.AlarmDescription != "" {
			sb.WriteString(fmt.Sprintf("  Description: %s\n", alarm.AlarmDescription))
		}
		sb.WriteString("\n")
	}

	if len(resp.Alarms) == 0 {
		sb.WriteString("  (no alarms match the filter criteria)\n")
	}

	return sb.String(), nil
}

// XML structures for EC2 DescribeInstances response.
type describeInstancesResult struct {
	XMLName      xml.Name      `xml:"DescribeInstancesResponse"`
	Reservations []reservation `xml:"reservationSet>item"`
}

type reservation struct {
	Instances []ec2Instance `xml:"instancesSet>item"`
}

type ec2Instance struct {
	InstanceID       string `xml:"instanceId"`
	InstanceType     string `xml:"instanceType"`
	AvailabilityZone string `xml:"placement>availabilityZone"`
	State            string `xml:"instanceState>name"`
	PrivateIP        string `xml:"privateIpAddress"`
	PublicIP         string `xml:"ipAddress"`
	LaunchTime       string `xml:"launchTime"`
}

func formatDescribeInstances(body []byte) (string, error) {
	var resp describeInstancesResult
	if err := xml.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("unmarshal xml: %w", err)
	}

	var instances []ec2Instance
	for _, r := range resp.Reservations {
		instances = append(instances, r.Instances...)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("EC2 Instances: %d found\n\n", len(instances)))

	for _, inst := range instances {
		sb.WriteString(fmt.Sprintf("Instance: %s\n", inst.InstanceID))
		sb.WriteString(fmt.Sprintf("  State: %s\n", inst.State))
		sb.WriteString(fmt.Sprintf("  Type: %s\n", inst.InstanceType))
		sb.WriteString(fmt.Sprintf("  AZ: %s\n", inst.AvailabilityZone))
		sb.WriteString(fmt.Sprintf("  Private IP: %s\n", inst.PrivateIP))
		if inst.PublicIP != "" {
			sb.WriteString(fmt.Sprintf("  Public IP: %s\n", inst.PublicIP))
		}
		sb.WriteString(fmt.Sprintf("  Launch time: %s\n", inst.LaunchTime))
		sb.WriteString("\n")
	}

	if len(instances) == 0 {
		sb.WriteString("  (no instances match the filter criteria)\n")
	}

	return sb.String(), nil
}

// JSON structures for CloudWatch Logs GetLogEvents response.
type getLogEventsResponse struct {
	Events []logEvent `json:"events"`
}

type logEvent struct {
	Timestamp      int64  `json:"timestamp"`
	Message        string `json:"message"`
	IngestionTime  int64  `json:"ingestionTime"`
}

func formatGetLogEvents(body []byte) (string, error) {
	var resp getLogEventsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("unmarshal json: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Log Events: %d returned\n\n", len(resp.Events)))

	for i, ev := range resp.Events {
		ts := time.UnixMilli(ev.Timestamp).UTC().Format(time.RFC3339)
		msg := strings.TrimSpace(ev.Message)
		sb.WriteString(fmt.Sprintf("[%d] %s  %s\n", i+1, ts, msg))
	}

	if len(resp.Events) == 0 {
		sb.WriteString("  (no log events in the requested time range)\n")
	}

	return sb.String(), nil
}

// formatMetricStatistics formats GetMetricStatistics XML response.
func formatMetricStatistics(body []byte, metricName, stat string) (string, error) {
	var resp struct {
		XMLName xml.Name `xml:"GetMetricStatisticsResponse"`
		Result  struct {
			Label      string `xml:"Label"`
			Datapoints struct {
				Members []struct {
					Timestamp string  `xml:"Timestamp"`
					Average   float64 `xml:"Average"`
					Sum       float64 `xml:"Sum"`
					Minimum   float64 `xml:"Minimum"`
					Maximum   float64 `xml:"Maximum"`
					Unit      string  `xml:"Unit"`
				} `xml:"member"`
			} `xml:"Datapoints"`
		} `xml:"GetMetricStatisticsResult"`
	}
	if err := xml.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("unmarshal xml: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Metric: %s, Statistic: %s, Datapoints: %d\n\n",
		metricName, stat, len(resp.Result.Datapoints.Members)))

	for i, dp := range resp.Result.Datapoints.Members {
		var val float64
		switch stat {
		case "Sum":
			val = dp.Sum
		case "Minimum":
			val = dp.Minimum
		case "Maximum":
			val = dp.Maximum
		default:
			val = dp.Average
		}
		sb.WriteString(fmt.Sprintf("[%d] %s  %.4f %s\n", i+1, dp.Timestamp, val, dp.Unit))
	}

	return sb.String(), nil
}

// logsQueryResponse represents CloudWatch Logs Insights query results.
type logsQueryResponse struct {
	Status  string             `json:"status"`
	Results [][]logsQueryField `json:"results"`
}

type logsQueryField struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// formatLogsQueryResults formats CloudWatch Logs Insights query results.
func formatLogsQueryResults(body []byte) (string, error) {
	var resp logsQueryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("unmarshal json: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Status: %s, Results: %d\n\n", resp.Status, len(resp.Results)))

	if len(resp.Results) == 0 {
		sb.WriteString("  (no log entries found)\n")
		return sb.String(), nil
	}

	for i, row := range resp.Results {
		sb.WriteString(fmt.Sprintf("[%d]\n", i+1))
		for _, field := range row {
			// Skip internal pointer fields.
			if field.Field == "@ptr" {
				continue
			}
			sb.WriteString(fmt.Sprintf("  %s: %s\n", field.Field, field.Value))
		}
	}

	return sb.String(), nil
}
