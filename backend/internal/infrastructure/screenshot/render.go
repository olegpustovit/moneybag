package screenshot

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

type Renderer struct {
	Binary  string
	running chan struct{}
}

func New(binary string) *Renderer { return &Renderer{Binary: binary, running: make(chan struct{}, 1)} }

// Capture opens url in one headless Chromium at a time. Virtual time fast-forwards page timers but
// waits for pending fetches, so the SPA can load its data and post its report before the screenshot.
func (r *Renderer) Capture(ctx context.Context, url string, width, height int) ([]byte, error) {
	if r.Binary == "" {
		return nil, fmt.Errorf("PNG renderer is unavailable")
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
	output := filepath.Join(dir, "preview.png")
	command := exec.CommandContext(ctx, r.Binary, "--headless", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage", "--hide-scrollbars", "--user-data-dir="+filepath.Join(dir, "profile"), "--window-size="+strconv.Itoa(width)+","+strconv.Itoa(height), "--virtual-time-budget=8000", "--screenshot="+output, url)
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
