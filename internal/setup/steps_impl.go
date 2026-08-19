package setup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mdenizay/peyk/internal/caddy"
	"github.com/mdenizay/peyk/internal/config"
	"github.com/mdenizay/peyk/internal/execx"
)

var aptEnv = []string{"DEBIAN_FRONTEND=noninteractive"}

func apt(ctx context.Context, args ...string) error {
	return execx.RunEnv(ctx, aptEnv, "apt-get", append([]string{"-y", "-o", "Dpkg::Options::=--force-confold"}, args...)...)
}

// writeFileIfChanged writes content only when it differs, returning whether it wrote.
func writeFileIfChanged(path, content string, mode os.FileMode) (bool, error) {
	if cur, err := os.ReadFile(path); err == nil && string(cur) == content {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(content), mode)
}

func applySystemUpdate(ctx context.Context, _ *Env) error {
	if err := apt(ctx, "update"); err != nil {
		return err
	}
	return apt(ctx, "upgrade")
}

func applyDocker(ctx context.Context, env *Env) error {
	if execx.Exists("docker") {
		// Ensure compose plugin too; harmless if present.
		if err := execx.Run(ctx, "docker", "compose", "version"); err == nil {
			return nil
		}
	}
	if err := apt(ctx, "update"); err != nil {
		return err
	}
	if err := apt(ctx, "install", "ca-certificates", "curl", "gnupg"); err != nil {
		return err
	}
	if err := os.MkdirAll("/etc/apt/keyrings", 0o755); err != nil {
		return err
	}
	if _, err := os.Stat("/etc/apt/keyrings/docker.asc"); err != nil {
		if err := execx.Run(ctx, "curl", "-fsSL", "https://download.docker.com/linux/ubuntu/gpg", "-o", "/etc/apt/keyrings/docker.asc"); err != nil {
			return err
		}
	}
	arch, err := execx.Output(ctx, "dpkg", "--print-architecture")
	if err != nil {
		return err
	}
	codename, err := execx.Output(ctx, "bash", "-c", ". /etc/os-release && echo $VERSION_CODENAME")
	if err != nil {
		return err
	}
	src := fmt.Sprintf("deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu %s stable\n", arch, codename)
	if _, err := writeFileIfChanged("/etc/apt/sources.list.d/docker.list", src, 0o644); err != nil {
		return err
	}
	if err := apt(ctx, "update"); err != nil {
		return err
	}
	if err := apt(ctx, "install", "docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin"); err != nil {
		return err
	}
	return execx.Run(ctx, "systemctl", "enable", "--now", "docker")
}

func applyUnattendedUpgrades(ctx context.Context, _ *Env) error {
	if err := apt(ctx, "install", "unattended-upgrades"); err != nil {
		return err
	}
	conf := "APT::Periodic::Update-Package-Lists \"1\";\nAPT::Periodic::Unattended-Upgrade \"1\";\n"
	if _, err := writeFileIfChanged("/etc/apt/apt.conf.d/20auto-upgrades", conf, 0o644); err != nil {
		return err
	}
	return execx.Run(ctx, "systemctl", "enable", "--now", "unattended-upgrades")
}

// ufwDockerMarker guards the DOCKER-USER block we append to after.rules.
const ufwDockerMarker = "# BEGIN PEYK UFW-DOCKER"

// ufwAfterRules is the well-known ufw-docker integration: it makes published
// container ports obey ufw instead of Docker's iptables rules bypassing it.
const ufwAfterRules = `
# BEGIN PEYK UFW-DOCKER
*filter
:ufw-user-forward - [0:0]
:ufw-docker-logging-deny - [0:0]
:DOCKER-USER - [0:0]
-A DOCKER-USER -j ufw-user-forward

-A DOCKER-USER -j RETURN -s 10.0.0.0/8
-A DOCKER-USER -j RETURN -s 172.16.0.0/12
-A DOCKER-USER -j RETURN -s 192.168.0.0/16

-A DOCKER-USER -p udp -m udp --sport 53 --dport 1024:65535 -j RETURN

-A DOCKER-USER -j ufw-docker-logging-deny -p tcp -m tcp --tcp-flags FIN,SYN,RST,ACK SYN -d 192.168.0.0/16
-A DOCKER-USER -j ufw-docker-logging-deny -p tcp -m tcp --tcp-flags FIN,SYN,RST,ACK SYN -d 10.0.0.0/8
-A DOCKER-USER -j ufw-docker-logging-deny -p tcp -m tcp --tcp-flags FIN,SYN,RST,ACK SYN -d 172.16.0.0/12
-A DOCKER-USER -j ufw-docker-logging-deny -p udp -m udp --dport 0:32767 -d 192.168.0.0/16
-A DOCKER-USER -j ufw-docker-logging-deny -p udp -m udp --dport 0:32767 -d 10.0.0.0/8
-A DOCKER-USER -j ufw-docker-logging-deny -p udp -m udp --dport 0:32767 -d 172.16.0.0/12

-A DOCKER-USER -j RETURN

-A ufw-docker-logging-deny -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix "[UFW DOCKER BLOCK] "
-A ufw-docker-logging-deny -j DROP

COMMIT
# END PEYK UFW-DOCKER
`

func applyFirewall(ctx context.Context, _ *Env) error {
	if err := apt(ctx, "install", "ufw"); err != nil {
		return err
	}
	for _, rule := range [][]string{
		{"default", "deny", "incoming"},
		{"default", "allow", "outgoing"},
		{"allow", "OpenSSH"},
		{"allow", "80/tcp"},
		{"allow", "443/tcp"},
		{"allow", "443/udp"}, // HTTP/3
		// Traffic to Docker-published ports traverses FORWARD, not INPUT:
		// without these route rules the DOCKER-USER chain below drops every
		// external packet headed for the Caddy container.
		{"route", "allow", "proto", "tcp", "from", "any", "to", "any", "port", "80"},
		{"route", "allow", "proto", "tcp", "from", "any", "to", "any", "port", "443"},
		{"route", "allow", "proto", "udp", "from", "any", "to", "any", "port", "443"},
	} {
		if err := execx.Run(ctx, "ufw", rule...); err != nil {
			return err
		}
	}
	after, err := os.ReadFile("/etc/ufw/after.rules")
	if err != nil {
		return err
	}
	if !strings.Contains(string(after), ufwDockerMarker) {
		f, err := os.OpenFile("/etc/ufw/after.rules", os.O_APPEND|os.O_WRONLY, 0o640)
		if err != nil {
			return err
		}
		if _, err := f.WriteString(ufwAfterRules); err != nil {
			f.Close()
			return err
		}
		f.Close()
	}
	if err := execx.Run(ctx, "ufw", "--force", "enable"); err != nil {
		return err
	}
	return execx.Run(ctx, "ufw", "reload")
}

func applyFail2ban(ctx context.Context, _ *Env) error {
	if err := apt(ctx, "install", "fail2ban"); err != nil {
		return err
	}
	jail := `[sshd]
enabled = true
backend = systemd
maxretry = 5
findtime = 10m
bantime = 1h
`
	if _, err := writeFileIfChanged("/etc/fail2ban/jail.d/peyk-sshd.conf", jail, 0o644); err != nil {
		return err
	}
	return execx.Run(ctx, "systemctl", "enable", "--now", "fail2ban")
}

// hasAuthorizedKey checks root and all /home users for a non-empty authorized_keys.
func hasAuthorizedKey() bool {
	candidates := []string{"/root/.ssh/authorized_keys"}
	if homes, err := os.ReadDir("/home"); err == nil {
		for _, h := range homes {
			candidates = append(candidates, filepath.Join("/home", h.Name(), ".ssh", "authorized_keys"))
		}
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			return true
		}
	}
	return false
}

func applySSHHardening(ctx context.Context, _ *Env) error {
	if !hasAuthorizedKey() {
		return fmt.Errorf("no authorized SSH key found; refusing to disable password login (add a key first)")
	}
	conf := `# Managed by peyk — SSH hardening
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
X11Forwarding no
MaxAuthTries 4
LoginGraceTime 30
ClientAliveInterval 300
ClientAliveCountMax 2
`
	changed, err := writeFileIfChanged("/etc/ssh/sshd_config.d/99-peyk.conf", conf, 0o644)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	// Validate before reloading so a bad config can never lock us out.
	if err := execx.Run(ctx, "sshd", "-t"); err != nil {
		os.Remove("/etc/ssh/sshd_config.d/99-peyk.conf")
		return fmt.Errorf("sshd config validation failed, change reverted: %w", err)
	}
	return execx.Run(ctx, "systemctl", "reload", "ssh")
}

func applySysctl(ctx context.Context, _ *Env) error {
	conf := `# Managed by peyk — web server tuning
net.core.somaxconn = 4096
net.ipv4.tcp_max_syn_backlog = 4096
net.ipv4.tcp_fin_timeout = 15
net.ipv4.tcp_tw_reuse = 1
net.ipv4.ip_local_port_range = 1024 65535
vm.swappiness = 10
vm.overcommit_memory = 1
fs.inotify.max_user_watches = 524288
fs.inotify.max_user_instances = 512
fs.file-max = 2097152
`
	if _, err := writeFileIfChanged("/etc/sysctl.d/99-peyk.conf", conf, 0o644); err != nil {
		return err
	}
	return execx.Run(ctx, "sysctl", "--system")
}

func applySwap(ctx context.Context, env *Env) error {
	if env.Sys.HasSwap {
		return nil
	}
	sizeMB := env.Sys.MemTotalMB
	if sizeMB > 4096 {
		sizeMB = 4096
	}
	if sizeMB < 1024 {
		sizeMB = 1024
	}
	if err := execx.Run(ctx, "fallocate", "-l", fmt.Sprintf("%dM", sizeMB), "/swapfile"); err != nil {
		return err
	}
	if err := os.Chmod("/swapfile", 0o600); err != nil {
		return err
	}
	if err := execx.Run(ctx, "mkswap", "/swapfile"); err != nil {
		return err
	}
	if err := execx.Run(ctx, "swapon", "/swapfile"); err != nil {
		return err
	}
	fstab, err := os.ReadFile("/etc/fstab")
	if err != nil {
		return err
	}
	if !strings.Contains(string(fstab), "/swapfile") {
		f, err := os.OpenFile("/etc/fstab", os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.WriteString("/swapfile none swap sw 0 0\n"); err != nil {
			return err
		}
	}
	return nil
}

func applyJournaldLimits(ctx context.Context, _ *Env) error {
	conf := "[Journal]\nSystemMaxUse=500M\nMaxRetentionSec=1month\n"
	changed, err := writeFileIfChanged("/etc/systemd/journald.conf.d/99-peyk.conf", conf, 0o644)
	if err != nil || !changed {
		return err
	}
	return execx.Run(ctx, "systemctl", "restart", "systemd-journald")
}

func applyCaddyEdge(ctx context.Context, env *Env) error {
	// Shared edge network that project app containers join.
	if out, _ := execx.Output(ctx, "docker", "network", "ls", "--filter", "name=^peyk-edge$", "--format", "{{.Name}}"); out != "peyk-edge" {
		if err := execx.Run(ctx, "docker", "network", "create", "peyk-edge"); err != nil {
			return err
		}
	}
	return caddy.EnsureEdgeStack(ctx, env.Cfg.ACMEEmail)
}

func applyPeykDaemon(ctx context.Context, env *Env) error {
	unit := fmt.Sprintf(`# Managed by peyk %s
[Unit]
Description=Peyk deployment daemon
After=network-online.target docker.service
Wants=network-online.target

[Service]
ExecStart=%s serve
Restart=on-failure
RestartSec=3
User=root
NoNewPrivileges=false
ProtectHome=read-only

[Install]
WantedBy=multi-user.target
`, env.Ver, config.BinPath())
	if _, err := writeFileIfChanged("/etc/systemd/system/peyk.service", unit, 0o644); err != nil {
		return err
	}
	if err := execx.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	return execx.Run(ctx, "systemctl", "enable", "--now", "peyk")
}

func applyAutoUpdate(ctx context.Context, env *Env) error {
	service := fmt.Sprintf(`[Unit]
Description=Peyk self-update

[Service]
Type=oneshot
ExecStart=%s self-update --if-needed
`, config.BinPath())
	timer := `[Unit]
Description=Daily peyk self-update

[Timer]
OnCalendar=daily
RandomizedDelaySec=6h
Persistent=true

[Install]
WantedBy=timers.target
`
	if _, err := writeFileIfChanged("/etc/systemd/system/peyk-update.service", service, 0o644); err != nil {
		return err
	}
	if _, err := writeFileIfChanged("/etc/systemd/system/peyk-update.timer", timer, 0o644); err != nil {
		return err
	}
	if err := execx.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := execx.Run(ctx, "systemctl", "enable", "--now", "peyk-update.timer"); err != nil {
		return err
	}
	env.Cfg.AutoUpdate = true
	return config.Save(*env.Cfg)
}
