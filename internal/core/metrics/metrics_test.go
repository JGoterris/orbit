package metrics

import (
	"os"
	"testing"
)

func TestContainerLimitIgnoresTheHostsMemory(t *testing.T) {
	host := int64(16 << 30)
	if got := ContainerLimit("1GiB", host); got != 1<<30 {
		t.Errorf("1GiB = %d", got)
	}
	if got := ContainerLimit("15.6GiB", host); got != 0 {
		t.Errorf("15.6GiB = %d, want no limit", got)
	}
}

func TestParseSizeUnits(t *testing.T) {
	cases := map[string]float64{"12.5MiB": 12.5 * 1024 * 1024, "1GiB": 1 << 30, "800kB": 800_000, "3B": 3, "": 0}
	for in, want := range cases {
		if got := ParseSize(in); got != want {
			t.Errorf("ParseSize(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestFormatting(t *testing.T) {
	bytes := map[float64]string{500: "500B", 2048: "2K", 5 * 1024 * 1024: "5.0M", 50 * 1024 * 1024: "50M", 1.5 * 1024 * 1024 * 1024: "1.5G"}
	for in, want := range bytes {
		if got := FormatBytes(in); got != want {
			t.Errorf("FormatBytes(%v) = %q, want %q", in, got, want)
		}
	}
	durs := map[float64]string{45_000: "45s", 185_000: "3m05s", 7_800_000: "2h10m", 100_800_000: "1d4h"}
	for in, want := range durs {
		if got := FormatDuration(in); got != want {
			t.Errorf("FormatDuration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestProcessSamplerSeesUs(t *testing.T) {
	s := NewProcessSampler()
	pid := os.Getpid()
	s.Sample([]int{pid})
	smp, ok := s.Sample([]int{pid})[pid]
	if !ok {
		t.Skip("process table is not available here")
	}
	if smp.Mem <= 0 {
		t.Fatalf("sample = %+v", smp)
	}
}
