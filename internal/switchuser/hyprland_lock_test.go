package switchuser

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"testing"
	"time"
)

func TestWaylandMessageRoundTrip(t *testing.T) {
	payload := uint32Payload(99)
	encoded, err := encodeWaylandMessage(7, 3, payload)
	if err != nil {
		t.Fatalf("encodeWaylandMessage() error = %v", err)
	}

	sender, opcode, gotPayload, err := readWaylandMessage(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("readWaylandMessage() error = %v", err)
	}
	if sender != 7 || opcode != 3 || !bytes.Equal(gotPayload, payload) {
		t.Fatalf("message = sender %d, opcode %d, payload %v", sender, opcode, gotPayload)
	}
}

func TestDecodeWaylandGlobal(t *testing.T) {
	payload := appendUint32(nil, 42)
	payload = appendWaylandString(payload, hyprLockNotifierInterface)
	payload = appendUint32(payload, 1)

	name, iface, version, err := decodeWaylandGlobal(payload)
	if err != nil {
		t.Fatalf("decodeWaylandGlobal() error = %v", err)
	}
	if name != 42 || iface != hyprLockNotifierInterface || version != 1 {
		t.Fatalf("global = (%d, %q, %d)", name, iface, version)
	}
}

func TestLockWatcherTracksProtocolState(t *testing.T) {
	watcher := &hyprlandLockWatcher{
		events: make(chan struct{}, 1),
		errors: make(chan error, 1),
	}

	if err := watcher.handleMessage(hyprLockNotificationID, 0, nil); err != nil {
		t.Fatalf("locked event error = %v", err)
	}
	locked, err := watcher.State()
	if err != nil || !locked {
		t.Fatalf("State() after locked = %t, %v", locked, err)
	}

	if err := watcher.handleMessage(hyprLockNotificationID, 1, nil); err != nil {
		t.Fatalf("unlocked event error = %v", err)
	}
	locked, err = watcher.State()
	if err != nil || locked {
		t.Fatalf("State() after unlocked = %t, %v", locked, err)
	}
}

func TestReadWaylandMessageRejectsBadSize(t *testing.T) {
	var header [8]byte
	binary.NativeEndian.PutUint32(header[0:4], 1)
	binary.NativeEndian.PutUint32(header[4:8], uint32(7)<<16)
	if _, _, _, err := readWaylandMessage(bytes.NewReader(header[:])); err == nil {
		t.Fatal("readWaylandMessage() accepted an unaligned short message")
	}
}

func TestLiveHyprlandLockWatcher(t *testing.T) {
	if os.Getenv("HYPR_SWITCH_USER_LIVE_TEST") != "1" {
		t.Skip("set HYPR_SWITCH_USER_LIVE_TEST=1 to probe the current compositor read-only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	watcher, err := newHyprlandLockWatcher(ctx)
	if err != nil {
		t.Fatalf("newHyprlandLockWatcher() error = %v", err)
	}
	if _, err := watcher.State(); err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if watcher.PeerPID() == 0 {
		t.Fatal("PeerPID() returned zero")
	}
	if err := watcher.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
