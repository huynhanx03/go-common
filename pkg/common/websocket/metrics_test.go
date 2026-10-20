package websocket

import "testing"

func TestMetricsSnapshotCountsActiveConnectionsAndCloseClassOnce(t *testing.T) {
	hub := newUnitHub(t, mailboxOptions())
	connection := newUnitConnection(t, hub, "connection-1", "user:1")

	if got := hub.Metrics().ActiveConnections; got != 1 {
		t.Fatalf("active connections = %d, want 1", got)
	}
	hub.mu.Lock()
	hub.markClosingLocked(connection, CloseOptions{Code: ClosePolicyViolation, Reason: "policy"})
	hub.markClosingLocked(connection, CloseOptions{Code: ClosePolicyViolation, Reason: "policy"})
	hub.mu.Unlock()

	snapshot := hub.Metrics()
	if snapshot.ActiveConnections != 0 || snapshot.Closes.Policy != 1 {
		t.Fatalf("snapshot after close = %+v", snapshot)
	}
}

func TestCloseMetricClassIsBounded(t *testing.T) {
	tests := []struct {
		code int
		want closeMetricClass
	}{
		{CloseNormal, closeMetricNormal},
		{CloseGoingAway, closeMetricShutdown},
		{CloseServiceRestart, closeMetricShutdown},
		{CloseAuthenticationGone, closeMetricAuth},
		{ClosePolicyViolation, closeMetricPolicy},
		{CloseTryAgainLater, closeMetricSlowClient},
		{CloseProtocolError, closeMetricProtocol},
		{CloseMessageTooBig, closeMetricProtocol},
		{CloseInternalError, closeMetricInternal},
		{4999, closeMetricPolicy},
		{2999, closeMetricInternal},
	}
	for _, test := range tests {
		if got := classifyCloseMetric(test.code); got != test.want {
			t.Errorf("classifyCloseMetric(%d) = %d, want %d", test.code, got, test.want)
		}
	}
}
