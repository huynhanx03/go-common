//go:build integration

package containers

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const redisClusterNodeCount = 3

// StartRedisCluster starts a three-master Redis Cluster for integration tests.
// The returned client and all containers are closed when t finishes.
func StartRedisCluster(t testing.TB) *redis.ClusterClient {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	configureDockerHost(t, ctx)

	networkName := fmt.Sprintf("go-common-redis-cluster-%d", time.Now().UnixNano())
	dockerNetwork, err := testcontainers.GenericNetwork(ctx, testcontainers.GenericNetworkRequest{
		NetworkRequest: testcontainers.NetworkRequest{Name: networkName, Driver: "bridge"},
	})
	if err != nil {
		t.Fatalf("create Redis Cluster network: %v", err)
	}
	t.Cleanup(func() {
		if err := dockerNetwork.Remove(context.Background()); err != nil {
			t.Errorf("remove Redis Cluster network: %v", err)
		}
	})

	containers := make([]testcontainers.Container, redisClusterNodeCount)
	addresses := make([]string, redisClusterNodeCount)
	internalAddresses := make(map[string]string, redisClusterNodeCount)
	for i := range redisClusterNodeCount {
		alias := fmt.Sprintf("redis-node-%d", i)
		container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:          RedisImage,
				ExposedPorts:   []string{"6379/tcp"},
				Networks:       []string{networkName},
				NetworkAliases: map[string][]string{networkName: {alias}},
				Cmd: []string{
					"redis-server", "--bind", "0.0.0.0", "--protected-mode", "no",
					"--port", "6379", "--cluster-enabled", "yes",
					"--cluster-config-file", "nodes.conf", "--cluster-node-timeout", "5000",
					"--appendonly", "no",
				},
				WaitingFor: wait.ForLog("Ready to accept connections").WithStartupTimeout(90 * time.Second),
			},
			Started: true,
		})
		if err != nil {
			t.Fatalf("start Redis Cluster node %d: %v", i, err)
		}
		containers[i] = container
		t.Cleanup(func() {
			if err := container.Terminate(context.Background()); err != nil {
				t.Errorf("terminate Redis Cluster node %d: %v", i, err)
			}
		})

		addresses[i], err = container.Endpoint(ctx, "")
		if err != nil {
			t.Fatalf("get Redis Cluster node %d endpoint: %v", i, err)
		}
		inspect, err := container.Inspect(ctx)
		if err != nil {
			t.Fatalf("inspect Redis Cluster node %d: %v", i, err)
		}
		internalIP := inspect.NetworkSettings.Networks[networkName].IPAddress
		_, port, err := net.SplitHostPort(addresses[i])
		if err != nil {
			t.Fatalf("parse Redis Cluster endpoint %q: %v", addresses[i], err)
		}
		internalAddresses[net.JoinHostPort(internalIP.String(), "6379")] = net.JoinHostPort(hostname(addresses[i]), port)
	}

	createCommand := []string{"redis-cli", "--cluster", "create"}
	for i := range redisClusterNodeCount {
		createCommand = append(createCommand, fmt.Sprintf("redis-node-%d:6379", i))
	}
	createCommand = append(createCommand, "--cluster-replicas", "0", "--cluster-yes")
	exitCode, output, err := containers[0].Exec(ctx, createCommand)
	if err != nil {
		t.Fatalf("create Redis Cluster: %v", err)
	}
	clusterOutput, err := io.ReadAll(output)
	if err != nil {
		t.Fatalf("read Redis Cluster creation output: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("create Redis Cluster exited with %d: %s", exitCode, strings.TrimSpace(string(clusterOutput)))
	}

	bootstrap := redis.NewClient(&redis.Options{Addr: addresses[0]})
	t.Cleanup(func() { _ = bootstrap.Close() })
	if err := waitForRedisCluster(ctx, addresses); err != nil {
		t.Fatalf("wait for Redis Cluster readiness: %v", err)
	}
	slots, err := bootstrap.ClusterSlots(ctx).Result()
	if err != nil {
		t.Fatalf("read Redis Cluster slots: %v", err)
	}
	for slotIndex := range slots {
		for nodeIndex := range slots[slotIndex].Nodes {
			originalAddress := slots[slotIndex].Nodes[nodeIndex].Addr
			mappedAddress, ok := internalAddresses[originalAddress]
			if !ok {
				t.Fatalf("Redis Cluster advertised unmapped node address %q", originalAddress)
			}
			slots[slotIndex].Nodes[nodeIndex].Addr = mappedAddress
		}
	}

	clusterClient := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs: addresses,
		ClusterSlots: func(context.Context) ([]redis.ClusterSlot, error) {
			mappedSlots := make([]redis.ClusterSlot, len(slots))
			for i, slot := range slots {
				mappedSlots[i] = slot
				mappedSlots[i].Nodes = append([]redis.ClusterNode(nil), slot.Nodes...)
			}
			return mappedSlots, nil
		},
	})
	if err := clusterClient.Ping(ctx).Err(); err != nil {
		_ = clusterClient.Close()
		t.Fatalf("connect to Redis Cluster: %v", err)
	}
	t.Cleanup(func() {
		if err := clusterClient.Close(); err != nil {
			t.Errorf("close Redis Cluster client: %v", err)
		}
	})
	return clusterClient
}

func waitForRedisCluster(ctx context.Context, addresses []string) error {
	clients := make([]*redis.Client, len(addresses))
	for i, address := range addresses {
		clients[i] = redis.NewClient(&redis.Options{Addr: address})
		defer clients[i].Close()
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		allReady := true
		var lastInfo string
		var lastErr error
		for _, client := range clients {
			info, err := client.ClusterInfo(ctx).Result()
			lastInfo, lastErr = info, err
			if err != nil || !strings.Contains(info, "cluster_state:ok") || !strings.Contains(info, "cluster_size:3") {
				allReady = false
			}
		}
		if allReady {
			return nil
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("cluster did not become ready: %w", lastErr)
			}
			return fmt.Errorf("cluster did not become ready before deadline; last info: %s", strings.TrimSpace(lastInfo))
		case <-ticker.C:
		}
	}
}

func hostname(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	return host
}
