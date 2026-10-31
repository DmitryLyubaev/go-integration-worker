package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"github.com/DmitryLyubaev/go-integration-worker/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	"os"
	"testing"
	"time"
)

type Database struct {
	Admin, Worker, Receiver *pgxpool.Pool
	WorkerDSN, ReceiverDSN  string
}

func PostgreSQL(t *testing.T) Database {
	t.Helper()
	dsn := os.Getenv("P27_TEST_DSN")
	if dsn == "" {
		t.Fatal("P27_TEST_DSN is required for real database tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	suffix := make([]byte, 8)
	if _, err = rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "p27_test_" + hex.EncodeToString(suffix)
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		if _, e := admin.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
		admin.Close()
	})
	if _, err = pool.Exec(ctx, migrations.SQL); err != nil {
		t.Fatal(err)
	}
	workerConfig := config.Copy()
	workerConfig.ConnConfig.User = "p27_worker"
	workerConfig.ConnConfig.Password = "p27-local-worker"
	wp, err := pgxpool.NewWithConfig(ctx, workerConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wp.Close)
	receiverConfig := config.Copy()
	receiverConfig.ConnConfig.User = "p27_receiver"
	receiverConfig.ConnConfig.Password = "p27-local-receiver"
	rp, err := pgxpool.NewWithConfig(ctx, receiverConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rp.Close)
	workerURL, e := url.Parse(dsn)
	if e != nil || workerURL.Scheme != "postgres" {
		t.Fatal("test DSN must be a PostgreSQL URI")
	}
	workerURL.Path = "/" + name
	workerURL.RawPath = ""
	workerURL.User = url.UserPassword("p27_worker", "p27-local-worker")
	receiverURL := *workerURL
	receiverURL.User = url.UserPassword("p27_receiver", "p27-local-receiver")
	return Database{pool, wp, rp, workerURL.String(), receiverURL.String()}
}
