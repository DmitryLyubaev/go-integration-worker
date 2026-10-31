package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/DmitryLyubaev/go-integration-worker/internal/acceptance"
	"os"
	"path/filepath"
)

func main() {
	var cfg acceptance.Config
	var verify string
	flag.StringVar(&cfg.Binary, "binary", "", "compiled worker binary")
	flag.StringVar(&cfg.Fixture, "fixture", "", "frozen fixture")
	flag.StringVar(&cfg.Output, "out", "", "new private output directory")
	flag.StringVar(&cfg.SourceCommit, "source-commit", "", "exact source commit")
	flag.StringVar(&cfg.SourceManifest, "source-manifest", "", "source file hashes")
	flag.StringVar(&cfg.SourceArchive, "source-archive", "", "source archive")
	flag.StringVar(&cfg.SourceRoot, "source-root", ".", "source working tree")
	flag.StringVar(&verify, "verify", "", "read-only result verification")
	flag.Parse()
	if verify != "" {
		raw, e := os.ReadFile(verify)
		var result acceptance.Result
		if e == nil {
			e = json.Unmarshal(raw, &result)
		}
		result.Root = filepath.Dir(verify)
		if e == nil {
			e = acceptance.Verify(result)
		}
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		fmt.Println("retained A1-A8 evidence verified read-only")
		return
	}
	result, e := acceptance.Run(context.Background(), cfg)
	if e == nil {
		e = acceptance.Verify(result)
	}
	if e != nil {
		result.Failure = e.Error()
	}
	raw, _ := json.MarshalIndent(result, "", "  ")
	if cfg.Output != "" {
		if writeErr := os.WriteFile(filepath.Join(cfg.Output, "result.json"), raw, 0600); writeErr != nil {
			fmt.Fprintln(os.Stderr, writeErr)
			os.Exit(1)
		}
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, "canonical sample failed:", e)
		os.Exit(1)
	}
	fmt.Printf("A1-A8 passed: 12 IDs, 20 reservations, 19 worker POSTs, 3 protocol POSTs, 1 receipt GET, 10 effects; %.3fs including process cleanup\n", result.Finished.Sub(result.Started).Seconds())
}
