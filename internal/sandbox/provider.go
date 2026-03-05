package sandbox

import "context"

type SandboxProvider interface {
	Create(ctx context.Context, opts CreateOpts) (Sandbox, error)
	Get(ctx context.Context, id string) (Sandbox, error)
	List(ctx context.Context, labels map[string]string) ([]Sandbox, error)
	Delete(ctx context.Context, id string) error
}

type Sandbox interface {
	ID() string
	Status() SandboxStatus
	Exec(ctx context.Context, cmd string, opts ExecOpts) (ExecResult, error)
	Upload(ctx context.Context, content []byte, path string) error
	Download(ctx context.Context, path string) ([]byte, error)
	Stop(ctx context.Context) error
	Start(ctx context.Context) error
}
