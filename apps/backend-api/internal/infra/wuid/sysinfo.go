package wuid

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
)

type sysinfo struct {
	OS        string
	Host      string
	IPv4Lan   string
	MAC       string
	Disk      string
	Container bool
	KVM       bool
}

func collectSysinfo() sysinfo {
	host, _ := os.Hostname()
	return sysinfo{
		OS: runtime.GOOS + "/" + runtime.GOARCH, Host: host,
		IPv4Lan: allIPv4(), MAC: allMAC(), Disk: disksJSON(),
		Container: isContainer(), KVM: isKVM(),
	}
}

func allIPv4() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var values []string
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagLoopback != 0 || networkInterface.Flags&net.FlagUp == 0 {
			continue
		}
		addresses, _ := networkInterface.Addrs()
		for _, address := range addresses {
			if network, ok := address.(*net.IPNet); ok && network.IP.To4() != nil && !network.IP.IsLoopback() {
				values = append(values, network.IP.String())
			}
		}
	}
	return strings.Join(values, ",")
}

func allMAC() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var values []string
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagLoopback == 0 && len(networkInterface.HardwareAddr) > 0 {
			values = append(values, networkInterface.HardwareAddr.String())
		}
	}
	return strings.Join(values, ",")
}

func disksJSON() string {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return "[]"
	}
	disks := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "loop") && !strings.HasPrefix(name, "ram") {
			disks = append(disks, name)
		}
	}
	data, _ := json.Marshal(disks)
	return string(data)
}

func isContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		value := string(data)
		if strings.Contains(value, "docker") || strings.Contains(value, "kubepods") || strings.Contains(value, "containerd") {
			return true
		}
	}
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

func isKVM() bool {
	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil && strings.Contains(string(data), "hypervisor") {
		return true
	}
	if data, err := os.ReadFile("/sys/class/dmi/id/product_name"); err == nil {
		value := strings.ToLower(string(data))
		return strings.Contains(value, "kvm") || strings.Contains(value, "qemu") || strings.Contains(value, "virtual")
	}
	return false
}

func fromEnv(key string) (uint16, bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return 0, false, nil
	}
	id, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return 0, false, fmt.Errorf("wuid: invalid %s %q: %w", key, value, err)
	}
	return uint16(id), true, nil
}

func newToken() (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
