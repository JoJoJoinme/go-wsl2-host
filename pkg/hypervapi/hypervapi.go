package hypervapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
)

const (
	defaultDomainNameSuffix = ".example.com"
	defaultComment          = "managed by api - hyper-vm"
)

type VMManager interface {
	GetRunningVMs() ([]VMInfo, error)
	GetVMIPs(vmName string) ([]string, error)
}

type IP struct {
	IPv4List []string
	IPv6List []string
}

func (ip *IP) String() string {
	if ip == nil {
		return ""
	}
	return fmt.Sprintf("ipv4:%s, ipv6:%s", ip.IPv4List, ip.IPv6List)
}

type VMInfo struct {
	Name   string
	IPInfo *IP
}

func (v *VMInfo) GeDefaulttDomainName() string {
	return v.Name + defaultDomainNameSuffix
}

func (v *VMInfo) GetIP() []string {
	if v.IPInfo == nil {
		return []string{}
	}
	// 预分配足够的容量以避免多次分配
	ips := make([]string, 0, len(v.IPInfo.IPv4List)+len(v.IPInfo.IPv6List))
	ips = append(ips, v.IPInfo.IPv4List...)
	ips = append(ips, v.IPInfo.IPv6List...)
	return ips
}

func (v *VMInfo) GetIPV4() []string {
	if v.IPInfo == nil {
		return []string{}
	}
	// 预分配足够的容量以避免多次分配
	ips := make([]string, 0, len(v.IPInfo.IPv4List))
	ips = append(ips, v.IPInfo.IPv4List...)
	return ips
}

func (v *VMInfo) GetComent() string {
	return defaultComment
}

type HyperVManager struct{}

func NewHyperVManager() *HyperVManager {
	return &HyperVManager{}
}

func (h *HyperVManager) GetRunningVMs() ([]*VMInfo, error) {
	vmNames, err := h.GetRunningVMNames()
	if err != nil {
		return nil, fmt.Errorf("get VM names failed: %w", err)
	}

	vmInfos := make([]*VMInfo, 0, len(vmNames))
	var errs []string

	for _, vmName := range vmNames {
		vmInfo := &VMInfo{Name: vmName}
		vmIPs, err := h.GetVMIPByVMName(vmName)
		if err != nil {
			errs = append(errs, fmt.Sprintf("get IP for VM %q failed: %v", vmName, err))
			continue
		}
		vmInfo.IPInfo = vmIPs
		fmt.Printf("vm:%v, ipInfo:%s\n", vmName, vmIPs)
		vmInfos = append(vmInfos, vmInfo)
	}

	if len(errs) > 0 {
		return vmInfos, fmt.Errorf("errors occurred while getting VM information: %s", strings.Join(errs, "; "))
	}

	return vmInfos, nil
}

func (h *HyperVManager) GetRunningVMNames() ([]string, error) {
	// Implementation to get running VMs using PowerShell commands
	cmd := exec.Command("powershell", "Get-VM | Where-Object {$_.State -eq 'Running'} | Select-Object -ExpandProperty Name")
	var out bytes.Buffer
	var errStr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errStr
	err := cmd.Run()
	if err != nil {
		fmt.Printf("Running powershell 'powershell Get-VM | Where-Object {$_.State -eq 'Running'} | Select-Object -ExpandProperty Name' failed with err:%v\n, output:%v\n", errStr.String(), out.String())
		return nil, err
	}
	vms := strings.Split(strings.TrimSpace(out.String()), "\r\n")
	fmt.Println("Get running vm names", vms)
	return vms, nil
}

func (h *HyperVManager) GetVMIPByVMName(vmName string) (*IP, error) {
	// Implementation to get IPs of a VM using PowerShell commands
	cmd := exec.Command("powershell", fmt.Sprintf("Get-VMNetworkAdapter -VMName %s | Select-Object -ExpandProperty IPAddresses", vmName))
	var out bytes.Buffer
	var errStr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errStr
	err := cmd.Run()
	if err != nil {
		fmt.Printf("Running powershell 'powershell Get-VMNetworkAdapter -VMName %s | Select-Object -ExpandProperty IPAddresses failed with err:%v\n, output:%v\n", vmName, errStr.String(), out.String())
		return nil, err
	}
	ipInfo := &IP{}
	ipList := strings.Split(strings.TrimSpace(out.String()), "\r\n")
	for _, ip := range ipList {
		ip = strings.TrimSpace(ip)
		if parsedIP := net.ParseIP(ip); parsedIP != nil {
			if parsedIP.To4() != nil {
				ipInfo.IPv4List = append(ipInfo.IPv4List, ip)
			} else {
				ipInfo.IPv6List = append(ipInfo.IPv6List, ip)
			}
		}
	}

	return ipInfo, nil
}

// NetworkAdapterInfo holds information about a VM's network adapter
type NetworkAdapterInfo struct {
	Name        string   `json:"Name"`
	SwitchName  string   `json:"SwitchName"`
	IPAddresses []string `json:"IPAddresses"`
}

// GetVMPreferredIPByVMName retrieves the preferred IPv4 address for a Hyper-V VM.
// It prioritizes IPs from the "Default Switch" and excludes APIPA addresses.
func GetVMPreferredIPByVMName(vmName string) (string, error) {
	cmd := exec.Command("powershell", fmt.Sprintf("Get-VMNetworkAdapter -VMName '%s' | Select-Object Name, SwitchName, IPAddresses | ConvertTo-Json", vmName))
	var out bytes.Buffer
	var errStr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errStr
	err := cmd.Run()
	if err != nil {
		output := out.String() + errStr.String()
		if strings.Contains(output, "No object was found") || strings.Contains(output, "cannot find a virtual machine") {
			return "", fmt.Errorf("vm '%s' not found or has no network adapters", vmName)
		}
		return "", fmt.Errorf("failed to execute PowerShell command: %w, output: %s", err, output)
	}

	jsonOut := strings.TrimSpace(out.String())
	if !strings.HasPrefix(jsonOut, "[") && strings.HasPrefix(jsonOut, "{") {
		jsonOut = "[" + jsonOut + "]"
	}

	var adapters []NetworkAdapterInfo
	err = json.Unmarshal([]byte(jsonOut), &adapters)
	if err != nil {
		return "", fmt.Errorf("failed to parse PowerShell JSON output: %w, output: %s", err, jsonOut)
	}

	const apipaFirstOctet = 169
	const apipaSecondOctet = 254
	preferredSwitch := "Default Switch"

	var preferredIP string
	var fallbackIP string

	for _, adapter := range adapters {
		for _, ipStr := range adapter.IPAddresses {
			ip := net.ParseIP(ipStr)
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.To4() == nil {
				continue
			}
			if ip[0] == apipaFirstOctet && ip[1] == apipaSecondOctet {
				continue
			}
			if adapter.SwitchName == preferredSwitch && preferredIP == "" {
				preferredIP = ip.String()
			}
			if fallbackIP == "" {
				fallbackIP = ip.String()
			}
		}
	}

	if preferredIP != "" {
		return preferredIP, nil
	}
	if fallbackIP != "" {
		return fallbackIP, nil
	}
	return "", fmt.Errorf("no suitable IPv4 address found for VM '%s'", vmName)
}

// GetVMIPByVMName retrieves all IP addresses for a specific Hyper-V VM.
// Consider using GetVMPreferredIPByVMName for a single, prioritized IP.
func GetVMIPByVMName(vmName string) ([]string, error) {
	cmd := exec.Command("powershell", fmt.Sprintf("Get-VMNetworkAdapter -VMName '%s' | Select-Object -ExpandProperty IPAddresses", vmName))
	var out bytes.Buffer
	var errStr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errStr
	err := cmd.Run()
	if err != nil {
		output := out.String() + errStr.String()
		if strings.Contains(output, "No object was found") || strings.Contains(output, "cannot find a virtual machine") {
			return nil, fmt.Errorf("vm '%s' not found", vmName)
		}
		return nil, fmt.Errorf("failed to execute PowerShell command: %w, output: %s", err, output)
	}

	ips := strings.Fields(out.String())
	if len(ips) == 0 && strings.TrimSpace(out.String()) != "" {
		ips = []string{strings.TrimSpace(out.String())}
	} else if len(ips) == 0 {
		return nil, fmt.Errorf("no IP addresses found for VM '%s'", vmName)
	}

	var validIPs []string
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip != nil {
			validIPs = append(validIPs, ip.String())
		}
	}

	if len(validIPs) == 0 {
		return nil, fmt.Errorf("no valid IP addresses found after parsing for VM '%s'", vmName)
	}

	return validIPs, nil
}
