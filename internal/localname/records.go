package localname

import (
	"fmt"
	"sort"

	"pier/internal/state"
)

// Records lists the local names saved by every Pier project.
// The same name claimed twice is an error, so two projects cannot publish one address.
func Records(projects []state.ProjectState) ([]Record, error) {
	byName := map[string]Record{}
	owners := map[string]string{}
	for _, project := range projects {
		for _, domain := range project.Domains {
			if domain.Name == "" || domain.Port == 0 {
				continue
			}
			if owner, exists := owners[domain.Name]; exists && owner != project.ProjectID {
				return nil, fmt.Errorf("local domain %s is already used by another Pier project", domain.Name)
			}
			owners[domain.Name] = project.ProjectID
			byName[domain.Name] = Record{Name: domain.Name, Port: domain.Port}
		}
	}
	records := make([]Record, 0, len(byName))
	for _, record := range byName {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Name < records[j].Name })
	return records, nil
}
