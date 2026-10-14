package resources

import (
	"math"
	"reflect"
	"testing"
)

const (
	mb  = 1024 * 1024
	min = 60_000
)

// series is one bucket per 10 s for `minutes`, with memory given by mem(minute); saw adds a garbage collector
// sawtooth whose peaks stay above the floor.
func series(minutes int, mem func(m float64) float64, saw float64) []Bucket {
	var out []Bucket
	for t := int64(0); t <= int64(minutes)*min; t += BucketMs {
		base := mem(float64(t) / min)
		wobble := 0.0
		if saw != 0 {
			wobble = float64((t/BucketMs)%6) * saw
		}
		out = append(out, Bucket{T: t, Cpu: 1, CpuMax: 1, Mem: base + wobble/2, MemMin: base, MemMax: base + wobble})
	}
	return out
}

func TestHistoryAveragesIntoBucketsAndKeepsAnHour(t *testing.T) {
	h := NewHistory()
	h.Push("a", 10, 100, 0)
	h.Push("a", 30, 300, 2000)
	h.Push("a", 0, 200, 4000)
	b := h.Buckets("a", 0, 0)[0]
	if b.T != 0 || b.CpuMax != 30 || b.MemMin != 100 || b.MemMax != 300 {
		t.Fatalf("bucket = %+v", b)
	}
	if math.Abs(b.Cpu-40.0/3) > 1e-9 || math.Abs(b.Mem-200) > 1e-9 {
		t.Fatalf("averages = %v, %v", b.Cpu, b.Mem)
	}
	for i := int64(1); i <= Buckets+5; i++ {
		h.Push("a", 1, 1, i*BucketMs)
	}
	if n := len(h.Buckets("a", 0, 0)); n != Buckets {
		t.Fatalf("kept %d buckets", n)
	}
	if n := len(h.Buckets("a", 60_000, (Buckets+5)*BucketMs)); n != 7 {
		t.Fatalf("last minute = %d buckets", n)
	}
	h.Clear("a")
	if len(h.Buckets("a", 0, 0)) != 0 {
		t.Fatal("clear did not clear")
	}
}

func TestResample(t *testing.T) {
	if got := Resample([]float64{1, 2, 3}, 5); !reflect.DeepEqual(got, []float64{1, 2, 3}) {
		t.Errorf("short = %v", got)
	}
	if got := Resample([]float64{1, 3, 5, 7}, 2); !reflect.DeepEqual(got, []float64{2, 6}) {
		t.Errorf("averaged = %v", got)
	}
	if got := Resample([]float64{1, 2}, 0); len(got) != 0 {
		t.Errorf("zero = %v", got)
	}
}

func TestLevelHasHysteresis(t *testing.T) {
	cases := []struct {
		mem   float64
		limit int64
		prev  MemLevel
		want  MemLevel
	}{
		{500, 0, "", LevelNone}, {800, 1000, "", LevelNone}, {850, 1000, "", LevelWarn}, {1000, 1000, "", LevelOver},
		{820, 1000, LevelWarn, LevelWarn}, {820, 1000, "", LevelNone}, {790, 1000, LevelWarn, LevelNone},
	}
	for _, c := range cases {
		if got := Level(c.mem, c.limit, c.prev); got != c.want {
			t.Errorf("Level(%v, %d, %q) = %q, want %q", c.mem, c.limit, c.prev, got, c.want)
		}
	}
}

func TestSteadyGrowthIsALeakWithItsEta(t *testing.T) {
	leak := DetectLeak(series(20, func(m float64) float64 { return 100*mb + m*5*mb }, 0), 400*mb)
	if leak == nil {
		t.Fatal("no leak found")
	}
	if math.Abs(leak.PerMin/mb-5) > 0.5 {
		t.Errorf("rate = %.2f MB/min", leak.PerMin/mb)
	}
	if math.Abs(leak.EtaMs/min-40) > 1 {
		t.Errorf("eta = %.1f min", leak.EtaMs/min)
	}
}

func TestSawtoothAroundAFlatFloorIsNotALeak(t *testing.T) {
	if DetectLeak(series(30, func(float64) float64 { return 200 * mb }, 30*mb), 0) != nil {
		t.Fatal("a flat floor is not a leak")
	}
}

func TestALeakStillShowsThroughTheSawtooth(t *testing.T) {
	if DetectLeak(series(25, func(m float64) float64 { return 100*mb + m*4*mb }, 30*mb), 0) == nil {
		t.Fatal("leak not found")
	}
}

func TestNeedsTimeAndRealGrowth(t *testing.T) {
	if DetectLeak(series(10, func(m float64) float64 { return 100*mb + m*20*mb }, 0), 0) != nil {
		t.Error("10 minutes is not enough data")
	}
	if DetectLeak(series(25, func(m float64) float64 { return 100*mb + m*0.1*mb }, 0), 0) != nil {
		t.Error("0.1 MB/min is not real growth")
	}
}

func TestGrowthThatStoppedIsNotALeak(t *testing.T) {
	if DetectLeak(series(40, func(m float64) float64 { return 100*mb + math.Min(m, 8)*20*mb }, 0), 0) != nil {
		t.Fatal("growth that stopped long ago is not a leak")
	}
}

func TestAStepUpAndThenNoiseIsNotALeak(t *testing.T) {
	f := func(m float64) float64 {
		if m < 15 {
			return 100 * mb
		}
		return 300*mb + math.Mod(m*7, 5)*mb
	}
	if DetectLeak(series(30, f, 0), 0) != nil {
		t.Fatal("a step is not a leak")
	}
}
