package receiver

import (
	"errors"
	"os"
	"sync"
	"time"
)

type Fault struct {
	Status, Failures, DropResponses int
	GatePath                        string
}
type FaultController struct {
	mu    sync.Mutex
	rules map[string]Fault
	seen  map[string]int
}

func NewFaultController(enabled bool, rules map[string]Fault) (*FaultController, error) {
	if !enabled && len(rules) > 0 {
		return nil, errors.New("faults require explicit local test mode")
	}
	for _, r := range rules {
		if r.Status != 0 && r.Status != 422 && r.Status != 503 {
			return nil, errors.New("unsupported local fault")
		}
	}
	return &FaultController{rules: rules, seen: map[string]int{}}, nil
}
func (f *FaultController) next(id string) (Fault, int) {
	if f == nil {
		return Fault{}, 1
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen[id]++
	return f.rules[id], f.seen[id]
}
func waitGate(path string) error {
	if err := os.WriteFile(path+".entered", []byte("entered\n"), 0600); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path + ".release"); err == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("test gate expired")
}
