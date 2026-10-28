package screenshot

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type Renderer struct {
	Binary  string
	running chan struct{}
}

func New(binary string) *Renderer { return &Renderer{Binary: binary, running: make(chan struct{}, 1)} }
func (r *Renderer) PNG(ctx context.Context, html string) ([]byte, error) {
	if r.Binary == "" {
		return nil, fmt.Errorf("PNG renderer is unavailable")
	}
	if len(html) > 2*1024*1024 {
		return nil, fmt.Errorf("preview exceeds 2 MiB")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	select {
	case r.running <- struct{}{}:
		defer func() { <-r.running }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	dir, e := os.MkdirTemp("", "moneybag-preview-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(dir)
	input, output := filepath.Join(dir, "index.html"), filepath.Join(dir, "preview.png")
	if e = os.WriteFile(input, []byte(html), 0600); e != nil {
		return nil, e
	}
	// All content is escaped by the dashboard renderer; CSP blocks scripts and network resources.
	command := exec.CommandContext(ctx, r.Binary, "--headless", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage", "--hide-scrollbars", "--window-size=1440,1200", "--virtual-time-budget=1000", "--screenshot="+output, "file://"+input)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = time.Second
	if e = command.Run(); e != nil {
		return nil, fmt.Errorf("PNG render failed; retry preview")
	}
	info, e := os.Stat(output)
	if e != nil || info.Size() > 10*1024*1024 {
		return nil, fmt.Errorf("invalid PNG render output")
	}
	return os.ReadFile(output)
}
