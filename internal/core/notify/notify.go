// Package notify tells the desktop when a service goes down (and when it comes back).
package notify

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/JGoterris/orbit/internal/core/metrics"
	"github.com/JGoterris/orbit/internal/core/resources"
	"github.com/JGoterris/orbit/internal/userconfig"
)

// Notice is one desktop notification.
type Notice struct {
	Title string
	Body  string
	// Urgent: a service went down (as opposed to coming back)
	Urgent bool
}

// BackendEnv is what picking a backend depends on; tests give their own.
type BackendEnv struct {
	Platform string
	WSL      bool
	Which    func(cmd string) string
}

// Backend is the program that shows a notification on this system.
type Backend struct {
	Name string
	Argv func(n Notice) []string
}

const maxBody = 240

var psQuotes = regexp.MustCompile("['‘’‚‛]")

// psString: PowerShell also treats the typographic single quotes as quote characters inside '…'.
func psString(s string) string { return "'" + psQuotes.ReplaceAllString(s, "''") + "'" }

func clip(s string) string {
	r := []rune(s)
	if len(r) > maxBody {
		return string(r[:maxBody])
	}
	return s
}

// PowershellScript is a Windows toast. The texts are PowerShell strings assigned with InnerText: no script or XML
// injection.
func PowershellScript(n Notice) string {
	return strings.Join([]string{
		"$ErrorActionPreference = 'Stop'",
		"[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null",
		"$t = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)",
		"$x = $t.GetElementsByTagName('text')",
		"$x.Item(0).InnerText = " + psString(n.Title),
		"$x.Item(1).InnerText = " + psString(clip(n.Body)),
		"$app = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\\WindowsPowerShell\\v1.0\\powershell.exe'",
		"[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($app).Show([Windows.UI.Notifications.ToastNotification]::new($t))",
	}, "\n")
}

func utf16le(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(units)*2)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

// PickBackend is the program that shows a desktop notification on this system, if there is one.
func PickBackend(env BackendEnv) *Backend {
	ps := env.Which("powershell.exe")
	if ps == "" {
		ps = env.Which("powershell")
	}
	// WSLg rarely shows notify-send, so on WSL the Windows side is the one that reaches the user
	if (env.WSL || env.Platform == "windows") && ps != "" {
		return &Backend{Name: "powershell", Argv: func(n Notice) []string {
			enc := base64.StdEncoding.EncodeToString(utf16le(PowershellScript(n)))
			return []string{ps, "-NoProfile", "-NonInteractive", "-EncodedCommand", enc}
		}}
	}
	if env.Platform == "darwin" {
		if osa := env.Which("osascript"); osa != "" {
			return &Backend{Name: "osascript", Argv: func(n Notice) []string {
				// the texts travel as argv, never inside the script
				return []string{osa, "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", n.Title, clip(n.Body)}
			}}
		}
	}
	if env.Platform == "linux" {
		if ns := env.Which("notify-send"); ns != "" {
			return &Backend{Name: "notify-send", Argv: func(n Notice) []string {
				urgency := "normal"
				if n.Urgent {
					urgency = "critical"
				}
				return []string{ns, "-a", "orbit", "-u", urgency, "--", n.Title, clip(n.Body)}
			}}
		}
	}
	return nil
}

func isWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	return err == nil && strings.Contains(strings.ToLower(string(data)), "microsoft")
}

var (
	backendOnce sync.Once
	backend     *Backend
)

// DefaultBackend is the backend for this machine.
func DefaultBackend() *Backend {
	backendOnce.Do(func() {
		backend = PickBackend(BackendEnv{Platform: runtime.GOOS, WSL: isWSL(), Which: func(c string) string {
			p, err := exec.LookPath(c)
			if err != nil {
				return ""
			}
			return p
		}})
	})
	return backend
}

// SendDesktop is fire and forget: a notification that cannot be shown is not worth an error.
func SendDesktop(n Notice) {
	b := DefaultBackend()
	if b == nil {
		return
	}
	argv := b.Argv(n)
	cmd := exec.Command(argv[0], argv[1:]...)
	if cmd.Start() != nil {
		return
	}
	timer := time.AfterFunc(10*time.Second, func() { cmd.Process.Kill() })
	go func() { cmd.Wait(); timer.Stop() }()
}

// What the notifier needs to know about a service.
type ServiceView struct {
	Status    string
	Error     string
	Health    string
	Resources *resources.Info
}

// Source is what a notifier watches: a supervisor.
type Source interface {
	ProjectName() string
	Names() []string
	ServiceView(name string) ServiceView
	OnChange(fn func(name string)) (cancel func())
}

type kind string

const (
	kCrashed   kind = "crashed"
	kFailed    kind = "failed"
	kUnhealthy kind = "unhealthy"
	kRecovered kind = "recovered"
	kMemory    kind = "memory"
	kLeak      kind = "leak"
)

type event struct {
	kind   kind
	name   string
	detail string
}

// Options tune a notifier; tests inject the clock and the sender.
type Options struct {
	Send func(Notice)
	// Enabled is asked every time something is about to be sent, so toggling it takes effect without a restart
	Enabled func() bool
	// GroupMs: events this close together go out as one notification (a failure often takes its dependents with it)
	GroupMs int64
	// ThrottleMs: the same event of the same service is reported at most this often (a restart loop)
	ThrottleMs int64
	Now        func() int64
}

func describe(e event) string {
	suffix := func(prefix string) string {
		if e.detail == "" {
			return ""
		}
		return prefix + e.detail
	}
	switch e.kind {
	case kRecovered:
		return fmt.Sprintf("✔  %s recovered", e.name)
	case kMemory:
		d := ""
		if e.detail != "" {
			d = " (" + e.detail + ")"
		}
		return fmt.Sprintf("▲  %s is over its memory limit%s", e.name, d)
	case kLeak:
		return fmt.Sprintf("↗  %s may be leaking memory%s", e.name, suffix(": "))
	case kUnhealthy:
		return fmt.Sprintf("●  %s is unhealthy%s", e.name, suffix(": "))
	}
	return fmt.Sprintf("✖  %s %s%s", e.name, e.kind, suffix(": "))
}

// Attach sends a desktop notification when a service of src crashes, fails or turns unhealthy, and when it comes
// back. It is meant for whoever runs the services (the daemon), so it works with every terminal closed. It returns
// what stops it.
func Attach(src Source, opts Options) (stop func()) {
	send := opts.Send
	if send == nil {
		send = SendDesktop
	}
	enabled := opts.Enabled
	if enabled == nil {
		enabled = func() bool { return userconfig.Read().NotificationsEnabled() }
	}
	groupMs := opts.GroupMs
	if groupMs == 0 {
		groupMs = 1500
	}
	throttleMs := opts.ThrottleMs
	if throttleMs == 0 {
		throttleMs = 60_000
	}
	now := opts.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}

	var mu sync.Mutex
	last := map[string]string{}
	for _, n := range src.Names() {
		last[n] = src.ServiceView(n).Status
	}
	down := map[string]bool{}
	sent := map[string]int64{}
	lastAlert := map[string][2]bool{} // over, leak
	var pending []event
	var timer *time.Timer

	flush := func() {
		mu.Lock()
		timer = nil
		events := pending
		pending = nil
		mu.Unlock()
		if len(events) == 0 || !enabled() {
			return
		}
		var alerts, downs, ups []event
		for _, e := range events {
			switch e.kind {
			case kMemory, kLeak:
				alerts = append(alerts, e)
			case kRecovered:
				ups = append(ups, e)
			default:
				downs = append(downs, e)
			}
		}
		var lines []string
		for _, e := range alerts {
			lines = append(lines, describe(e))
		}
		names := func(es []event) string {
			n := make([]string, len(es))
			for i, e := range es {
				n[i] = e.name
			}
			return strings.Join(n, ", ")
		}
		switch {
		case len(downs) == 1:
			lines = append(lines, describe(downs[0]))
		case len(downs) > 1:
			lines = append(lines, fmt.Sprintf("✖  %d services down: %s", len(downs), names(downs)))
		}
		switch {
		case len(ups) == 1:
			lines = append(lines, describe(ups[0]))
		case len(ups) > 1:
			lines = append(lines, fmt.Sprintf("✔  %d services recovered: %s", len(ups), names(ups)))
		}
		urgent := len(downs) > 0
		for _, e := range alerts {
			if e.kind == kMemory {
				urgent = true
			}
		}
		send(Notice{Title: "orbit · " + src.ProjectName(), Body: strings.Join(lines, "\n"), Urgent: urgent})
	}

	// requires mu
	arm := func() {
		if timer == nil {
			timer = time.AfterFunc(time.Duration(groupMs)*time.Millisecond, flush)
		}
	}
	queue := func(e event, key string, every int64) {
		at := now()
		if prev, ok := sent[key]; ok && at-prev < every {
			return
		}
		sent[key] = at
		pending = append(pending, e)
		arm()
	}

	onChange := func(name string) {
		if name == "" {
			return
		}
		v := src.ServiceView(name)
		mu.Lock()
		defer mu.Unlock()

		// memory alerts are reported when they start, not while they last
		prevAlert := lastAlert[name]
		cur := [2]bool{v.Resources != nil && v.Resources.Level == resources.LevelOver, v.Resources != nil && v.Resources.Leak != nil}
		lastAlert[name] = cur
		if cur[0] && !prevAlert[0] {
			d := ""
			if v.Resources.MemLimit > 0 {
				d = "limit " + metrics.FormatBytes(float64(v.Resources.MemLimit))
			}
			queue(event{kMemory, name, d}, name+":memory", throttleMs)
		}
		if cur[1] && !prevAlert[1] {
			l := v.Resources.Leak
			d := "+" + metrics.FormatBytes(l.PerMin) + "/min"
			if l.EtaMs > 0 {
				d += ", limit in ~" + metrics.FormatDuration(l.EtaMs)
			}
			queue(event{kLeak, name, d}, name+":leak", max(throttleMs, 15*60_000))
		}

		prev := last[name]
		last[name] = v.Status
		if prev == v.Status {
			return
		}
		var k kind
		switch {
		case v.Status == "crashed" || v.Status == "failed":
			k = kind(v.Status)
		case v.Status == "unhealthy":
			k = kUnhealthy
		case (v.Status == "healthy" || v.Status == "running") && down[name]:
			k = kRecovered
		default:
			return
		}
		key := name + ":" + string(k)
		at := now()
		if p, ok := sent[key]; ok && at-p < throttleMs {
			return
		}
		sent[key] = at
		if k == kRecovered {
			delete(down, name)
		} else {
			down[name] = true
		}
		detail := v.Error
		if k == kUnhealthy {
			detail = v.Health
		}
		pending = append(pending, event{k, name, detail})
		arm()
	}

	cancel := src.OnChange(onChange)
	return func() {
		cancel()
		mu.Lock()
		if timer != nil {
			timer.Stop()
		}
		mu.Unlock()
	}
}
