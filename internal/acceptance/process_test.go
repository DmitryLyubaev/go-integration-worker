package acceptance

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestProcessHelper(t *testing.T) {
	if os.Getenv("P27_PROCESS_HELPER") != "1" {
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	os.WriteFile(os.Getenv("P27_PROCESS_READY"), []byte("ready"), 0600)
	<-signals
	os.Exit(0)
}
func TestOwnedProcessTermination(t *testing.T) {
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"TERM", "KILL"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			s := runner{cfg: Config{Binary: binary, Output: dir}, ctx: context.Background(), result: Result{Root: dir}}
			ready := filepath.Join(dir, "ready")
			c, e := s.start("worker", "software", kind, []string{"P27_PROCESS_HELPER=1", "P27_PROCESS_READY=" + ready}, "-test.run=^TestProcessHelper$")
			if e != nil {
				t.Fatal(e)
			}
			defer c.stop("KILL")
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, e = os.Stat(ready); e == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("helper did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if e = c.stop(kind); e != nil {
				t.Fatal(e)
			}
			if c.info.Stopped.IsZero() || c.info.ShutdownSeconds > 5 {
				t.Fatal("exit observation missing")
			}
			want := 0
			if kind == "KILL" {
				want = -1
			}
			if c.info.ExitCode != want || c.info.Signal != kind {
				t.Fatalf("exit=%+v", c.info)
			}
		})
	}
}
