// Package sysinfo detects the host operating system and resources.
package sysinfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// Info describes the host.
type Info struct {
	ID        string // "ubuntu"
	VersionID string // "22.04", "24.04"
	MemTotalMB int
	HasSwap    bool
}

// Detect reads /etc/os-release and /proc/meminfo. On non-Linux hosts the
// zero-value fields simply stay empty; callers gate on SupportedUbuntu.
func Detect() Info {
	var in Info
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			v = strings.Trim(v, `"`)
			switch k {
			case "ID":
				in.ID = v
			case "VERSION_ID":
				in.VersionID = v
			}
		}
	}
	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) >= 2 && fields[0] == "MemTotal:" {
				kb, _ := strconv.Atoi(fields[1])
				in.MemTotalMB = kb / 1024
			}
			if len(fields) >= 2 && fields[0] == "SwapTotal:" {
				kb, _ := strconv.Atoi(fields[1])
				in.HasSwap = kb > 0
			}
		}
	}
	return in
}

// SupportedUbuntu reports whether the host is a supported Ubuntu LTS.
func (i Info) SupportedUbuntu() bool {
	return i.ID == "ubuntu" && (i.VersionID == "22.04" || i.VersionID == "24.04")
}

// IsRoot reports whether the process runs as root.
func IsRoot() bool { return os.Geteuid() == 0 }
