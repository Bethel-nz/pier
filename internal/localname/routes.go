package localname

import (
	"path/filepath"
	"sort"

	"github.com/Bethel-nz/pier/internal/state"
)

// Route is one name the daemon serves.
type Route struct {
	Name      string
	Service   string
	Target    string
	ProjectID string
	CertDir   string
}

// Conflict is a name that two projects both declared. The first claim wins.
type Conflict struct {
	Name      string
	ProjectID string
	Winner    string
}

// Routes lists every saved name across projects. Projects are visited in ID
// order so the same claim always wins, and a duplicate never blocks the rest.
func Routes(projects []state.ProjectState) ([]Route, []Conflict) {
	sorted := append([]state.ProjectState(nil), projects...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ProjectID < sorted[j].ProjectID })
	owner := map[string]string{}
	var routes []Route
	var conflicts []Conflict
	for _, project := range sorted {
		for _, domain := range project.Domains {
			if domain.Name == "" || domain.Target == "" || project.Path == "" {
				continue
			}
			if winner, taken := owner[domain.Name]; taken {
				if winner != project.ProjectID {
					conflicts = append(conflicts, Conflict{Name: domain.Name, ProjectID: project.ProjectID, Winner: winner})
				}
				continue
			}
			owner[domain.Name] = project.ProjectID
			routes = append(routes, Route{
				Name:      domain.Name,
				Service:   domain.Service,
				Target:    domain.Target,
				ProjectID: project.ProjectID,
				CertDir:   filepath.Join(project.Path, ".pier", "certs"),
			})
		}
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Name < routes[j].Name })
	return routes, conflicts
}
