package stack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Hy0sh/worktree-manager/internal/compose"
)

// baseOffset, the formula and the fallback are taken verbatim from
// worktree-compose. Keeping them identical is not a detail: worktrees created
// before this tool stopped shelling out to it must keep the exact same ports.
const baseOffset = 20000

// DefaultStride applies when the project does not configure one.
const DefaultStride = 1

// Allocation is one published port, rebased for a given worktree.
type Allocation struct {
	Service   string
	Var       string
	Port      int
	Container string
}

// AllocatePort rebases a default port for a worktree index.
func AllocatePort(defaultPort, index, stride int) (int, error) {
	port := baseOffset + defaultPort + index*stride
	if port > 65535 {
		port = defaultPort + 100*index
	}
	if port > 65535 || port < 1024 {
		return 0, fmt.Errorf("cannot allocate a port for default %d at worktree index %d: %d is out of range (1024-65535)",
			defaultPort, index, port)
	}
	return port, nil
}

// Allocate rebases every parameterised port of the compose file. Ports written
// as a literal are skipped: without a variable there is nothing to override,
// and they would collide with the main stack.
func Allocate(services []compose.ServicePort, index, stride int) ([]Allocation, error) {
	var out []Allocation
	seen := map[int]string{}
	for _, s := range services {
		if s.Var == "" {
			continue
		}
		defaultPort, err := strconv.Atoi(s.Host)
		if err != nil {
			continue
		}
		port, err := AllocatePort(defaultPort, index, stride)
		if err != nil {
			return nil, err
		}
		if other, clash := seen[port]; clash {
			return nil, fmt.Errorf("port collision in worktree %d: %d is assigned to both %s and %s",
				index, port, other, s.Var)
		}
		seen[port] = s.Var
		out = append(out, Allocation{Service: s.Service, Var: s.Var, Port: port, Container: s.Container})
	}
	return out, nil
}

// Stride reads the project's port stride, from .wtcrc.json when present so an
// existing setup keeps working untouched.
func Stride(projectDir string) int {
	var cfg struct {
		PortStride int `json:"portStride"`
	}
	data, err := os.ReadFile(filepath.Join(projectDir, ".wtcrc.json"))
	if err == nil && json.Unmarshal(data, &cfg) == nil && cfg.PortStride > 0 {
		return cfg.PortStride
	}
	var pkg struct {
		Wtc struct {
			PortStride int `json:"portStride"`
		} `json:"wtc"`
	}
	data, err = os.ReadFile(filepath.Join(projectDir, "package.json"))
	if err == nil && json.Unmarshal(data, &pkg) == nil && pkg.Wtc.PortStride > 0 {
		return pkg.Wtc.PortStride
	}
	return DefaultStride
}
