package dag

import (
	"fmt"
	"strings"

	"github.com/forger/buildengine/internal/config"
)

// JobStatus represents the lifecycle state of a job.
type JobStatus string

const (
	StatusPending   JobStatus = "PENDING"
	StatusReady     JobStatus = "READY"
	StatusRunning   JobStatus = "RUNNING"
	StatusSucceeded JobStatus = "SUCCEEDED"
	StatusCacheHit  JobStatus = "CACHE_HIT"
	StatusFailed    JobStatus = "FAILED"
	StatusSkipped   JobStatus = "SKIPPED"
)

// JobNode is a DAG node wrapping a step and runtime state.
type JobNode struct {
	Step         config.Step
	Dependencies []string
	Dependents   []string
	Status       JobStatus
}

// Graph is a dependency DAG over build steps.
type Graph struct {
	Nodes map[string]*JobNode
	Order []string
}

// Build constructs a DAG from a BuildSpec and validates acyclicity.
func Build(spec *config.BuildSpec) (*Graph, error) {
	g := &Graph{Nodes: make(map[string]*JobNode)}

	for _, step := range spec.Steps {
		g.Nodes[step.Name] = &JobNode{
			Step:         step,
			Dependencies: append([]string(nil), step.Needs...),
			Status:       StatusPending,
		}
	}

	for name, node := range g.Nodes {
		for _, dep := range node.Dependencies {
			depNode, ok := g.Nodes[dep]
			if !ok {
				return nil, fmt.Errorf("dag: step '%s' depends on undefined '%s'", name, dep)
			}
			depNode.Dependents = append(depNode.Dependents, name)
		}
	}

	if cycle := detectCycle(g); cycle != nil {
		return nil, fmt.Errorf("dag: dependency cycle detected: %s", strings.Join(cycle, " -> "))
	}

	order, err := topologicalSort(g)
	if err != nil {
		return nil, err
	}
	g.Order = order
	return g, nil
}

// Subgraph returns the transitive closure of dependencies for target job.
func (g *Graph) Subgraph(target string) (*Graph, error) {
	if _, ok := g.Nodes[target]; !ok {
		return nil, fmt.Errorf("dag: unknown job '%s'", target)
	}

	include := make(map[string]bool)
	var visit func(string)
	visit = func(name string) {
		if include[name] {
			return
		}
		include[name] = true
		for _, dep := range g.Nodes[name].Dependencies {
			visit(dep)
		}
	}
	visit(target)

	steps := make([]config.Step, 0, len(include))
	for _, name := range g.Order {
		if include[name] {
			steps = append(steps, g.Nodes[name].Step)
		}
	}
	return Build(&config.BuildSpec{Steps: steps})
}

func detectCycle(g *Graph) []string {
	state := make(map[string]int) // 0=unvisited, 1=visiting, 2=done
	var path []string

	var dfs func(string) []string
	dfs = func(name string) []string {
		switch state[name] {
		case 1:
			for i, p := range path {
				if p == name {
					return append(path[i:], name)
				}
			}
			return []string{name, name}
		case 2:
			return nil
		}
		state[name] = 1
		path = append(path, name)
		for _, dep := range g.Nodes[name].Dependencies {
			if c := dfs(dep); c != nil {
				return c
			}
		}
		path = path[:len(path)-1]
		state[name] = 2
		return nil
	}

	for name := range g.Nodes {
		if state[name] == 0 {
			if c := dfs(name); c != nil {
				return c
			}
		}
	}
	return nil
}

func topologicalSort(g *Graph) ([]string, error) {
	inDegree := make(map[string]int, len(g.Nodes))
	for name := range g.Nodes {
		inDegree[name] = len(g.Nodes[name].Dependencies)
	}

	queue := make([]string, 0)
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}

	order := make([]string, 0, len(g.Nodes))
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		order = append(order, name)

		for _, dep := range g.Nodes[name].Dependents {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(order) != len(g.Nodes) {
		return nil, fmt.Errorf("dag: cycle detected during topological sort")
	}
	return order, nil
}

// ReadyJobs returns jobs whose dependencies have all completed successfully or hit cache.
func (g *Graph) ReadyJobs() []string {
	var ready []string
	for _, name := range g.Order {
		node := g.Nodes[name]
		if node.Status != StatusPending {
			continue
		}
		allDone := true
		for _, dep := range node.Dependencies {
			ds := g.Nodes[dep].Status
			if ds != StatusSucceeded && ds != StatusCacheHit {
				allDone = false
				break
			}
		}
		if allDone {
			ready = append(ready, name)
		}
	}
	return ready
}

// MarkDownstreamSkipped marks all transitive dependents of failed jobs as SKIPPED.
func (g *Graph) MarkDownstreamSkipped(failed string) {
	var skip func(string)
	skip = func(name string) {
		for _, dep := range g.Nodes[name].Dependents {
			node := g.Nodes[dep]
			if node.Status == StatusPending || node.Status == StatusReady {
				node.Status = StatusSkipped
				skip(dep)
			}
		}
	}
	skip(failed)
}

// HasPendingOrRunning returns true if any job is not in a terminal state.
func (g *Graph) HasPendingOrRunning() bool {
	for _, node := range g.Nodes {
		switch node.Status {
		case StatusPending, StatusReady, StatusRunning:
			return true
		}
	}
	return false
}

// AllTerminal returns true when every job reached a terminal state.
func (g *Graph) AllTerminal() bool {
	for _, node := range g.Nodes {
		switch node.Status {
		case StatusSucceeded, StatusCacheHit, StatusFailed, StatusSkipped:
		default:
			return false
		}
	}
	return true
}
