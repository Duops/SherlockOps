---
title: "High CPU Usage"
alerts:
  - "HighCPU*"
  - "CPUThrottling"
labels:
  severity: critical
priority: 10
---

## Investigation Steps

1. SSH into the affected node or use `kubectl exec` to access the pod
2. Run `top -b -n 1 | head -20` to identify the process consuming the most CPU
3. Check recent deployments with `kubectl rollout history` for the affected namespace
4. Review application metrics in Grafana for the last 1-2 hours
5. Check for garbage collection pressure: look at GC pause times and heap usage

## Common Causes

- **Runaway process or infinite loop**: A bug in application code causing 100% CPU on one or more cores
- **Insufficient CPU limits**: Kubernetes CPU limits set too low for the workload
- **Traffic spike**: Sudden increase in request volume overwhelming the service
- **Memory leak causing GC pressure**: Excessive garbage collection due to memory leaks
- **Regex backtracking**: Poorly written regular expressions causing exponential CPU usage
- **Connection pool exhaustion**: Threads blocking on I/O and piling up CPU-intensive retries

## Key Metrics to Check

- `node_cpu_seconds_total` (per-mode breakdown)
- `container_cpu_usage_seconds_total` (per-pod)
- `container_cpu_cfs_throttled_seconds_total` (throttling indicator)
- `process_cpu_seconds_total` (application-level)
- Request latency percentiles (p99 spike often correlates)

## Remediation

1. **Immediate**: If a single pod is affected, restart it: `kubectl delete pod <name>`
2. **Scale out**: Add replicas if the load is legitimate: `kubectl scale deployment <name> --replicas=<N>`
3. **Roll back**: If correlated with a recent deploy: `kubectl rollout undo deployment/<name>`
4. **Increase limits**: If CPU limits are consistently hit, raise them in the deployment spec
5. **Profile**: Attach a profiler (pprof for Go, async-profiler for JVM) to identify hot paths
