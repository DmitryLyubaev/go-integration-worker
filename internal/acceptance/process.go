package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type runner struct {
	cfg                    Config
	ctx                    context.Context
	result                 Result
	children               []*child
	workerDSN, receiverDSN string
	db                     *pgxpool.Pool
	events                 map[string][]byte
}
type child struct {
	cmd       *exec.Cmd
	done      chan struct{}
	err       error
	info      ProcessResult
	log       string
	requested bool
}

func baseEnv() []string {
	var values []string
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "P27_") {
			values = append(values, value)
		}
	}
	return values
}
func (s *runner) environment(role, tracePath string, extra []string) []string {
	env := baseEnv()
	if role == "receiver" {
		env = append(env, "P27_RECEIVER_DSN="+s.receiverDSN)
	} else {
		env = append(env, "P27_WORKER_DSN="+s.workerDSN)
	}
	env = append(env, "P27_TRACE_FILE="+tracePath, "P27_RECEIVER_URL=http://127.0.0.1:8127", "P27_LISTEN=127.0.0.1:8127")
	return append(env, extra...)
}
func (s *runner) start(role, group, name string, extra []string, args ...string) (*child, error) {
	prefix := group + "-" + name
	logPath := prefix + ".log.jsonl"
	tracePath := prefix + ".trace.jsonl"
	stderr, e := os.OpenFile(filepath.Join(s.cfg.Output, logPath), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	stdout, e := os.OpenFile(filepath.Join(s.cfg.Output, prefix+".stdout.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		stderr.Close()
		return nil, e
	}
	cmd := exec.Command(s.cfg.Binary, args...)
	cmd.Env = s.environment(role, filepath.Join(s.cfg.Output, tracePath), extra)
	cmd.Stderr = stderr
	cmd.Stdout = stdout
	c := &child{cmd: cmd, done: make(chan struct{}), log: logPath, info: ProcessResult{Case: group, Role: role, Started: time.Now().UTC()}}
	if e = cmd.Start(); e != nil {
		stdout.Close()
		stderr.Close()
		return nil, e
	}
	c.info.PID = cmd.Process.Pid
	s.children = append(s.children, c)
	s.result.TraceFiles = append(s.result.TraceFiles, tracePath)
	go func() {
		c.err = cmd.Wait()
		c.info.Stopped = time.Now().UTC()
		c.info.ExitCode = cmd.ProcessState.ExitCode()
		stdout.Close()
		stderr.Close()
		close(c.done)
	}()
	return c, nil
}
func (c *child) stop(kind string) error {
	select {
	case <-c.done:
		if c.requested {
			return nil
		}
		if c.info.ExitCode != 0 {
			return errors.New("owned process exited unexpectedly")
		}
		return nil
	default:
	}
	c.requested = true
	c.info.Signal = kind
	start := time.Now()
	signal := syscall.SIGTERM
	if kind == "KILL" {
		signal = syscall.SIGKILL
	}
	if e := c.cmd.Process.Signal(signal); e != nil && !errors.Is(e, os.ErrProcessDone) {
		return e
	}
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		c.cmd.Process.Kill()
		<-c.done
		c.info.ShutdownSeconds = time.Since(start).Seconds()
		return errors.New("owned process exceeded shutdown bound")
	}
	c.info.ShutdownSeconds = time.Since(start).Seconds()
	if kind == "KILL" {
		if c.info.ExitCode != -1 {
			return errors.New("kill did not terminate process by signal")
		}
		return nil
	}
	if c.info.ExitCode != 0 {
		return fmt.Errorf("owned process exited %d", c.info.ExitCode)
	}
	return nil
}
func (s *runner) command(name string, args ...string) ([]byte, error) {
	ctx, c := context.WithTimeout(s.ctx, 5*time.Second)
	defer c()
	tracePath := name + ".trace.jsonl"
	cmd := exec.CommandContext(ctx, s.cfg.Binary, args...)
	cmd.Env = s.environment("worker", filepath.Join(s.cfg.Output, tracePath), nil)
	stderr, e := os.OpenFile(filepath.Join(s.cfg.Output, name+".stderr.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	defer stderr.Close()
	cmd.Stderr = stderr
	raw, e := cmd.Output()
	if writeErr := os.WriteFile(filepath.Join(s.cfg.Output, name+".stdout.json"), raw, 0600); writeErr != nil {
		return nil, writeErr
	}
	status := 0
	if e != nil {
		status = -1
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			status = exit.ExitCode()
		}
	}
	receipt, _ := json.Marshal(map[string]any{"args": args, "exit_code": status, "at": time.Now().UTC()})
	os.WriteFile(filepath.Join(s.cfg.Output, name+".command.json"), receipt, 0600)
	s.result.TraceFiles = append(s.result.TraceFiles, tracePath)
	return raw, e
}
