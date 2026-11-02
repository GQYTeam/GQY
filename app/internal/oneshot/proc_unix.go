//go:build !windows

package oneshot

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"gqy/internal/plugin"
)

// proc 是一次性插件进程句柄；回收时序沿用 04 P8。
type proc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (p *proc) Read(b []byte) (int, error)  { return p.stdout.Read(b) }
func (p *proc) Write(b []byte) (int, error) { return p.stdin.Write(b) }

// Close 关 stdin（EOF 自退）→ 等 5s → SIGTERM 进程组 → 等 2s → SIGKILL → 回收。
func (p *proc) Close() error {
	_ = p.stdin.Close()
	if waitExit(p.cmd, 5*time.Second) {
		return nil
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
	if waitExit(p.cmd, 2*time.Second) {
		return nil
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	_ = p.cmd.Wait()
	return nil
}

func waitExit(cmd *exec.Cmd, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

func startProc(ctx context.Context, m *plugin.Manifest, dir string) (*proc, error) {
	cmd := exec.CommandContext(ctx, m.Command[0], m.Command[1:]...)
	cwd := dir
	if m.Cwd != "" && m.Cwd != "." {
		cwd = filepath.Join(dir, m.Cwd)
	}
	cmd.Dir = cwd
	cmd.Env = mergeEnv(m.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &proc{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

func mergeEnv(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		switch k {
		case "PATH", "HOME", "GQY_HOME":
			continue // 不可覆盖（04 P3）
		}
		env = append(env, k+"="+v)
	}
	return env
}
