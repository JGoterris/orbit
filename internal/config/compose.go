package config

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var ComposeFilenames = []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"}

// FindComposeFile is the compose file of a directory, if it has one.
func FindComposeFile(dir string) string {
	for _, n := range ComposeFilenames {
		if p := filepath.Join(dir, n); exists(p) {
			return p
		}
	}
	return ""
}

var projectCleanRe = regexp.MustCompile(`[^a-z0-9_-]`)

func defaultComposeProject(file string) string {
	return projectCleanRe.ReplaceAllString(strings.ToLower(filepath.Base(filepath.Dir(file))), "")
}

// ParseComposeFile turns the services of a compose file into orbit services of type compose. `project` is the
// compose project name (default: the file's `name:`, else the folder's name); env feeds `${VAR}` (default: the
// process environment).
func ParseComposeFile(file, text, project string, env map[string]string) (svcs []*ServiceConfig, err error) {
	defer recoverConfig(&err)
	if env == nil {
		env = environ()
	}
	vars := map[string]string{}
	if data, rerr := os.ReadFile(filepath.Join(filepath.Dir(file), ".env")); rerr == nil {
		for k, v := range ParseDotEnv(string(data)) {
			vars[k] = v
		}
	}
	for k, v := range env {
		vars[k] = v
	}
	parsed, perr := ParseYAML(text)
	if perr != nil {
		fail("invalid YAML: "+perr.Error(), file)
	}
	doc, _ := Interpolate(asRecord(parsed, file), vars).(*Map)
	services := asRecord(doc.Get("services"), file+": services")
	composeProject := project
	if composeProject == "" {
		if name, ok := doc.Get("name").(string); ok {
			composeProject = name
		} else {
			composeProject = defaultComposeProject(file)
		}
	}

	for _, name := range services.Keys() {
		raw := services.Get(name)
		path := fmt.Sprintf("%s: services.%s", file, name)
		svc := asRecord(raw, path)

		// depends_on may be a list or a mapping { db: { condition: service_healthy } }
		var dependsOn []string
		if list, ok := svc.Get("depends_on").([]any); ok {
			dependsOn = asStringList(list, path+".depends_on")
		} else {
			dependsOn = append(dependsOn, asRecord(svc.Get("depends_on"), path+".depends_on").Keys()...)
		}

		var portsRaw any = svc.Get("ports")
		if list, ok := portsRaw.([]any); ok {
			mapped := make([]any, len(list))
			for i, p := range list {
				if pm, ok := p.(*Map); ok {
					target := jsString(pm.Get("target"))
					published := pm.Get("published")
					if published == nil {
						published = pm.Get("target")
					}
					mapped[i] = jsString(published) + ":" + target
				} else {
					mapped[i] = p
				}
			}
			portsRaw = mapped
		}
		ports := asStringList(portsRaw, path+".ports")
		port := 0
		for _, p := range ports {
			if hp := hostPortOf(p); hp != 0 {
				port = hp
				break
			}
		}

		deploy := asRecord(svc.Get("deploy"), path+".deploy")
		resources := asRecord(deploy.Get("resources"), path+".deploy.resources")
		limits := asRecord(resources.Get("limits"), path+".deploy.resources.limits")
		memRaw := svc.First("mem_limit")
		if memRaw == nil {
			memRaw = limits.Get("memory")
		}
		memLimit := asMemSize(memRaw, path+".mem_limit")

		hasHealthcheck := false
		if svc.Has("healthcheck") {
			hasHealthcheck = asRecord(svc.Get("healthcheck"), path).Get("disable") != true
		}

		image, _ := svc.Get("image").(string)
		description := image
		if description == "" && truthy(svc.Get("build")) {
			description = "build"
		}

		var health *HealthCheck
		switch {
		case hasHealthcheck:
			health = &HealthCheck{Container: true, Interval: 2000, Timeout: 5000}
		case port != 0:
			health = &HealthCheck{Tcp: strconv.Itoa(port), Interval: 1000, Timeout: 1000}
		}

		h := fnv.New64a()
		h.Write([]byte(jsonOf(raw)))

		svcs = append(svcs, &ServiceConfig{
			Name:           name,
			Type:           TypeCompose,
			Description:    description,
			Cwd:            filepath.Dir(file),
			Env:            asEnv(svc.Get("environment"), path+".environment"),
			EnvFiles:       []EnvFileRef{},
			DependsOn:      orEmpty(dependsOn),
			Port:           port,
			Health:         health,
			Restart:        RestartNo,
			StartTimeout:   120_000,
			StopTimeout:    10_000,
			Autostart:      true,
			MemLimit:       memLimit,
			LeakDetection:  true,
			Image:          image,
			Ports:          orEmpty(ports),
			Volumes:        []string{},
			DockerArgs:     []string{},
			ComposeFile:    file,
			ComposeProject: composeProject,
			ComposeService: name,
			ComposeHash:    strconv.FormatUint(h.Sum64(), 10),
		})
	}
	return svcs, nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
