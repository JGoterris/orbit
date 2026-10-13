package proc

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestExecCapturesOutputAndCode(t *testing.T) {
	r := Exec([]string{"/bin/sh", "-c", "echo out; echo err >&2; exit 3"}, Options{})
	if r.Code != 3 || strings.TrimSpace(r.Stdout) != "out" || strings.TrimSpace(r.Stderr) != "err" {
		t.Fatalf("result = %+v", r)
	}
}

func TestExecInputAndEnv(t *testing.T) {
	in := "hello"
	r := Exec([]string{"/bin/sh", "-c", "cat; echo $ORBIT_T"}, Options{Input: &in, Env: EnvList(map[string]string{"ORBIT_T": "v"})})
	if r.Code != 0 || r.Stdout != "hellov\n" {
		t.Fatalf("result = %+v", r)
	}
}

func TestExecMissingProgram(t *testing.T) {
	r := Exec([]string{"/definitely/not/here"}, Options{})
	if r.Code != 127 || r.Stderr == "" {
		t.Fatalf("result = %+v", r)
	}
}

func TestExecTimeoutKills(t *testing.T) {
	start := time.Now()
	r := Exec([]string{"/bin/sh", "-c", "sleep 30"}, Options{Timeout: 150 * time.Millisecond})
	if r.Code == 0 || time.Since(start) > 5*time.Second {
		t.Fatalf("result = %+v after %v", r, time.Since(start))
	}
}

func TestIsPortOpen(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if !IsPortOpen(port, "", 0) {
		t.Error("listening port reported closed")
	}
	l.Close()
	if IsPortOpen(port, "", 200*time.Millisecond) {
		t.Error("closed port reported open")
	}
}
