package cli

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/graph"
	"github.com/JGoterris/orbit/internal/ui/graphlayout"
)

// RunGraph prints the dependency graph (`orbit graph`).
func RunGraph(cfg *config.OrbitConfig) int {
	layout := graphlayout.LayoutGraph(cfg.DepMap(), cfg.Order)
	grid := graphlayout.Paint(layout, graphlayout.PaintOptions{
		Node: func(name string) graphlayout.NodeStyle {
			svc := cfg.Services[name]
			sub := string(svc.Type)
			if svc.Port != 0 {
				sub += " :" + strconv.Itoa(svc.Port)
			}
			return graphlayout.NodeStyle{Icon: "○", Title: name, Subtitle: sub}
		},
	})
	fmt.Println(graphlayout.GridString(grid))
	fmt.Println(dim("\n──▶ starts before"))
	return 0
}

// RunList prints the services as a table (`orbit ls`).
func RunList(cfg *config.OrbitConfig) int {
	lv := graph.Levels(cfg.DepMap())
	var rows [][]string
	for _, name := range cfg.Order {
		s := cfg.Services[name]
		port := ""
		if s.Port != 0 {
			port = ":" + strconv.Itoa(s.Port)
		}
		var runs string
		switch s.Type {
		case config.TypeExternal:
			runs = s.Health.Describe()
		case config.TypeProcess:
			runs = s.Cmd
		case config.TypeDocker:
			runs = s.Image
		default:
			rel, err := filepath.Rel(cfg.Root, s.ComposeFile)
			if err != nil {
				rel = s.ComposeFile
			}
			runs = rel + "#" + s.ComposeService
		}
		rows = append(rows, []string{s.Name, string(s.Type), port, strconv.Itoa(lv[s.Name]), strings.Join(s.DependsOn, ", "), runs})
	}
	header := []string{"SERVICE", "TYPE", "PORT", "LEVEL", "DEPENDS ON", "RUNS"}
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = len([]rune(h))
		for _, r := range rows {
			widths[i] = max(widths[i], len([]rune(r[i])))
		}
	}
	line := func(r []string) string {
		cells := make([]string, len(r))
		for i, v := range r {
			cells[i] = padEnd(v, widths[i])
		}
		return strings.Join(cells, "  ")
	}
	fmt.Println(bold(line(header)))
	for _, r := range rows {
		fmt.Println(line(r))
	}
	for _, g := range cfg.GroupOrder {
		fmt.Println(dim(fmt.Sprintf("group %s: %s", g, strings.Join(cfg.Groups[g], ", "))))
	}
	if len(cfg.TaskOrder) > 0 {
		fmt.Println(dim("tasks (orbit run): " + strings.Join(cfg.TaskOrder, ", ")))
	}
	return 0
}
