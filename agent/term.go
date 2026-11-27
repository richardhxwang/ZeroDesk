package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// Terminal protocol: binary frames are raw terminal bytes in both
// directions; text frames from the browser are control messages
// {"t":"resize","cols":80,"rows":24}.
func handleTermWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// the login shell, or zsh when chosen in Settings › Terminal (shell.go)
	cmd := exec.Command(terminalShell(), "-l")
	home, _ := os.UserHomeDir()
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor", "LANG=C.UTF-8")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 100, Rows: 30})
	if err != nil {
		_ = c.Close(websocket.StatusInternalError, err.Error())
		return
	}
	defer func() {
		ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	go func() {
		defer cancel()
		buf := make([]byte, 16384)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if werr := c.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			if _, err := ptmx.Write(data); err != nil {
				return
			}
			continue
		}
		var m struct {
			T    string `json:"t"`
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}
		if json.Unmarshal(data, &m) == nil && m.T == "resize" && m.Cols > 0 && m.Rows > 0 {
			_ = pty.Setsize(ptmx, &pty.Winsize{Cols: m.Cols, Rows: m.Rows})
		}
	}
}
