package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// K8sJobRunner runs tool scripts as ephemeral Kubernetes Jobs.
// PodSpec is hardened per spec §2.4: non-root, read-only rootfs,
// dropped capabilities, seccomp RuntimeDefault.
type K8sJobRunner struct {
	client    kubernetes.Interface
	namespace string
	log       *slog.Logger
}

// NewK8sJobRunner creates a runner using in-cluster config, falling back to kubeconfig.
func NewK8sJobRunner(namespace string, log *slog.Logger) (*K8sJobRunner, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(), nil,
		).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("k8s: build config: %w", err)
		}
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8s: build client: %w", err)
	}
	return &K8sJobRunner{client: client, namespace: namespace, log: log}, nil
}

func (r *K8sJobRunner) Run(ctx context.Context, spec Spec) (Result, error) {
	jobName := fmt.Sprintf("opsmate-%s-%d", spec.ToolID[:min(16, len(spec.ToolID))], time.Now().UnixMilli())
	deadlineSeconds := int64(spec.TimeoutSeconds)
	if deadlineSeconds == 0 {
		deadlineSeconds = 300
	}
	ttl := int32(300)
	backoff := int32(0)

	cpu := spec.CPULimit
	if cpu == "" {
		cpu = "500m"
	}
	mem := spec.MemoryLimit
	if mem == "" {
		mem = "512Mi"
	}

	nonRoot := true
	runAsUser := int64(65534)
	readOnly := true
	allowPrivEsc := false
	zero := int64(0)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: r.namespace,
			Labels:    map[string]string{"app": "opsmate-sandbox", "tool": spec.ToolID},
		},
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds:   &deadlineSeconds,
			TTLSecondsAfterFinished: &ttl,
			BackoffLimit:            &backoff,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &nonRoot,
						RunAsUser:    &runAsUser,
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "tool",
							Image:           spec.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{interpreterFor(spec.Language), "/workspace/script"},
							Env: []corev1.EnvVar{
								{Name: "PARAMS_PATH", Value: "/workspace/params.json"},
								{Name: "REPO_PATH", Value: "/repo"},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse(cpu),
									corev1.ResourceMemory: resource.MustParse(mem),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &allowPrivEsc,
								ReadOnlyRootFilesystem:   &readOnly,
								RunAsNonRoot:             &nonRoot,
								RunAsUser:                &runAsUser,
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
								SeccompProfile: &corev1.SeccompProfile{
									Type: corev1.SeccompProfileTypeRuntimeDefault,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "workspace", MountPath: "/workspace"},
								{Name: "repo", MountPath: "/repo"},
								{Name: "tmp", MountPath: "/tmp"},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name:  "git-clone",
							Image: "alpine/git:latest",
							Command: []string{
								"git", "clone", "--depth=1",
								"--branch", spec.RepoBranch,
								spec.RepoURL, "/repo",
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &allowPrivEsc,
								RunAsNonRoot:             &nonRoot,
								RunAsUser:                &runAsUser,
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "repo", MountPath: "/repo"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "repo", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: func() *resource.Quantity { q := resource.MustParse("100Mi"); return &q }()}}},
					},
				},
			},
		},
	}
	_ = zero

	start := time.Now()
	createdJob, err := r.client.BatchV1().Jobs(r.namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return Result{}, fmt.Errorf("k8s: create job: %w", err)
	}

	result, err := r.waitForJob(ctx, createdJob.Name, time.Duration(deadlineSeconds)*time.Second)
	result.StartedAt = start
	return result, err
}

func (r *K8sJobRunner) waitForJob(ctx context.Context, name string, timeout time.Duration) (Result, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		job, err := r.client.BatchV1().Jobs(r.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return Result{}, fmt.Errorf("k8s: get job: %w", err)
		}
		if job.Status.Succeeded > 0 || job.Status.Failed > 0 {
			return r.collectResult(ctx, job)
		}
		time.Sleep(2 * time.Second)
	}
	return Result{ExitCode: 124}, fmt.Errorf("k8s: job %s timed out after %s", name, timeout)
}

func (r *K8sJobRunner) collectResult(ctx context.Context, job *batchv1.Job) (Result, error) {
	pods, err := r.client.CoreV1().Pods(r.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", job.Name),
	})
	if err != nil || len(pods.Items) == 0 {
		exitCode := 0
		if job.Status.Failed > 0 {
			exitCode = 1
		}
		return Result{ExitCode: exitCode, FinishedAt: time.Now()}, nil
	}

	pod := pods.Items[0]
	logs, _ := r.client.CoreV1().Pods(r.namespace).
		GetLogs(pod.Name, &corev1.PodLogOptions{Container: "tool"}).
		DoRaw(ctx)

	exitCode := 0
	if job.Status.Failed > 0 {
		exitCode = 1
	}
	return Result{
		ExitCode:   exitCode,
		Stdout:     string(logs),
		PodName:    pod.Name,
		FinishedAt: time.Now(),
	}, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
