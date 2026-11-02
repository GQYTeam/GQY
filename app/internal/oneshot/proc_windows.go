//go:build windows

package oneshot

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"gqy/internal/plugin"
)

type proc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (p *proc) Read(b []byte) (int, error)  { return p.stdout.Read(b) }
func (p *proc) Write(b []byte) (int, error) { return p.stdin.Write(b) }

// Close 在 Windows 上关 stdin 后用 taskkill /T /F 兜底回收整棵进程树。
func (p *proc) Close() error {
	_ = p.stdin.Close()
	if waitExit(p.cmd, 5*time.Second) {
		return nil
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.cmd.Process.Pid))
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

func startProc(ctx context.Context, m *plugin.Manifest, dir string) (*proc, error) {
	cmd := exec.CommandContext(ctx, m.Command[0], m.Command[1:]...)
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
			continue
		}
		env = append(env, k+"="+v)
	}
	return env
}
