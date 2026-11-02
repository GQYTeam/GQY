//go:build !windows

package plugin

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// process 是运行时拉起的 stdio 进程句柄（P8）。
type process interface {
	io.ReadWriteCloser
	PID() int
}

type unixProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (p *unixProcess) Read(b []byte) (int, error)  { return p.stdout.Read(b) }
func (p *unixProcess) Write(b []byte) (int, error) { return p.stdin.Write(b) }
func (p *unixProcess) PID() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Close 按固定时序回收进程组：关 stdin（EOF 自退）→ 等 5s → SIGTERM → 等 2s → SIGKILL → 回收。
func (p *unixProcess) Close() error {
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

// startProcess 启动独立进程组的插件进程（P8：Setpgid 整组可杀）。
func startProcess(ctx context.Context, m *Manifest, dir string) (process, error) {
	cmd := exec.Command(m.Command[0], m.Command[1:]...) // 进程生命周期由 Close 管，不绑请求 ctx（P8）
	cwd := dir
	if m.Cwd != "" && m.Cwd != "." {
		cwd = filepath.Join(dir, m.Cwd)
	}
	cmd.Dir = cwd
	cmd.Env = mergeEnv(m.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	logPath := filepath.Join(filepath.Dir(dir), m.ID+".log")
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		cmd.Stderr = f
		defer func() { _ = f.Close() }()
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &unixProcess{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

func mergeEnv(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		switch k {
		case "PATH", "HOME", "GQY_HOME":
			continue // 不可覆盖
		}
		env = append(env, k+"="+v)
	}
	return env
}

var _ = errors.Is
