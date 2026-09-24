package app

import (
	"net"
	"path/filepath"
	"strconv"

	"github.com/Bethel-nz/pier/internal/config"
	"github.com/Bethel-nz/pier/internal/project"
	"github.com/Bethel-nz/pier/internal/runner"
)

// RunPlan is what pier up starts for a project with run: commands.
type RunPlan struct {
	Project   project.Context
	Processes []runner.Process
	// Targets are the addresses pier up waits on before applying routes.
	Targets map[string]string
}

// RunPlan lists the project's run: commands. It is empty when Pier runs nothing.
func (s *Service) RunPlan(start string) (RunPlan, error) {
	loaded, err := s.loadProject(start, true)
	if err != nil {
		return RunPlan{}, err
	}
	plan := RunPlan{Project: loaded.project, Targets: map[string]string{}}
	for _, service := range loaded.cfg.Services {
		if service.Run.Command == "" {
			continue
		}
		plan.Processes = append(plan.Processes, process(loaded.project.Root, service))
		plan.Targets[service.Name] = net.JoinHostPort(service.Host, strconv.Itoa(int(service.Port)))
	}
	return plan, nil
}

// process sets PORT to the target's port, since most dev servers read it,
// unless env sets it.
func process(root string, service config.ResolvedService) runner.Process {
	env := map[string]string{"PORT": strconv.Itoa(int(service.Port))}
	for key, value := range service.Run.Env {
		env[key] = value
	}
	return runner.Process{
		Name:    service.Name,
		Command: service.Run.Command,
		Dir:     filepath.Join(root, filepath.FromSlash(service.Run.Dir)),
		Env:     env,
		Watch:   service.Run.Watch,
	}
}
