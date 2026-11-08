package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"example.com/shop/internal/catalog"
	commonent "github.com/huynhanx03/go-common/pkg/database/ent"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
)

func TestGeneratedAppShutdownDrainsRequests(t *testing.T) {
	database, err := url.Parse(os.Getenv("COMXKIT_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	password, _ := database.User.Password()
	port, err := strconv.Atoi(database.Port())
	if err != nil {
		t.Fatal(err)
	}
	addressListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := addressListener.Addr().String()
	if err := addressListener.Close(); err != nil {
		t.Fatal(err)
	}

	application, err := New(context.Background(), Config{
		Address: address,
		Logger:  logger.LoggerConfig{Mode: settings.EnvDev, Env: settings.EnvDev, Service: "integration"},
		Database: settings.Database{
			Driver: commonent.DriverPostgres, Host: database.Hostname(), Port: port,
			Username: database.User.Username(), Password: password, Database: strings.TrimPrefix(database.Path, "/"),
			MaxOpenConns: 5, MaxIdleConns: 2,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	closeCount := new(atomic.Int32)
	closeDependencies := application.closeFunc
	application.closeFunc = func() error {
		closeCount.Add(1)
		return closeDependencies()
	}

	runContext, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	runDone := make(chan error, 1)
	go func() { runDone <- application.Run(runContext) }()
	waitForAppLive(t, address)

	requestDone := make(chan error, 1)
	go func() {
		request, err := http.NewRequest(http.MethodGet, "http://"+address+"/api/v1/catalog/_test/drain", nil)
		if err != nil {
			requestDone <- err
			return
		}
		client := &http.Client{
			Transport: &http.Transport{DisableKeepAlives: true},
			Timeout:   5 * time.Second,
		}
		response, err := client.Do(request)
		if err != nil {
			requestDone <- err
			return
		}
		status := response.StatusCode
		closeErr := response.Body.Close()
		if status != http.StatusNoContent {
			requestDone <- errors.New("drain route returned an unexpected status")
			return
		}
		if closeErr != nil {
			requestDone <- closeErr
			return
		}
		requestDone <- nil
	}()

	select {
	case <-catalog.DrainEntered():
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight handler did not start")
	}

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownDone <- application.Shutdown(ctx)
	}()
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before the in-flight request drained: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if got := closeCount.Load(); got != 0 {
		t.Fatalf("dependencies closed while a request was in flight: %d closes", got)
	}

	catalog.ReleaseDrain()
	if err := <-requestDone; err != nil {
		t.Fatal(err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatal(err)
	}
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if err := application.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := closeCount.Load(); got != 1 {
		t.Fatalf("dependency close count = %d, want 1", got)
	}
}

func waitForAppLive(t *testing.T, address string) {
	t.Helper()
	client := &http.Client{Timeout: 250 * time.Millisecond}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	var lastFailure string

	for {
		response, err := client.Get("http://" + address + "/live")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
			lastFailure = "GET /live returned " + strconv.Itoa(response.StatusCode)
		} else {
			lastFailure = err.Error()
		}

		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatalf("HTTP app did not become live: %s", lastFailure)
		}
	}
}
