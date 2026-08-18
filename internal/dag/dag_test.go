package dag

import (
	"testing"

	"github.com/forger/buildengine/internal/config"
)

func TestTopologicalOrder(t *testing.T) {
	spec := &config.BuildSpec{
		Steps: []config.Step{
			{Name: "install", Run: "echo install"},
			{Name: "test", Run: "echo test", Needs: []string{"install"}},
			{Name: "build", Run: "echo build", Needs: []string{"install", "test"}},
		},
	}
	g, err := Build(spec)
	if err != nil {
		t.Fatal(err)
	}
	pos := make(map[string]int)
	for i, name := range g.Order {
		pos[name] = i
	}
	if pos["install"] >= pos["test"] || pos["test"] >= pos["build"] {
		t.Fatalf("invalid order: %v", g.Order)
	}
}

func TestCycleDetection(t *testing.T) {
	spec := &config.BuildSpec{
		Steps: []config.Step{
			{Name: "a", Run: "echo a", Needs: []string{"b"}},
			{Name: "b", Run: "echo b", Needs: []string{"a"}},
		},
	}
	_, err := Build(spec)
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestSubgraph(t *testing.T) {
	spec := &config.BuildSpec{
		Steps: []config.Step{
			{Name: "install", Run: "echo install"},
			{Name: "test", Run: "echo test", Needs: []string{"install"}},
			{Name: "build", Run: "echo build", Needs: []string{"install", "test"}},
			{Name: "lint", Run: "echo lint"},
		},
	}
	g, err := Build(spec)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := g.Subgraph("build")
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(sub.Nodes))
	}
	if _, ok := sub.Nodes["lint"]; ok {
		t.Fatal("lint should not be in subgraph")
	}
}
