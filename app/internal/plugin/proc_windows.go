//go:build windows

package plugin

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

type process interface {
	io.ReadWriteCloser
	PID() int
}

type winProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (p *winProcess) Read(b []byte) (int, error)  { return p.stdout.Read(b) }
func (p *winProcess) Write(b []byte) (int, error) { return p.stdin.Write(b) }
func (p *winProcess) PID() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Close 在 Windows 上关 stdin 后用 taskkill /T /F 兜底回收整棵进程树。
func (p *winProcess) Close() error {
	_ = p.stdin.Close()
	if waitExit(p.cmd, 5*time.Second) {
		return nil
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", itoa(p.cmd.Process.Pid))
	_ = kill.Run()
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

func itoa(v int) string { return strconv.Itoa(v) }

func startProcess(ctx context.Context, m *Manifest, dir string) (process, error) {
	cmd := exec.Command(m.Command[0], m.Command[1:]...) // 进程生命周期由 Close 管，不绑请求 ctx（P8）
	cwd := dir
	if m.Cwd != "" && m.Cwd != "." {
		cwd = filepath.Join(dir, m.Cwd)
	}
	cmd.Dir = cwd
	cmd.Env = mergeEnv(m.Env)

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
		defer f.Close()
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &winProcess{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

func mergeEnv(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		switch k {
		case "PATH", "HOME", "GQY_HOME":
			continue
		}
		env = append(env, k+"="+v)
	}
	return env
}
