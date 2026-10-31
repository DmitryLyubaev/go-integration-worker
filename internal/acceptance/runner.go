package acceptance

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/DmitryLyubaev/go-integration-worker/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func Run(parent context.Context, cfg Config) (result Result, err error) {
	if cfg.Output == "" || cfg.SourceCommit == "" || cfg.SourceManifest == "" || cfg.SourceArchive == "" || cfg.Binary == "" || cfg.Fixture == "" {
		return result, errors.New("explicit source, binary, fixture and output bindings are required")
	}
	if cfg.SourceRoot == "" {
		cfg.SourceRoot = "."
	}
	root, e := filepath.Abs(cfg.Output)
	if e != nil {
		return result, e
	}
	cfg.Output = root
	if e = os.Mkdir(root, 0700); e != nil {
		return result, e
	}
	ctx, cancel := context.WithTimeout(parent, 290*time.Second)
	defer cancel()
	s := &runner{cfg: cfg, ctx: ctx, result: Result{Root: root, SourceCommit: cfg.SourceCommit, Started: time.Now().UTC()}, events: map[string][]byte{}}
	defer func() {
		clean := true
		for _, child := range s.children {
			if e := child.stop("TERM"); e != nil {
				clean = false
				err = errors.Join(err, e)
			}
			s.result.Processes = append(s.result.Processes, child.info)
		}
		if s.db != nil {
			s.db.Close()
		}
		if connection, e := net.DialTimeout("tcp", "127.0.0.1:8127", 100*time.Millisecond); e == nil {
			connection.Close()
			clean = false
			err = errors.Join(err, errors.New("owned receiver listener remains"))
		}
		s.result.Cleaned = clean
		if e := s.verifySource(); e != nil {
			err = errors.Join(err, e)
		}
		s.result.Finished = time.Now().UTC()
		if err != nil {
			s.result.Failure = err.Error()
		}
		s.result.Inventory, e = inventory(root)
		if e != nil {
			err = errors.Join(err, e)
			s.result.Failure = err.Error()
		}
		result = s.result
	}()
	if connection, e := net.DialTimeout("tcp", "127.0.0.1:8127", 100*time.Millisecond); e == nil {
		connection.Close()
		return result, errors.New("selected receiver port is occupied; no other process was changed")
	}
	for path, source := range map[string]string{"p27": cfg.Binary, "fixture.json": cfg.Fixture, "source-manifest.json": cfg.SourceManifest, "source.tar": cfg.SourceArchive, "compose.yaml": filepath.Join(cfg.SourceRoot, "compose.yaml")} {
		raw, e := os.ReadFile(source)
		if e != nil {
			return result, e
		}
		mode := os.FileMode(0600)
		if path == "p27" {
			mode = 0700
		}
		if e = os.WriteFile(filepath.Join(root, path), raw, mode); e != nil {
			return result, e
		}
		switch path {
		case "p27":
			s.result.BinaryHash = hashBytes(raw)
		case "fixture.json":
			s.result.FixtureHash = hashBytes(raw)
		case "source-manifest.json":
			s.result.SourceTreeHash = hashBytes(raw)
		case "compose.yaml":
			s.result.ConfigHash = hashBytes(raw)
		}
	}
	s.cfg.Binary = filepath.Join(root, "p27")
	if e = s.verifySource(); e != nil {
		return result, e
	}
	var fixture struct {
		Events []domain.Event `json:"events"`
	}
	if e = readJSON(root, "fixture.json", &fixture); e != nil || len(fixture.Events) != 12 {
		return result, errors.New("expected twelve frozen events")
	}
	for _, event := range fixture.Events {
		raw, _ := json.Marshal(event)
		p, e := domain.Decode(bytesReader(raw))
		if e != nil {
			return result, e
		}
		s.events[event.EventID] = p.Bytes
	}
	dsn := os.Getenv("P27_TEST_DSN")
	if dsn == "" {
		return result, errors.New("explicit local acceptance admin DSN is required")
	}
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		return result, errors.New("invalid local database configuration")
	}
	defer admin.Close()
	token := make([]byte, 6)
	rand.Read(token)
	name := "p27_run_" + hex.EncodeToString(token)
	if _, e = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); e != nil {
		return result, e
	}
	s.result.Database = name
	config, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		return result, e
	}
	config.ConnConfig.Database = name
	s.db, e = pgxpool.NewWithConfig(ctx, config)
	if e != nil {
		return result, e
	}
	if _, e = s.db.Exec(ctx, migrations.SQL); e != nil {
		return result, e
	}
	uri, e := url.Parse(dsn)
	if e != nil || uri.Scheme != "postgres" {
		return result, errors.New("local DSN must be a PostgreSQL URI")
	}
	uri.Path = "/" + name
	uri.RawPath = ""
	uri.User = url.UserPassword("p27_worker", "p27-local-worker")
	s.workerDSN = uri.String()
	uri.User = url.UserPassword("p27_receiver", "p27-local-receiver")
	s.receiverDSN = uri.String()
	for _, name := range []string{"A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8"} {
		if e = s.scenario(name); e != nil {
			return result, fmt.Errorf("%s: %w", name, e)
		}
	}
	return result, nil
}
func bytesReader(raw []byte) *bytes.Reader { return bytes.NewReader(raw) }
func (s *runner) verifySource() error {
	var manifest struct {
		SourceCommit string
		Files        []FileEntry
	}
	raw, e := os.ReadFile(s.cfg.SourceManifest)
	if e != nil {
		return e
	}
	if e = json.Unmarshal(raw, &manifest); e != nil || manifest.SourceCommit != s.cfg.SourceCommit || len(manifest.Files) == 0 {
		return errors.New("source manifest is incomplete")
	}
	for _, entry := range manifest.Files {
		full, e := safePath(s.cfg.SourceRoot, entry.Path)
		if e != nil {
			return e
		}
		raw, e := os.ReadFile(full)
		if e != nil || hashBytes(raw) != entry.SHA256 {
			return fmt.Errorf("source changed during sample: %s", entry.Path)
		}
	}
	return nil
}
func inventory(root string) ([]FileEntry, error) {
	var entries []FileEntry
	e := filepath.WalkDir(root, func(path string, item os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if item.IsDir() {
			return nil
		}
		relative, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		entries = append(entries, FileEntry{relative, hashBytes(raw)})
		return nil
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, e
}
