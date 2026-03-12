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
	ExecStreaming(ctx context.Context, cmd string, opts ExecOpts) (ProcessHandle, error)
	Upload(ctx context.Context, content []byte, path string) error
	Download(ctx context.Context, path string) ([]byte, error)
	Stop(ctx context.Context) error
	Start(ctx context.Context) error
}

// ProcessHandle abstracts over a long-running process with stdin/stdout streaming.
// Used by Pi RPC and any runtime that needs bidirectional communication.
type ProcessHandle interface {
	// Write sends data to the process's stdin.
	Write(data []byte) error
	// ReadLine reads the next line from stdout (blocking). Returns io.EOF when done.
	ReadLine() (string, error)
	// Wait blocks until the process exits and returns the exit code.
	Wait() (int, error)
	// Kill terminates the process.
	Kill() error
}
