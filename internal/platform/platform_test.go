package platform

import (
	"os"
	"os/exec"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func TestShellArgv(t *testing.T) {
	t.Setenv("ComSpec", "")
	if got := ShellArgv("echo hi", "", false); !reflect.DeepEqual(got, []string{"/bin/sh", "-c", "echo hi"}) {
		t.Errorf("posix = %v", got)
	}
	if got := ShellArgv("echo hi", "", true); !reflect.DeepEqual(got, []string{"cmd.exe", "/d", "/s", "/c", "echo hi"}) {
		t.Errorf("windows = %v", got)
	}
}

func TestShellCommandQuotesForCmd(t *testing.T) {
	t.Setenv("ComSpec", "")
	argv, verbatim := ShellCommand(`bun -e "x()"`, "", true)
	if !verbatim || !reflect.DeepEqual(argv[1:], []string{"/d", "/s", "/c", `"bun -e "x()""`}) {
		t.Errorf("cmd: %v verbatim=%v", argv, verbatim)
	}
	if _, v := ShellCommand("ls", "pwsh", true); v {
		t.Error("pwsh must not be verbatim")
	}
	argv, v := ShellCommand("ls", "", false)
	if v || !reflect.DeepEqual(argv, []string{"/bin/sh", "-c", "ls"}) {
		t.Errorf("posix: %v %v", argv, v)
	}
}

func TestShellPicksFlagsThatSuitIt(t *testing.T) {
	cases := []struct {
		shell string
		want  []string
	}{
		{"pwsh", []string{"pwsh", "-NoProfile", "-Command", "ls"}},
		{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, []string{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, "-NoProfile", "-Command", "ls"}},
		{"cmd.exe", []string{"cmd.exe", "/d", "/s", "/c", "ls"}},
		{"/usr/bin/bash", []string{"/usr/bin/bash", "-c", "ls"}},
	}
	for _, c := range cases {
		if got := ShellArgv("ls", c.shell, false); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.shell, got, c.want)
		}
	}
}

func TestParsePsTime(t *testing.T) {
	for in, want := range map[string]float64{"0:01.50": 1.5, "12:30.00": 750, "1:02:03": 3723, "2-00:00:01": 172801, "nonsense": 0} {
		if got := ParsePsTime(in); got != want {
			t.Errorf("ParsePsTime(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParsePsTable(t *testing.T) {
	tbl := ParsePsTable("    1     0  1024  0:00.10\n  200     1  2048  1:00.00\nbad line\n")
	if got := tbl[200]; got != (ProcRow{PPid: 1, CPUSeconds: 60, RSS: 2048 * 1024}) {
		t.Errorf("row = %+v", got)
	}
	if len(tbl) != 2 {
		t.Errorf("rows = %d, want 2", len(tbl))
	}
}

func TestParseWinTable(t *testing.T) {
	row := `{"ProcessId":10,"ParentProcessId":4,"WorkingSetSize":4096,"KernelModeTime":10000000,"UserModeTime":20000000}`
	if got := ParseWinTable("[" + row + "]")[10]; got != (ProcRow{PPid: 4, CPUSeconds: 3, RSS: 4096}) {
		t.Errorf("list: %+v", got)
	}
	if ParseWinTable(row)[10].RSS != 4096 {
		t.Error("a single process is an object, not a list")
	}
	if len(ParseWinTable("not json")) != 0 {
		t.Error("garbage must give an empty table")
	}
}

func TestXDGDirectoriesWin(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/x/state")
	t.Setenv("XDG_CONFIG_HOME", "/x/config")
	if StateBase() != "/x/state" || ConfigBase() != "/x/config" {
		t.Errorf("state = %s, config = %s", StateBase(), ConfigBase())
	}
}

func TestLiveProcessQueriesOnOurselves(t *testing.T) {
	pid := os.Getpid()
	if !PidAlive(pid) {
		t.Fatal("we are alive")
	}
	if PidAlive(0) || PidAlive(-1) {
		t.Fatal("pid 0 and negatives are never alive")
	}
	start, ok := ProcStartTime(pid)
	if !ok {
		t.Skip("start time is not available here")
	}
	if again, _ := ProcStartTime(pid); again != start {
		t.Fatal("start time must be stable")
	}
}

func TestProcessTableSeesUs(t *testing.T) {
	row, ok := ProcessTable()[os.Getpid()]
	if !ok {
		t.Skip("process table is not available here")
	}
	if row.RSS <= 0 || row.PPid <= 0 {
		t.Fatalf("row = %+v", row)
	}
}

func TestKillTreeTakesChildrenDown(t *testing.T) {
	if IsWindows {
		t.Skip("posix process groups")
	}
	// a shell that starts a background sleep, prints its pid and waits
	cmd := exec.Command("/bin/sh", "-c", "sleep 60 & echo $!; wait")
	SetDetached(cmd)
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	buf := make([]byte, 32)
	n, _ := out.Read(buf)
	var child int
	for _, c := range string(buf[:n]) {
		if c >= '0' && c <= '9' {
			child = child*10 + int(c-'0')
		}
	}
	if !PidAlive(child) || !TreeAlive(cmd.Process.Pid) {
		t.Fatalf("child %d / tree %d should be alive", child, cmd.Process.Pid)
	}
	KillTree(cmd.Process.Pid, syscall.SIGKILL)
	cmd.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for PidAlive(child) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if PidAlive(child) {
		t.Fatal("the child survived its group being killed")
	}
}
