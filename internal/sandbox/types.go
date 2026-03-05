package sandbox

import "time"

type SandboxStatus string

const (
	SandboxStatusRunning  SandboxStatus = "running"
	SandboxStatusStopped  SandboxStatus = "stopped"
	SandboxStatusCreating SandboxStatus = "creating"
	SandboxStatusError    SandboxStatus = "error"
)

type CreateOpts struct {
	Name       string            `json:"name"`
	Labels     map[string]string `json:"labels"`
	Snapshot   string            `json:"snapshot"`
	Resources  ResourceSpec      `json:"resources"`
	EnvVars    map[string]string `json:"env_vars"`
	AutoStop   time.Duration     `json:"auto_stop"`
	AutoDelete time.Duration     `json:"auto_delete"`
	Ephemeral  bool              `json:"ephemeral"`
}

type ResourceSpec struct {
	CPU    int `json:"cpu"`
	Memory int `json:"memory"`
	Disk   int `json:"disk"`
}

type ExecOpts struct {
	WorkDir string            `json:"work_dir"`
	Env     map[string]string `json:"env"`
	Timeout time.Duration     `json:"timeout"`
}

type ExecResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type VolumeMount struct {
	VolumeID  string `json:"volume_id"`
	MountPath string `json:"mount_path"`
	Subpath   string `json:"subpath"`
}
