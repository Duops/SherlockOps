package tooling

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/Duops/SherlockOps/internal/domain"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// KubernetesExecutor provides Kubernetes query tools.
type KubernetesExecutor struct {
	clientset *kubernetes.Clientset
	logger    *slog.Logger
}

// NewKubernetesExecutor creates a new Kubernetes tool executor.
// kubeconfig is the path to a kubeconfig file; if empty, in-cluster config is used.
// kubeContext is the kubeconfig context to use; if empty, the current context is used.
func NewKubernetesExecutor(kubeconfig, kubeContext string, logger *slog.Logger) (*KubernetesExecutor, error) {
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
		overrides := &clientcmd.ConfigOverrides{}
		if kubeContext != "" {
			overrides.CurrentContext = kubeContext
		}
		config, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			loadingRules, overrides,
		).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("build kubeconfig: %w", err)
		}
	} else {
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("in-cluster config: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	return &KubernetesExecutor{
		clientset: clientset,
		logger:    logger,
	}, nil
}

// ListTools returns the Kubernetes tool definitions.
func (k *KubernetesExecutor) ListTools(_ context.Context) ([]domain.Tool, error) {
	return []domain.Tool{
		{
			Name:        "k8s_get_pods",
			Description: "List Kubernetes pods in a namespace, optionally filtered by label selector.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"namespace": map[string]interface{}{
						"type":        "string",
						"description": "Kubernetes namespace (default: default)",
					},
					"label_selector": map[string]interface{}{
						"type":        "string",
						"description": "Label selector, e.g. app=myapp",
					},
				},
			},
		},
		{
			Name:        "k8s_pod_logs",
			Description: "Get logs from a Kubernetes pod.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"namespace": map[string]interface{}{
						"type":        "string",
						"description": "Kubernetes namespace (default: default)",
					},
					"pod": map[string]interface{}{
						"type":        "string",
						"description": "Pod name",
					},
					"container": map[string]interface{}{
						"type":        "string",
						"description": "Container name (optional for single-container pods)",
					},
					"previous": map[string]interface{}{
						"type":        "boolean",
						"description": "Get logs from the previous container instance",
					},
					"tail_lines": map[string]interface{}{
						"type":        "number",
						"description": "Number of lines from the end of the logs (default: 100)",
					},
				},
				"required": []interface{}{"pod"},
			},
		},
		{
			Name:        "k8s_get_events",
			Description: "Get Kubernetes events in a namespace, optionally filtered by field selector.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"namespace": map[string]interface{}{
						"type":        "string",
						"description": "Kubernetes namespace (default: default)",
					},
					"field_selector": map[string]interface{}{
						"type":        "string",
						"description": "Field selector, e.g. involvedObject.name=myapp-xyz",
					},
				},
			},
		},
	}, nil
}

// Execute dispatches a Kubernetes tool call.
func (k *KubernetesExecutor) Execute(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	switch call.Name {
	case "k8s_get_pods":
		return k.getPods(ctx, call)
	case "k8s_pod_logs":
		return k.getPodLogs(ctx, call)
	case "k8s_get_events":
		return k.getEvents(ctx, call)
	default:
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("unknown tool: %s", call.Name),
			IsError: true,
		}, nil
	}
}

func (k *KubernetesExecutor) getPods(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	ns := stringParam(call.Input, "namespace", "default")
	labelSelector, _ := call.Input["label_selector"].(string)

	pods, err := k.clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("list pods error: %v", err),
			IsError: true,
		}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Pods in namespace %q (count: %d):\n\n", ns, len(pods.Items)))

	for _, pod := range pods.Items {
		sb.WriteString(fmt.Sprintf("Name: %s\n", pod.Name))
		sb.WriteString(fmt.Sprintf("  Status: %s\n", pod.Status.Phase))
		sb.WriteString(fmt.Sprintf("  Node: %s\n", pod.Spec.NodeName))

		for _, cs := range pod.Status.ContainerStatuses {
			sb.WriteString(fmt.Sprintf("  Container %s: ready=%v, restarts=%d\n",
				cs.Name, cs.Ready, cs.RestartCount))
			if cs.State.Waiting != nil {
				sb.WriteString(fmt.Sprintf("    Waiting: %s (%s)\n",
					cs.State.Waiting.Reason, cs.State.Waiting.Message))
			}
			if cs.State.Terminated != nil {
				sb.WriteString(fmt.Sprintf("    Terminated: %s (exit=%d)\n",
					cs.State.Terminated.Reason, cs.State.Terminated.ExitCode))
			}
		}
		sb.WriteString("\n")
	}

	return &domain.ToolResult{CallID: call.ID, Content: sb.String()}, nil
}

func (k *KubernetesExecutor) getPodLogs(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	ns := stringParam(call.Input, "namespace", "default")
	podName, _ := call.Input["pod"].(string)
	if podName == "" {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: "missing required parameter: pod",
			IsError: true,
		}, nil
	}

	container, _ := call.Input["container"].(string)
	previous, _ := call.Input["previous"].(bool)

	tailLines := int64(100)
	if tl, ok := call.Input["tail_lines"].(float64); ok && tl > 0 {
		tailLines = int64(tl)
	}

	opts := &corev1.PodLogOptions{
		TailLines: &tailLines,
		Previous:  previous,
	}
	if container != "" {
		opts.Container = container
	}

	req := k.clientset.CoreV1().Pods(ns).GetLogs(podName, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("get pod logs error: %v", err),
			IsError: true,
		}, nil
	}
	defer stream.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stream); err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("read logs error: %v", err),
			IsError: true,
		}, nil
	}

	header := fmt.Sprintf("Logs for pod %s/%s", ns, podName)
	if container != "" {
		header += fmt.Sprintf(" (container: %s)", container)
	}
	header += fmt.Sprintf(" (tail: %d lines):\n\n", tailLines)

	return &domain.ToolResult{
		CallID:  call.ID,
		Content: header + buf.String(),
	}, nil
}

func (k *KubernetesExecutor) getEvents(ctx context.Context, call domain.ToolCall) (*domain.ToolResult, error) {
	ns := stringParam(call.Input, "namespace", "default")
	fieldSelector, _ := call.Input["field_selector"].(string)

	events, err := k.clientset.CoreV1().Events(ns).List(ctx, metav1.ListOptions{
		FieldSelector: fieldSelector,
	})
	if err != nil {
		return &domain.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("list events error: %v", err),
			IsError: true,
		}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Events in namespace %q (count: %d):\n\n", ns, len(events.Items)))

	for _, event := range events.Items {
		sb.WriteString(fmt.Sprintf("[%s] %s/%s: %s\n",
			event.Type, event.InvolvedObject.Kind, event.InvolvedObject.Name, event.Reason))
		sb.WriteString(fmt.Sprintf("  Message: %s\n", event.Message))
		if event.Count > 1 {
			sb.WriteString(fmt.Sprintf("  Count: %d, Last: %s\n",
				event.Count, event.LastTimestamp.Format("2006-01-02 15:04:05")))
		}
		sb.WriteString("\n")
	}

	return &domain.ToolResult{CallID: call.ID, Content: sb.String()}, nil
}

func stringParam(input map[string]interface{}, key, defaultVal string) string {
	v, ok := input[key].(string)
	if !ok || v == "" {
		return defaultVal
	}
	return v
}
