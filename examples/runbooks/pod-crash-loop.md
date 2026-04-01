---
title: "Pod CrashLoopBackOff"
alerts:
  - "KubePodCrashLooping"
  - "PodCrashLoop*"
  - "KubePodNotReady"
labels:
  severity: warning
priority: 8
---

## Investigation Steps

1. Get pod status and events: `kubectl describe pod <name> -n <namespace>`
2. Check container logs from the last crash: `kubectl logs <pod> -n <namespace> --previous`
3. Check current logs: `kubectl logs <pod> -n <namespace>`
4. Review recent deployments: `kubectl rollout history deployment/<name> -n <namespace>`
5. Check resource usage: `kubectl top pod <name> -n <namespace>`
6. Inspect pod spec for misconfigurations: `kubectl get pod <name> -n <namespace> -o yaml`

## Common Causes

- **OOMKilled**: Container exceeds memory limits. Look for exit code 137 in `kubectl describe pod`
- **Application startup failure**: Missing config, unreachable database, invalid credentials
- **Health check failure**: Liveness probe failing causes restart loop. Check probe configuration
- **Missing dependencies**: ConfigMap, Secret, or PVC not available
- **Image pull errors**: Wrong image tag, expired registry credentials, or registry down
- **Port conflicts**: Container trying to bind to an already-used port
- **File permission issues**: Read-only filesystem or wrong UID/GID

## Key Indicators

| Exit Code | Meaning |
|-----------|---------|
| 0 | Normal exit (check why app exits voluntarily) |
| 1 | Application error |
| 137 | OOMKilled (SIGKILL) |
| 139 | Segfault (SIGSEGV) |
| 143 | SIGTERM (graceful shutdown failed) |

## Remediation

1. **OOMKilled**: Increase memory limits or fix the memory leak in the application
2. **Config issues**: Verify all required ConfigMaps, Secrets, and environment variables exist
3. **Health checks**: Adjust `initialDelaySeconds`, `timeoutSeconds`, or `failureThreshold` on probes
4. **Roll back**: If crash started after a deploy: `kubectl rollout undo deployment/<name>`
5. **Debug container**: Use `kubectl debug` to attach a debug container for deeper investigation
6. **Restart backoff**: If stuck in backoff, delete the pod to reset the backoff timer
