//go:build integration

package containers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// EtcdCluster controls an isolated one- or three-member etcd cluster.
type EtcdCluster struct {
	containers []testcontainers.Container
	endpoints  []string
}

// StartEtcd starts an isolated etcd cluster and stops it when the test ends.
func StartEtcd(t testing.TB, members int) *EtcdCluster {
	t.Helper()
	if members != 1 && members != 3 {
		t.Fatalf("etcd cluster member count %d is unsupported", members)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	configureDockerHost(t, ctx)
	networkName := fmt.Sprintf("go-common-etcd-%d", time.Now().UnixNano())
	network, err := testcontainers.GenericNetwork(ctx, testcontainers.GenericNetworkRequest{
		NetworkRequest: testcontainers.NetworkRequest{Name: networkName, Driver: "bridge"},
	})
	if err != nil {
		t.Fatalf("create etcd network: %v", err)
	}
	t.Cleanup(func() {
		if err := network.Remove(context.Background()); err != nil {
			t.Errorf("remove etcd network: %v", err)
		}
	})

	cluster := &EtcdCluster{
		containers: make([]testcontainers.Container, members),
		endpoints:  make([]string, members),
	}
	initialCluster := make([]string, members)
	for i := range members {
		initialCluster[i] = fmt.Sprintf("node%d=http://node%d:%s", i, i, "2380")
	}
	clusterValue := strings.Join(initialCluster, ",")
	for i := range members {
		name := fmt.Sprintf("node%d", i)
		container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:          EtcdImage,
				ExposedPorts:   []string{"2379/tcp"},
				Networks:       []string{networkName},
				NetworkAliases: map[string][]string{networkName: {name}},
				Cmd: []string{
					"/usr/local/bin/etcd",
					"--name=" + name,
					"--data-dir=/etcd-data",
					"--listen-peer-urls=http://0.0.0.0:2380",
					"--initial-advertise-peer-urls=http://" + name + ":2380",
					"--listen-client-urls=http://0.0.0.0:2379",
					"--advertise-client-urls=http://" + name + ":2379",
					"--initial-cluster=" + clusterValue,
					"--initial-cluster-state=new",
					"--initial-cluster-token=" + networkName,
				},
				WaitingFor: wait.ForListeningPort("2379/tcp").WithStartupTimeout(2 * time.Minute),
			},
			Started: true,
		})
		if err != nil {
			t.Fatalf("start etcd member %d: %v", i, err)
		}
		cluster.containers[i] = container
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanupCancel()
			if err := container.Terminate(cleanupCtx); err != nil {
				t.Errorf("terminate etcd member %d: %v", i, err)
			}
		})
		cluster.endpoints[i], err = container.Endpoint(ctx, "")
		if err != nil {
			t.Fatalf("get etcd member %d endpoint: %v", i, err)
		}
	}
	if err := waitForEtcdCluster(ctx, cluster.endpoints); err != nil {
		t.Fatalf("wait for etcd cluster readiness: %v", err)
	}
	return cluster
}

// Endpoints returns the client endpoints in member order.
func (c *EtcdCluster) Endpoints() []string {
	return append([]string(nil), c.endpoints...)
}

// StopMember stops one cluster member without removing its persistent data.
func (c *EtcdCluster) StopMember(ctx context.Context, member int) error {
	if member < 0 || member >= len(c.containers) {
		return fmt.Errorf("etcd member index %d is out of range", member)
	}
	stopTimeout := 10 * time.Second
	return c.containers[member].Stop(ctx, &stopTimeout)
}

// StartMember restarts one previously stopped cluster member.
func (c *EtcdCluster) StartMember(ctx context.Context, member int) error {
	if member < 0 || member >= len(c.containers) {
		return fmt.Errorf("etcd member index %d is out of range", member)
	}
	return c.containers[member].Start(ctx)
}

func waitForEtcdCluster(ctx context.Context, endpoints []string) error {
	client := &http.Client{Timeout: time.Second}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready := true
		for _, endpoint := range endpoints {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+endpoint+"/health", nil)
			if err != nil {
				return fmt.Errorf("create health request: %w", err)
			}
			response, err := client.Do(request)
			if err != nil {
				ready = false
				break
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				ready = false
				break
			}
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("cluster did not become ready: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
