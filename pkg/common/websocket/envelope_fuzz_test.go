package websocket_test

import (
	"testing"

	"github.com/huynhanx03/go-common/pkg/common/websocket"
)

func FuzzDecodeEnvelope(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{"v":1,"op":"ping","type":"protocol.ping.v1"}`),
		[]byte(`{"v":2,"op":"event","type":"event.v1"}`),
		[]byte(`{"v":1,"v":1}`),
		{0xff},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, frame []byte) {
		const limit = int64(1024)
		_, _ = websocket.DecodeEnvelope(frame, limit)
	})
}
