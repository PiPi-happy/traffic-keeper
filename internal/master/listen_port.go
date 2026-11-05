package master

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

const masterUnitPath = "/etc/systemd/system/traffic-keeper-master.service"

var listenPortPattern = regexp.MustCompile(`(?m)^Environment=MASTER_ADDR=:[0-9]+\s*$`)

func (s *Server) handleListenPort(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]int{"port": s.listenPort})
	case http.MethodPut:
		var req struct {
			Port int `json:"port"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.Port < 1 || req.Port > 65535 {
			http.Error(w, "port must be between 1 and 65535", http.StatusBadRequest)
			return
		}
		if os.Getenv("INVOCATION_ID") == "" || os.Geteuid() != 0 {
			http.Error(w, "运行中修改端口需要由 systemd 管理；请修改 MASTER_ADDR 并重启服务", http.StatusConflict)
			return
		}
		if req.Port == s.listenPort {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "port": req.Port, "unchanged": true})
			return
		}
		ln, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(req.Port)))
		if err != nil {
			http.Error(w, "目标端口当前不可用: "+err.Error(), http.StatusConflict)
			return
		}
		_ = ln.Close()
		if err := updateMasterUnitPort(req.Port); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "port": req.Port, "restarting": true})
		go func() { _ = exec.Command("systemctl", "restart", "traffic-keeper-master.service").Run() }()
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func updateMasterUnitPort(port int) error {
	data, err := os.ReadFile(masterUnitPath)
	if err != nil {
		return fmt.Errorf("read systemd unit: %w", err)
	}
	if !listenPortPattern.Match(data) {
		return fmt.Errorf("systemd unit has no simple MASTER_ADDR=:PORT setting; update it manually")
	}
	if strings.Contains(string(data), "EnvironmentFile=") || strings.Contains(string(data), "MASTER_ADDR=") && !listenPortPattern.Match(data) {
		return fmt.Errorf("systemd unit 使用了复杂端口配置，请手动更新配置")
	}
	updated := listenPortPattern.ReplaceAll(data, []byte("Environment=MASTER_ADDR=:"+strconv.Itoa(port)))
	info, err := os.Stat(masterUnitPath)
	if err != nil {
		return fmt.Errorf("stat systemd unit: %w", err)
	}
	tmp := masterUnitPath + ".tmp"
	if err := os.WriteFile(tmp, updated, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}
	if err := os.Chown(tmp, int(info.Sys().(*syscall.Stat_t).Uid), int(info.Sys().(*syscall.Stat_t).Gid)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("preserve systemd unit owner: %w", err)
	}
	if err := os.Rename(tmp, masterUnitPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace systemd unit: %w", err)
	}
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		_ = os.WriteFile(tmp, data, info.Mode().Perm())
		_ = os.Chown(tmp, int(info.Sys().(*syscall.Stat_t).Uid), int(info.Sys().(*syscall.Stat_t).Gid))
		_ = os.Rename(tmp, masterUnitPath)
		_, _ = exec.Command("systemctl", "daemon-reload").CombinedOutput()
		return fmt.Errorf("systemd daemon-reload: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
