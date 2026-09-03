package boot

import (
	"fmt"
	"strings"
)

// orderComponents performs a stable topological sort. Registration order is
// used as the tie breaker, preserving existing startup behaviour when there
// are no dependencies.
func orderComponents(components []Component) ([]Component, error) {
	byName := make(map[string]Component, len(components))
	for _, component := range components {
		byName[component.Name()] = component
	}
	state := make(map[string]uint8, len(components))
	ordered := make([]Component, 0, len(components))
	var visit func(Component, []string) error
	visit = func(component Component, path []string) error {
		name := component.Name()
		switch state[name] {
		case 1:
			return fmt.Errorf("boot: component dependency cycle: %s", strings.Join(append(path, name), " -> "))
		case 2:
			return nil
		}
		state[name] = 1
		if aware, ok := component.(DependencyAware); ok {
			for _, dependencyName := range aware.DependsOn() {
				dependencyName = strings.TrimSpace(dependencyName)
				if dependencyName == "" {
					continue
				}
				dependency, exists := byName[dependencyName]
				if !exists {
					return fmt.Errorf("boot: component %q depends on unknown component %q", name, dependencyName)
				}
				if err := visit(dependency, append(path, name)); err != nil {
					return err
				}
			}
		}
		state[name] = 2
		ordered = append(ordered, component)
		return nil
	}
	for _, component := range components {
		if err := visit(component, nil); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}
