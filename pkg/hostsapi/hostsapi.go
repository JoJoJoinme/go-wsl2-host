package hostsapi

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

const hostspath = "C:/Windows/System32/drivers/etc/hosts"

// HostEntry data structure for IP and hostnames
type HostEntry struct {
	idx      int
	IP       string
	Hostname string
	Comment  string
}

func (e *HostEntry) String() string {
	return fmt.Sprintf("id:%d, ip:%s, hostname:%s, comment:%s", e.idx, e.IP, e.Hostname, e.Comment)
}

// HostsAPI data structure
type HostsAPI struct {
	filter    string
	hostsfile *os.File
	entries   map[string]*HostEntry
	remidxs   map[int]interface{}
}

func parseHostfileLine(idx int, line string) ([]*HostEntry, error) {
	if len(line) <= 0 {
		return nil, errors.New("invalid line")
	}
	line = strings.TrimSpace(line)
	if line[0] == '#' {
		return nil, errors.New("comment line")
	}
	fields := strings.Fields(line)
	var ip string
	var hostnames []string
	var comment string
	var commentidx int
	for i, f := range fields {
		if f == "" {
			continue
		}
		if f[0] == '#' { // inline comment
			commentidx = i + 1
			break // don't process any more
		}
		if i == 0 {
			ip = f
		} else {
			hostnames = append(hostnames, f)
		}
	}
	if commentidx > 0 {
		comment = strings.Join(fields[commentidx:], " ")
	}
	if ip == "" || len(hostnames) == 0 {
		return nil, fmt.Errorf("invalid fields for line: %q", line)
	}
	var entries []*HostEntry
	for _, hostname := range hostnames {
		entries = append(entries, &HostEntry{
			idx:      idx,
			IP:       ip,
			Hostname: hostname,
			Comment:  comment,
		})
	}

	return entries, nil
}

func (h *HostsAPI) loadAndParse() error {
	scanner := bufio.NewScanner(h.hostsfile)
	idx := 0
	for scanner.Scan() {
		line := scanner.Text()
		entries, err := parseHostfileLine(idx, line)
		idx++
		if err != nil {
			// log.Println(err) // debug
			continue
		}
		for _, e := range entries {
			if h.filter == "" || strings.Contains(e.Comment, h.filter) {
				h.entries[e.Hostname] = e
				h.remidxs[e.idx] = nil
			}
		}
	}
	h.hostsfile.Seek(0, 0)
	return nil
}

// CreateAPI creates a new instance of the hosts file API
// Call Close() when finished
// `filter` proves ability to filter by string contains
func CreateAPI(filter string) (*HostsAPI, error) {
	f, err := os.Open(hostspath)
	if err != nil {
		return nil, fmt.Errorf("failed to open hosts file: %w", err)
	}
	h := &HostsAPI{
		filter:    filter,
		remidxs:   make(map[int]interface{}),
		entries:   make(map[string]*HostEntry),
		hostsfile: f,
	}
	err = h.loadAndParse()
	if err != nil {
		return nil, fmt.Errorf("failed to parse hosts file: %w", err)
	}
	return h, nil
}

// Close closes the hosts file
func (h *HostsAPI) Close() error {
	err := h.hostsfile.Close()
	if err != nil {
		return fmt.Errorf("failed to close hosts file: %w", err)
	}

	return nil
}

// Entries returns parsed entries of host file
func (h *HostsAPI) Entries() map[string]*HostEntry {
	return h.entries
}

// RemoveEntry removes existing entry from hosts file
func (h *HostsAPI) RemoveEntry(hostname string) error {
	if _, exists := h.entries[hostname]; exists {
		delete(h.entries, hostname)
	} else {
		return fmt.Errorf("failed to remove, hostname does not exist: %s", hostname)
	}
	return nil
}

// AddEntry adds a new HostEntry
func (h *HostsAPI) AddEntry(entry *HostEntry) error {
	if _, exists := h.entries[entry.Hostname]; exists {
		return fmt.Errorf("failed to add entry, hostname already exists: %s", entry.Hostname)
	}

	h.entries[entry.Hostname] = entry

	return nil
}

func (h *HostsAPI) IsUpsertEntry(entry *HostEntry) bool {
	if oldEntry, exists := h.entries[entry.Hostname]; exists {
		if oldEntry == entry {
			fmt.Printf("entry is same, no need to update, entry:%s\n", entry)
			return false
		}
	}
	fmt.Printf("upsert entry:%s\n", entry)
	h.entries[entry.Hostname] = entry
	return true
}

// Write
func (h *HostsAPI) Write() error {
	var outbuf bytes.Buffer

	// first remove all current entries
	scanner := bufio.NewScanner(h.hostsfile)
	for idx := 0; scanner.Scan() == true; idx++ {
		line := scanner.Text()
		if _, exists := h.remidxs[idx]; !exists {
			outbuf.WriteString(line)
			outbuf.WriteString("\r\n")
		}
	}

	// append entries to file
	for _, e := range h.entries {
		var comment string
		if e.Comment != "" {
			comment = fmt.Sprintf("    # %s", e.Comment)
		}
		outbuf.WriteString(fmt.Sprintf("%s %s%s\r\n", e.IP, e.Hostname, comment))
	}

	f, err := os.OpenFile(hostspath, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open hosts file for writing: %w", err)
	}
	defer f.Close()

	f.Write(outbuf.Bytes())
	err = f.Sync()
	if err != nil {
		return err
	}

	return nil
}

// GetHostIP returns the IP address of Hyper-V Switch on the host connected to WSL
func GetHostIP() (string, error) {
	cmd := exec.Command("netsh", "interface", "ip", "show", "address", "vEthernet (WSL)") //, "|", "findstr", "IP Address", "|", "%", "{", "$_", "-replace", "IP Address:", "", "}", "|", "%", "{", "$_", "-replace", " ", "", "}")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	// If system language not english, the output not "IP Address". such as in chinese it's "IP 地址".
	// And the output no have other such as "IP", so we can only match the "IP".
	// If need change reglar, make sure the other language is work OK.
	// In chinese language os, the output is:
	// "\r\n\xbdӿ\xda \"vEthernet (WSL)\" \xb5\xc4\xc5\xe4\xd6\xc3\r\n    DHCP \xd2\xd1\xc6\xf4\xd3\xc3:                          \xb7\xf1\r\n    IP \xb5\xd8ַ:                           172.18.144.1\r\n    \xd7\xd3\xcd\xf8ǰ\u05fa:                        172.18.144.0/20 (\xd1\xda\xc2\xeb 255.255.240.0)\r\n    InterfaceMetric:                      15\r\n\r\n"
	ipRegex := regexp.MustCompile("IP.*:? \040*(.*)\r\n")
	ipString := ipRegex.FindStringSubmatch(string(out))
	if len(ipString) != 2 {
		return "", errors.New(`netsh interface ip show address "vEthernet (WSL)"`)
	}
	return ipString[1], nil
}

func GetHostIPV2() (string, error) {
    cmd := exec.Command("powershell", "-Command", "(Get-NetIPAddress | Where-Object {$_.InterfaceAlias -like '*WSL*' -and $_.AddressFamily -eq 'IPv4'}).IPAddress")

    out, err := cmd.CombinedOutput()
    if err != nil {
        return "", fmt.Errorf("failed to execute PowerShell command with output: %v", string(out))
    }

    // Trim any whitespace or newline characters from the output
    ip := strings.TrimSpace(string(out))

    if ip == "" {
        return "", errors.New("no WSL IP address found")
    }

    return ip, nil
}

