package kafka

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	integrationBrokers []string
)

func TestMain(m *testing.M) {
	// Check if Docker is available
	ctx := context.Background()
	if !isDockerRunning(ctx) {
		fmt.Println("Docker not running, skipping integration setup.")
		os.Exit(m.Run())
	}

	// Setup global Kafka container
	brokers, terminate, err := setupKafkaContainer(ctx)
	if err != nil {
		fmt.Printf("Failed to setup Kafka container: %v\n", err)
		os.Exit(1)
	}
	integrationBrokers = brokers

	// Run tests
	code := m.Run()

	// Teardown
	terminate()

	os.Exit(code)
}

func setupKafkaContainer(ctx context.Context) ([]string, func(), error) {
	req := testcontainers.ContainerRequest{
		Image: kafkaImage,
		Env: map[string]string{
			"KAFKA_NODE_ID":                                  "1",
			"KAFKA_PROCESS_ROLES":                            "broker,controller",
			"KAFKA_LISTENERS":                                "PLAINTEXT://:9092,CONTROLLER://:9093",
			"KAFKA_ADVERTISED_LISTENERS":                     fmt.Sprintf("PLAINTEXT://localhost:%s", mappedPort),
			"KAFKA_CONTROLLER_LISTENER_NAMES":                "CONTROLLER",
			"KAFKA_LISTENER_SECURITY_PROTOCOL_MAP":           "CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT",
			"KAFKA_CONTROLLER_QUORUM_VOTERS":                 "1@localhost:9093",
			"KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR":         "1",
			"KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR": "1",
			"KAFKA_TRANSACTION_STATE_LOG_MIN_ISR":            "1",
			"KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS":         "0",
			"KAFKA_NUM_PARTITIONS":                           "1",
		},
		ExposedPorts: []string{internalPort + "/tcp"},
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			hostConfig.PortBindings = nat.PortMap{
				nat.Port(internalPort + "/tcp"): []nat.PortBinding{
					{
						HostIP:   "0.0.0.0",
						HostPort: mappedPort,
					},
				},
			}
		},
		WaitingFor: wait.ForLog("Kafka Server started").WithStartupTimeout(startupTimeout),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start container: %w", err)
	}

	brokers := []string{fmt.Sprintf("localhost:%s", mappedPort)}

	terminate := func() {
		if err := container.Terminate(ctx); err != nil {
			fmt.Printf("failed to terminate container: %v\n", err)
		}
	}

	return brokers, terminate, nil
}

func isDockerRunning(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "docker", "info")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}
