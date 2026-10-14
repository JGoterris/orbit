package config

// Describe is a one-line description of what the check does.
func (h *HealthCheck) Describe() string {
	switch {
	case h == nil:
		return "none"
	case h.Http != "":
		return "http " + h.Http
	case h.Tcp != "":
		return "tcp " + h.Tcp
	case h.Cmd != "":
		return "cmd " + h.Cmd
	case h.Container:
		return "docker healthcheck"
	}
	return "none"
}
