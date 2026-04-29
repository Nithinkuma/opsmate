from __future__ import annotations
import subprocess
import shutil
from . import tool

_MAX_OUTPUT = 12_000  # chars returned to model


def _run(args: list[str], timeout: int = 30) -> str:
    if not shutil.which("kubectl"):
        return "Error: kubectl not found. Install kubectl and configure your kubeconfig."
    try:
        r = subprocess.run(
            ["kubectl"] + args,
            capture_output=True, text=True, timeout=timeout,
        )
        out = (r.stdout + r.stderr).strip()
        return _tail(out, _MAX_OUTPUT)
    except subprocess.TimeoutExpired:
        return f"Error: command timed out after {timeout}s"
    except Exception as e:
        return f"Error: {e}"


def _tail(text: str, max_chars: int) -> str:
    if len(text) <= max_chars:
        return text
    return f"[... truncated, showing last {max_chars} chars ...]\n" + text[-max_chars:]


# ── Resource inspection ──────────────────────────────────────────────────────

@tool(
    "List Kubernetes resources (pods, deployments, services, nodes, events, etc.)",
    params={
        "resource": "Resource type: pod, deployment, service, node, ingress, pvc, configmap, secret, job, cronjob, statefulset, daemonset, replicaset, namespace, event",
        "namespace": "Namespace. Use '-' for all namespaces, omit for default",
        "name": "Specific resource name (optional — omit to list all)",
        "extra_flags": "Extra kubectl flags e.g. '--sort-by=.lastTimestamp' or '-l app=nginx'",
    },
)
def kubectl_get(
    resource: str,
    namespace: str = "default",
    name: str = "",
    extra_flags: str = "",
) -> str:
    args = ["get", resource]
    if name:
        args.append(name)
    if namespace == "-":
        args += ["--all-namespaces"]
    else:
        args += ["-n", namespace]
    args += ["-o", "wide"]
    if extra_flags:
        args += extra_flags.split()
    return _run(args)


@tool(
    "Describe a Kubernetes resource in detail — shows status, events, conditions",
    params={
        "resource": "Resource type (pod, deployment, service, node, ingress, pvc, etc.)",
        "name": "Resource name",
        "namespace": "Namespace (omit for default)",
    },
)
def kubectl_describe(resource: str, name: str, namespace: str = "default") -> str:
    args = ["describe", resource, name, "-n", namespace]
    return _run(args)


@tool(
    "Get logs from a pod or deployment",
    params={
        "pod": "Pod name (or deployment/name to get logs from a deployment's pod)",
        "namespace": "Namespace",
        "container": "Container name (omit for single-container pods)",
        "previous": "Set to 'true' to get logs from the previous (crashed) container",
        "tail": "Number of lines from the end (default 200)",
        "since": "Only return logs newer than this duration, e.g. '1h', '30m'",
    },
)
def kubectl_logs(
    pod: str,
    namespace: str = "default",
    container: str = "",
    previous: str = "false",
    tail: str = "200",
    since: str = "",
) -> str:
    args = ["logs", pod, "-n", namespace, f"--tail={tail}"]
    if container:
        args += ["-c", container]
    if previous.lower() == "true":
        args.append("--previous")
    if since:
        args += [f"--since={since}"]
    return _run(args, timeout=20)


@tool(
    "Get Kubernetes events — sorted by time, optionally filtered by namespace or resource",
    params={
        "namespace": "Namespace. Use '-' for all namespaces",
        "resource": "Filter events for a specific resource, e.g. 'pod/nginx-xxx'",
        "warning_only": "Set to 'true' to show only Warning events",
    },
)
def kubectl_events(
    namespace: str = "default",
    resource: str = "",
    warning_only: str = "false",
) -> str:
    args = ["get", "events", "--sort-by=.lastTimestamp"]
    if namespace == "-":
        args += ["--all-namespaces"]
    else:
        args += ["-n", namespace]
    if resource:
        args += ["--field-selector", f"involvedObject.name={resource.split('/')[-1]}"]
    if warning_only.lower() == "true":
        args += ["--field-selector", "type=Warning"]
    return _run(args)


@tool(
    "Show resource usage (CPU/memory) for pods or nodes — requires metrics-server",
    params={
        "target": "What to measure: 'pods' or 'nodes'",
        "namespace": "Namespace for pods (omit for nodes)",
        "sort_by": "Sort field: 'cpu' or 'memory'",
    },
)
def kubectl_top(target: str = "pods", namespace: str = "default", sort_by: str = "cpu") -> str:
    args = ["top", target, f"--sort-by={sort_by}"]
    if target == "pods":
        args += ["-n", namespace]
    return _run(args, timeout=15)


# ── Context & cluster info ───────────────────────────────────────────────────

@tool("List available kubectl contexts and show the current active one")
def kubectl_get_contexts() -> str:
    return _run(["config", "get-contexts"])


@tool(
    "Switch the active kubectl context",
    params={"context": "Context name to switch to (from kubectl_get_contexts)"},
)
def kubectl_use_context(context: str) -> str:
    return _run(["config", "use-context", context])


@tool(
    "Show cluster info — API server URL, CoreDNS, and other cluster services",
)
def kubectl_cluster_info() -> str:
    return _run(["cluster-info"])


@tool(
    "Check node health — shows status, roles, version, and resource pressure",
)
def kubectl_node_status() -> str:
    result = _run(["get", "nodes", "-o", "wide"])
    detail = _run(["describe", "nodes"])
    # Only return describe if nodes seem unhealthy
    if "NotReady" in result or "MemoryPressure" in result or "DiskPressure" in result:
        return result + "\n\n--- Node details ---\n" + _tail(detail, 4000)
    return result


# ── Exec & port-forward ──────────────────────────────────────────────────────

@tool(
    "Run a command inside a running pod container",
    params={
        "pod": "Pod name",
        "command": "Shell command to run inside the container",
        "namespace": "Namespace",
        "container": "Container name (for multi-container pods)",
    },
    dangerous=True,
)
def kubectl_exec(pod: str, command: str, namespace: str = "default", container: str = "") -> str:
    args = ["exec", pod, "-n", namespace]
    if container:
        args += ["-c", container]
    args += ["--", "sh", "-c", command]
    return _run(args, timeout=30)


# ── Remediation tools ────────────────────────────────────────────────────────

@tool(
    "Restart a deployment, statefulset, or daemonset by triggering a rolling restart",
    params={
        "resource": "Resource type and name e.g. 'deployment/nginx' or 'statefulset/db'",
        "namespace": "Namespace",
    },
    dangerous=True,
)
def kubectl_rollout_restart(resource: str, namespace: str = "default") -> str:
    return _run(["rollout", "restart", resource, "-n", namespace])


@tool(
    "Check rollout status or history of a deployment/statefulset/daemonset",
    params={
        "resource": "Resource type and name e.g. 'deployment/nginx'",
        "namespace": "Namespace",
        "history": "Set to 'true' to show rollout history instead of current status",
    },
)
def kubectl_rollout_status(resource: str, namespace: str = "default", history: str = "false") -> str:
    sub = "history" if history.lower() == "true" else "status"
    return _run(["rollout", sub, resource, "-n", namespace], timeout=15)


@tool(
    "Undo the last rollout of a deployment/statefulset",
    params={
        "resource": "Resource type and name e.g. 'deployment/nginx'",
        "namespace": "Namespace",
    },
    dangerous=True,
)
def kubectl_rollout_undo(resource: str, namespace: str = "default") -> str:
    return _run(["rollout", "undo", resource, "-n", namespace])


@tool(
    "Scale a deployment, statefulset, or replicaset",
    params={
        "resource": "Resource type and name e.g. 'deployment/nginx'",
        "replicas": "Target replica count",
        "namespace": "Namespace",
    },
    dangerous=True,
)
def kubectl_scale(resource: str, replicas: str, namespace: str = "default") -> str:
    return _run(["scale", resource, f"--replicas={replicas}", "-n", namespace])


@tool(
    "Update the container image of a deployment or statefulset",
    params={
        "resource": "Resource type and name e.g. 'deployment/nginx'",
        "container": "Container name within the pod spec",
        "image": "New image e.g. 'nginx:1.27'",
        "namespace": "Namespace",
    },
    dangerous=True,
)
def kubectl_set_image(resource: str, container: str, image: str, namespace: str = "default") -> str:
    return _run(["set", "image", resource, f"{container}={image}", "-n", namespace])


@tool(
    "Apply a Kubernetes YAML manifest from inline content — use dry-run first to preview",
    params={
        "manifest_yaml": "Full YAML content of the manifest to apply",
        "dry_run": "Set to 'true' to preview changes without applying (default true)",
        "namespace": "Namespace (overrides namespace in manifest if set)",
    },
    dangerous=True,
)
def kubectl_apply(manifest_yaml: str, dry_run: str = "true", namespace: str = "") -> str:
    import tempfile, os
    with tempfile.NamedTemporaryFile(mode="w", suffix=".yaml", delete=False) as f:
        f.write(manifest_yaml)
        tmp = f.name
    try:
        args = ["apply", "-f", tmp]
        if dry_run.lower() == "true":
            args += ["--dry-run=client"]
        if namespace:
            args += ["-n", namespace]
        return _run(args)
    finally:
        os.unlink(tmp)


@tool(
    "Delete a Kubernetes resource",
    params={
        "resource": "Resource type and name e.g. 'pod/nginx-xxx' or 'deployment/api'",
        "namespace": "Namespace",
        "force": "Set to 'true' for immediate deletion (--force --grace-period=0)",
    },
    dangerous=True,
)
def kubectl_delete(resource: str, namespace: str = "default", force: str = "false") -> str:
    args = ["delete", resource, "-n", namespace]
    if force.lower() == "true":
        args += ["--force", "--grace-period=0"]
    return _run(args)
