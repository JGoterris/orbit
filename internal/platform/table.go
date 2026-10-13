package platform

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ProcRow is one process of the table.
type ProcRow struct {
	PPid int
	// CPUSeconds is the cumulative CPU time (user + system)
	CPUSeconds float64
	// RSS is the resident memory in bytes
	RSS int64
}

const clkTck = 100

func linuxTable() map[int]ProcRow {
	table := map[int]ProcRow{}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return table
	}
	pageSize := int64(os.Getpagesize())
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue // process vanished
		}
		stat := string(data)
		i := strings.LastIndex(stat, ")")
		if i < 0 || i+2 > len(stat) {
			continue
		}
		f := strings.Split(stat[i+2:], " ")
		if len(f) < 22 {
			continue
		}
		num := func(s string) float64 { n, _ := strconv.ParseFloat(s, 64); return n }
		table[pid] = ProcRow{
			PPid:       int(num(f[1])),
			CPUSeconds: (num(f[11]) + num(f[12])) / clkTck,
			RSS:        int64(num(f[21])) * pageSize,
		}
	}
	return table
}

var psTimeRe = regexp.MustCompile(`^(?:(\d+)-)?(?:(\d+):)?(\d+):(\d+(?:\.\d+)?)$`)

// ParsePsTime reads the CPU time of `ps`: `[dd-][hh:]mm:ss[.ss]`, in seconds (0 when it is not one).
func ParsePsTime(text string) float64 {
	m := psTimeRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return 0
	}
	f := func(s string) float64 { n, _ := strconv.ParseFloat(s, 64); return n }
	return f(m[1])*86400 + f(m[2])*3600 + f(m[3])*60 + f(m[4])
}

// ParsePsTable reads `ps -axo pid=,ppid=,rss=,time=`.
func ParsePsTable(text string) map[int]ProcRow {
	table := map[int]ProcRow{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		ppid, _ := strconv.Atoi(f[1])
		rssKb, _ := strconv.ParseInt(f[2], 10, 64)
		table[pid] = ProcRow{PPid: ppid, CPUSeconds: ParsePsTime(f[3]), RSS: rssKb * 1024}
	}
	return table
}

// ParseWinTable reads Win32_Process rows as JSON (KernelModeTime / UserModeTime are in 100 ns units). A single
// process comes as an object, several as a list.
func ParseWinTable(text string) map[int]ProcRow {
	table := map[int]ProcRow{}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return table
	}
	rows, ok := parsed.([]any)
	if !ok {
		rows = []any{parsed}
	}
	num := func(v any) float64 {
		switch x := v.(type) {
		case float64:
			return x
		case string:
			f, _ := strconv.ParseFloat(x, 64)
			return f
		}
		return 0
	}
	for _, r := range rows {
		row, ok := r.(map[string]any)
		if !ok {
			continue
		}
		pid, ok := row["ProcessId"].(float64)
		if !ok {
			continue
		}
		table[int(pid)] = ProcRow{
			PPid:       int(num(row["ParentProcessId"])),
			CPUSeconds: (num(row["KernelModeTime"]) + num(row["UserModeTime"])) / 1e7,
			RSS:        int64(num(row["WorkingSetSize"])),
		}
	}
	return table
}

// ProcessTable is a snapshot of every process: pid -> (ppid, cpu seconds, rss bytes).
func ProcessTable() map[int]ProcRow {
	switch {
	case IsLinux:
		return linuxTable()
	case IsMac:
		out, _ := runOutput([]string{"ps", "-axo", "pid=,ppid=,rss=,time="}, 5*time.Second)
		return ParsePsTable(out)
	case IsWindows:
		script := "Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,WorkingSetSize,KernelModeTime,UserModeTime | ConvertTo-Json -Compress"
		out, _ := runOutput([]string{"powershell", "-NoProfile", "-NonInteractive", "-Command", script}, 10*time.Second)
		return ParseWinTable(out)
	}
	return map[int]ProcRow{}
}
